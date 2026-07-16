package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

type uploadResult struct {
	Success bool      `json:"success"`
	Image   *Image    `json:"image,omitempty"`
	Error   *apiError `json:"error,omitempty"`
}

func (a *App) uploadImages(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings(r.Context(), a.db)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取上传设置失败")
		return
	}
	maxBody := settings.MaxFileSize*int64(settings.MaxBatchCount) + int64(settings.MaxBatchCount)*(1<<20)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, r, 400, "INVALID_MULTIPART", "上传内容无效或请求超过限制")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, r, 400, "FILE_REQUIRED", "请选择要上传的图片")
		return
	}
	if len(files) > settings.MaxBatchCount {
		writeError(w, r, 400, "TOO_MANY_FILES", fmt.Sprintf("单批最多上传 %d 个文件", settings.MaxBatchCount))
		return
	}
	storageID := strings.TrimSpace(r.FormValue("storage_id"))
	if storageID == "" {
		storageID = settings.DefaultStorageID
	}
	record, err := a.storageRecord(r.Context(), storageID)
	if err != nil || !record.Enabled {
		writeError(w, r, 400, "STORAGE_UNAVAILABLE", "所选存储不存在或未启用")
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		writeError(w, r, 500, "STORAGE_CONFIG_ERROR", "存储配置不可用")
		return
	}
	results := make([]uploadResult, 0, len(files))
	failures := 0
	for _, header := range files {
		img, apiErr := a.uploadOne(r, settings, record, backend, header)
		if apiErr != nil {
			failures++
			results = append(results, uploadResult{Success: false, Error: apiErr})
		} else {
			results = append(results, uploadResult{Success: true, Image: &img})
		}
	}
	if len(results) == 1 {
		if results[0].Success {
			writeData(w, r, 201, results[0].Image)
			return
		}
		writeError(w, r, 400, results[0].Error.Code, results[0].Error.Message)
		return
	}
	status := http.StatusCreated
	if failures > 0 {
		status = http.StatusMultiStatus
	}
	writeData(w, r, status, map[string]any{"items": results, "total": len(results), "succeeded": len(results) - failures, "failed": failures})
}

func (a *App) uploadOne(r *http.Request, settings Settings, record StorageRecord, backend storageBackend, header *multipart.FileHeader) (Image, *apiError) {
	if header.Size > settings.MaxFileSize {
		return Image{}, &apiError{Code: "FILE_TOO_LARGE", Message: fmt.Sprintf("文件超过 %d MB，请压缩后重试", settings.MaxFileSize>>20)}
	}
	file, err := header.Open()
	if err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "无法读取上传文件"}
	}
	defer file.Close()
	temp, err := osCreateTemp(a.cfg.DataDir)
	if err != nil {
		return Image{}, &apiError{Code: "TEMP_FILE_ERROR", Message: "服务器暂时无法接收文件"}
	}
	tempName := temp.Name()
	defer removeFile(tempName)
	defer temp.Close()
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(file, settings.MaxFileSize+1))
	if err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	if written > settings.MaxFileSize {
		return Image{}, &apiError{Code: "FILE_TOO_LARGE", Message: fmt.Sprintf("文件超过 %d MB，请压缩后重试", settings.MaxFileSize>>20)}
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	buffer := make([]byte, 512)
	n, _ := io.ReadFull(temp, buffer)
	buffer = buffer[:n]
	mime := detectImageMIME(buffer)
	if mime == "" || !contains(settings.AllowedTypes, mime) {
		return Image{}, &apiError{Code: "UNSUPPORTED_FILE_TYPE", Message: "文件不是允许上传的图片格式"}
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	config, format, err := image.DecodeConfig(temp)
	if err != nil || mimeForFormat(format) != mime {
		return Image{}, &apiError{Code: "INVALID_IMAGE", Message: "图片内容损坏或格式与文件内容不一致"}
	}
	hash := fmt.Sprintf("%x", hasher.Sum(nil))
	if !settings.AllowDuplicates {
		existing, err := a.findDuplicate(r, hash, record.ID)
		if err == nil {
			return existing, nil
		}
	}
	id := newUUIDv7()
	key := objectKey(settings.NamingRule, id, header.Filename, mime)
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	ctx, cancel := contextWithTimeout(r, 2*time.Minute)
	defer cancel()
	storedKey, err := backend.Put(ctx, key, temp, written, mime)
	if err != nil {
		a.logger.Error("图片写入存储失败", "request_id", requestID(r), "storage_id", record.ID, "error", err)
		return Image{}, &apiError{Code: "STORAGE_WRITE_FAILED", Message: "图片写入存储失败，请稍后重试"}
	}
	img := Image{ID: id, Hash: hash, OriginalName: safeOriginalName(header.Filename), ObjectKey: storedKey, StorageType: record.Type, StorageID: record.ID, MIMEType: mime, Size: written, Width: config.Width, Height: config.Height, PublicURL: publicURL(record, storedKey), CreatedAt: nowUTC()}
	_, err = a.db.ExecContext(r.Context(), "INSERT INTO images(id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,width,height,public_url,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", img.ID, img.Hash, img.OriginalName, img.ObjectKey, img.StorageType, img.StorageID, img.MIMEType, img.Size, img.Width, img.Height, img.PublicURL, img.CreatedAt)
	if err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if rollbackErr := backend.Delete(rollbackCtx, storedKey); rollbackErr != nil {
			a.logger.Error("元数据保存失败且存储回滚失败", "request_id", requestID(r), "storage_id", record.ID, "object_key", storedKey, "error", rollbackErr)
		}
		return Image{}, &apiError{Code: "DATABASE_ERROR", Message: "图片信息保存失败，已尝试回滚上传"}
	}
	return img, nil
}

func detectImageMIME(data []byte) string {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}
func mimeForFormat(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return ""
}
func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeOriginalName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "image"
	}
	if len(name) > 255 {
		name = name[len(name)-255:]
	}
	return name
}
func objectKey(rule, id, original, mime string) string {
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}[mime]
	switch rule {
	case "date":
		return time.Now().UTC().Format("2006/01/02/") + id + ext
	case "original":
		base := strings.TrimSuffix(safeOriginalName(original), path.Ext(original))
		base = strings.Trim(unsafeName.ReplaceAllString(base, "-"), "-._")
		if base == "" {
			base = "image"
		}
		return base + "-" + id[:8] + ext
	default:
		return id + ext
	}
}

func newUUIDv7() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func osCreateTemp(dataDir string) (*os.File, error) {
	dir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "upload-*")
}
func removeFile(name string) { _ = os.Remove(name) }

func scanImage(scanner interface{ Scan(...any) error }) (Image, error) {
	var img Image
	var width, height sql.NullInt64
	var deleteError sql.NullString
	err := scanner.Scan(&img.ID, &img.Hash, &img.OriginalName, &img.ObjectKey, &img.StorageType, &img.StorageID, &img.MIMEType, &img.Size, &width, &height, &img.PublicURL, &deleteError, &img.CreatedAt)
	if width.Valid {
		img.Width = int(width.Int64)
	}
	if height.Valid {
		img.Height = int(height.Int64)
	}
	if deleteError.Valid {
		img.DeleteError = deleteError.String
	}
	return img, err
}

const imageColumns = "id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,width,height,public_url,delete_error,created_at"

func (a *App) findDuplicate(r *http.Request, hash, storageID string) (Image, error) {
	return scanImage(a.db.QueryRowContext(r.Context(), "SELECT "+imageColumns+" FROM images WHERE hash=? AND storage_id=? AND delete_error IS NULL ORDER BY created_at DESC LIMIT 1", hash, storageID))
}

func (a *App) getImage(w http.ResponseWriter, r *http.Request) {
	img, err := scanImage(a.db.QueryRowContext(r.Context(), "SELECT "+imageColumns+" FROM images WHERE id=?", r.PathValue("id")))
	if isNotFound(err) {
		writeError(w, r, 404, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取图片失败")
		return
	}
	writeData(w, r, 200, img)
}

func (a *App) randomImage(w http.ResponseWriter, r *http.Request) {
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format != "" && format != "json" {
		writeError(w, r, http.StatusBadRequest, "INVALID_FORMAT", "format 只能是 json 或留空")
		return
	}

	query := "SELECT " + imageColumns + " FROM images WHERE delete_error IS NULL"
	args := []any{}
	if storageID := strings.TrimSpace(r.URL.Query().Get("storage_id")); storageID != "" {
		query += " AND storage_id=?"
		args = append(args, storageID)
	}
	query += " ORDER BY RANDOM() LIMIT 1"

	img, err := scanImage(a.db.QueryRowContext(r.Context(), query, args...))
	if isNotFound(err) {
		writeError(w, r, http.StatusNotFound, "RANDOM_IMAGE_NOT_FOUND", "没有符合条件的图片")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取随机图片失败")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	if format == "json" {
		writeData(w, r, http.StatusOK, map[string]any{
			"id": img.ID, "url": img.PublicURL, "mime_type": img.MIMEType,
			"width": img.Width, "height": img.Height,
		})
		return
	}
	w.Header().Set("Location", img.PublicURL)
	w.WriteHeader(http.StatusFound)
}

func (a *App) listImages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	query := "SELECT " + imageColumns + " FROM images WHERE 1=1"
	args := []any{}
	if storage := r.URL.Query().Get("storage_id"); storage != "" {
		query += " AND storage_id=?"
		args = append(args, storage)
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		query += " AND original_name LIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(search)+"%")
	}
	if from := r.URL.Query().Get("from"); from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			writeError(w, r, 400, "INVALID_DATE", "from 必须是 ISO 8601 时间")
			return
		}
		query += " AND created_at>=?"
		args = append(args, formatTime(parsed))
	}
	if to := r.URL.Query().Get("to"); to != "" {
		parsed, err := time.Parse(time.RFC3339, to)
		if err != nil {
			writeError(w, r, 400, "INVALID_DATE", "to 必须是 ISO 8601 时间")
			return
		}
		query += " AND created_at<=?"
		args = append(args, formatTime(parsed))
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		created, id, ok := decodeCursor(cursor)
		if !ok {
			writeError(w, r, 400, "INVALID_CURSOR", "分页游标无效")
			return
		}
		query += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, created, created, id)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取图片列表失败")
		return
	}
	defer rows.Close()
	items := make([]Image, 0, limit+1)
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			writeError(w, r, 500, "DATABASE_ERROR", "读取图片列表失败")
			return
		}
		items = append(items, img)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取图片列表失败")
		return
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	writeData(w, r, 200, map[string]any{"items": items, "next_cursor": next})
}
func escapeLike(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "%", "\\%")
	return strings.ReplaceAll(v, "_", "\\_")
}
func encodeCursor(created, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(created + "\x00" + id))
}
func decodeCursor(value string) (string, string, bool) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (a *App) deleteImage(w http.ResponseWriter, r *http.Request) {
	img, err := scanImage(a.db.QueryRowContext(r.Context(), "SELECT "+imageColumns+" FROM images WHERE id=?", r.PathValue("id")))
	if isNotFound(err) {
		writeError(w, r, 404, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取图片失败")
		return
	}
	record, err := a.storageRecord(r.Context(), img.StorageID)
	if err != nil {
		_, _ = a.db.ExecContext(r.Context(), "UPDATE images SET delete_error=? WHERE id=?", "存储配置不存在", img.ID)
		writeError(w, r, 409, "STORAGE_CONFIG_MISSING", "存储配置不存在，已保留图片记录")
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		writeError(w, r, 500, "STORAGE_CONFIG_ERROR", "存储配置不可用")
		return
	}
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	if err := backend.Delete(ctx, img.ObjectKey); err != nil {
		_, _ = a.db.ExecContext(r.Context(), "UPDATE images SET delete_error=? WHERE id=?", truncate(err.Error(), 500), img.ID)
		a.logger.Error("删除存储对象失败", "request_id", requestID(r), "image_id", img.ID, "error", err)
		writeError(w, r, 502, "STORAGE_DELETE_FAILED", "存储对象删除失败，图片记录已保留，可稍后重试")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), "DELETE FROM images WHERE id=?", img.ID); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "存储对象已删除，但图片记录清理失败")
		return
	}
	writeData(w, r, 200, map[string]bool{"deleted": true})
}
func truncate(v string, max int) string {
	if len(v) > max {
		return v[:max]
	}
	return v
}

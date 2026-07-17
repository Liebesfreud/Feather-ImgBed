package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const maxImagePixels = 100_000_000

type uploadResult struct {
	Success bool      `json:"success"`
	Image   *Image    `json:"image,omitempty"`
	Error   *apiError `json:"error,omitempty"`
}

func (e *apiError) Error() string { return e.Message }

func (a *App) uploadImages(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings(r.Context(), a.db)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取上传设置失败")
		return
	}
	maxBody := settings.MaxFileSize*int64(settings.MaxBatchCount) + int64(settings.MaxBatchCount)*(1<<20)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_MULTIPART", "上传内容无效或请求超过限制")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, r, http.StatusBadRequest, "FILE_REQUIRED", "请选择要上传的图片")
		return
	}
	if len(files) > settings.MaxBatchCount {
		writeError(w, r, http.StatusBadRequest, "TOO_MANY_FILES", fmt.Sprintf("单批最多上传 %d 个文件", settings.MaxBatchCount))
		return
	}
	storageID := strings.TrimSpace(r.FormValue("storage_id"))
	if storageID == "" {
		storageID = settings.DefaultStorageID
	}
	record, err := a.storageRecord(r.Context(), storageID)
	if err != nil || !record.Enabled {
		writeError(w, r, http.StatusBadRequest, "STORAGE_UNAVAILABLE", "所选存储不存在或未启用")
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STORAGE_CONFIG_ERROR", "存储配置不可用")
		return
	}
	results := make([]uploadResult, 0, len(files))
	failures := 0
	for _, header := range files {
		img, apiErr := a.uploadOne(r, settings, record, backend, header)
		if apiErr != nil {
			failures++
			results = append(results, uploadResult{Error: apiErr})
		} else {
			results = append(results, uploadResult{Success: true, Image: &img})
		}
	}
	if len(results) == 1 {
		if results[0].Success {
			writeData(w, r, http.StatusCreated, results[0].Image)
		} else {
			writeError(w, r, http.StatusBadRequest, results[0].Error.Code, results[0].Error.Message)
		}
		return
	}
	status := http.StatusCreated
	if failures > 0 {
		status = http.StatusMultiStatus
	}
	writeData(w, r, status, map[string]any{
		"items": results, "total": len(results),
		"succeeded": len(results) - failures, "failed": failures,
	})
}

func (a *App) uploadOne(r *http.Request, settings Settings, record StorageRecord, backend storageBackend, header *multipart.FileHeader) (Image, *apiError) {
	file, err := header.Open()
	if err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "无法读取上传文件"}
	}
	defer file.Close()
	return a.ingestImageWithBackend(r.Context(), file, header.Filename, header.Size, settings, record, backend, requestID(r))
}

func (a *App) ingestImage(ctx context.Context, source io.Reader, filename string, expectedSize int64, storageID string) (Image, error) {
	settings, err := loadSettings(ctx, a.db)
	if err != nil {
		return Image{}, err
	}
	if storageID == "" {
		storageID = settings.DefaultStorageID
	}
	record, err := a.storageRecord(ctx, storageID)
	if err != nil || !record.Enabled {
		return Image{}, &apiError{Code: "STORAGE_UNAVAILABLE", Message: "所选存储不存在或未启用"}
	}
	backend, err := a.backend(record)
	if err != nil {
		return Image{}, err
	}
	img, apiErr := a.ingestImageWithBackend(ctx, source, filename, expectedSize, settings, record, backend, "")
	if apiErr != nil {
		return Image{}, apiErr
	}
	return img, nil
}

func (a *App) ingestImageWithBackend(ctx context.Context, source io.Reader, filename string, expectedSize int64, settings Settings, record StorageRecord, backend storageBackend, requestID string) (Image, *apiError) {
	if expectedSize > settings.MaxFileSize {
		return Image{}, &apiError{Code: "FILE_TOO_LARGE", Message: fmt.Sprintf("文件超过 %d MB，请压缩后重试", settings.MaxFileSize>>20)}
	}
	temp, err := osCreateTemp(a.cfg.DataDir)
	if err != nil {
		return Image{}, &apiError{Code: "TEMP_FILE_ERROR", Message: "服务器暂时无法接收文件"}
	}
	tempName := temp.Name()
	defer removeFile(tempName)
	defer temp.Close()
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(source, settings.MaxFileSize+1))
	if err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	if written > settings.MaxFileSize {
		return Image{}, &apiError{Code: "FILE_TOO_LARGE", Message: fmt.Sprintf("文件超过 %d MB，请压缩后重试", settings.MaxFileSize>>20)}
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	header := make([]byte, 512)
	n, _ := io.ReadFull(temp, header)
	mime := detectImageMIME(header[:n])
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
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return Image{}, &apiError{Code: "IMAGE_TOO_LARGE", Message: "图片像素尺寸超过安全限制"}
	}
	hash := fmt.Sprintf("%x", hasher.Sum(nil))
	if !settings.AllowDuplicates {
		if existing, duplicateErr := a.findDuplicate(ctx, hash, record.ID); duplicateErr == nil {
			return existing, nil
		}
	}
	id := newUUIDv7()
	key := objectKey(settings.NamingRule, id, filename, mime)
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return Image{}, &apiError{Code: "FILE_READ_ERROR", Message: "读取上传文件失败"}
	}
	putCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	storedKey, err := backend.Put(putCtx, key, temp, written, mime)
	cancel()
	if err != nil {
		a.logger.Error("图片写入存储失败", "request_id", requestID, "storage_id", record.ID, "error", err)
		return Image{}, &apiError{Code: "STORAGE_WRITE_FAILED", Message: "图片写入存储失败，请稍后重试"}
	}
	img := Image{
		ID: id, Hash: hash, OriginalName: safeOriginalName(filename), ObjectKey: storedKey,
		StorageType: record.Type, StorageID: record.ID, MIMEType: mime, Size: written,
		Width: config.Width, Height: config.Height, PublicURL: publicURL(record, storedKey, settings.SiteURL), CreatedAt: nowUTC(),
	}

	variants := make([]*ImageVariant, 0, 3)
	if generated, generateErr := generateThumbnail(temp, mime, id); generateErr != nil {
		a.logger.Warn("缩略图生成失败，保留原图", "request_id", requestID, "image_id", id, "error", generateErr)
	} else {
		variant, storeErr := a.storeGeneratedVariant(ctx, backend, record, settings.SiteURL, id, "thumbnail", img.CreatedAt, generated)
		if storeErr != nil {
			a.logger.Warn("缩略图写入失败，保留原图", "request_id", requestID, "image_id", id, "error", storeErr)
		} else {
			variants = append(variants, variant)
			img.ThumbnailURL = variant.PublicURL
		}
	}
	generatedVariants, processingFailures := generateProcessingVariants(temp, mime, id, settings.Processing)
	for _, processingErr := range processingFailures {
		a.logger.Warn("图片派生处理失败，保留原图", "request_id", requestID, "image_id", id, "error", processingErr)
	}
	for _, generated := range generatedVariants {
		variant, storeErr := a.storeGeneratedVariant(ctx, backend, record, settings.SiteURL, id, generated.Kind, img.CreatedAt, generated.Image)
		if storeErr != nil {
			a.logger.Warn("图片派生版本写入失败，保留原图", "request_id", requestID, "image_id", id, "kind", generated.Kind, "error", storeErr)
			continue
		}
		variants = append(variants, variant)
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO images(
			id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,width,height,public_url,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			img.ID, img.Hash, img.OriginalName, img.ObjectKey, img.StorageType, img.StorageID,
			img.MIMEType, img.Size, img.Width, img.Height, img.PublicURL, img.CreatedAt)
	}
	if err == nil {
		for _, variant := range variants {
			_, err = tx.ExecContext(ctx, `INSERT INTO image_variants(
				id,image_id,kind,object_key,public_url,mime_type,size,width,height,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				variant.ID, variant.ImageID, variant.Kind, variant.ObjectKey, variant.PublicURL,
				variant.MIMEType, variant.Size, variant.Width, variant.Height, variant.CreatedAt)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		a.rollbackStoredObjects(backend, requestID, record.ID, storedKey, variants)
		return Image{}, &apiError{Code: "DATABASE_ERROR", Message: "图片信息保存失败，已尝试回滚上传"}
	}
	return img, nil
}

func (a *App) storeGeneratedVariant(
	ctx context.Context,
	backend storageBackend,
	record StorageRecord,
	siteURL, imageID, kind, createdAt string,
	generated generatedImage,
) (*ImageVariant, error) {
	putCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	storedKey, err := backend.Put(putCtx, generated.ObjectKey, generated.Reader, generated.Size, generated.MIMEType)
	if err != nil {
		return nil, err
	}
	return &ImageVariant{
		ID: newUUIDv7(), ImageID: imageID, Kind: kind, ObjectKey: storedKey,
		PublicURL: publicURL(record, storedKey, siteURL), MIMEType: generated.MIMEType,
		Size: generated.Size, Width: generated.Width, Height: generated.Height, CreatedAt: createdAt,
	}, nil
}

func (a *App) rollbackStoredObjects(backend storageBackend, requestID, storageID, originalKey string, variants []*ImageVariant) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	keys := []string{originalKey}
	for _, variant := range variants {
		keys = append([]string{variant.ObjectKey}, keys...)
	}
	for _, key := range keys {
		if err := backend.Delete(rollbackCtx, key); err != nil {
			a.logger.Error("元数据保存失败且存储回滚失败", "request_id", requestID, "storage_id", storageID, "object_key", key, "error", err)
		}
	}
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
	default:
		return ""
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
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
	var value [16]byte
	_, _ = rand.Read(value[:])
	ms := uint64(time.Now().UnixMilli())
	value[0], value[1], value[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	value[3], value[4], value[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func osCreateTemp(dataDir string) (*os.File, error) {
	dir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "upload-*")
}

func removeFile(name string) { _ = os.Remove(name) }

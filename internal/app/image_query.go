package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const imageColumns = `i.id,i.hash,i.original_name,i.object_key,i.storage_type,i.storage_id,
	i.mime_type,i.size,i.width,i.height,i.public_url,i.delete_error,i.deleted_at,i.purge_error,
	i.favorite,i.created_at,COALESCE((SELECT public_url FROM image_variants v
	WHERE v.image_id=i.id AND v.kind='thumbnail'),'')`

func scanImage(scanner interface{ Scan(...any) error }) (Image, error) {
	var img Image
	var width, height sql.NullInt64
	var deleteError, deletedAt, purgeError sql.NullString
	var favorite int
	err := scanner.Scan(
		&img.ID, &img.Hash, &img.OriginalName, &img.ObjectKey, &img.StorageType, &img.StorageID,
		&img.MIMEType, &img.Size, &width, &height, &img.PublicURL, &deleteError, &deletedAt,
		&purgeError, &favorite, &img.CreatedAt, &img.ThumbnailURL,
	)
	if width.Valid {
		img.Width = int(width.Int64)
	}
	if height.Valid {
		img.Height = int(height.Int64)
	}
	if deleteError.Valid {
		img.DeleteError = deleteError.String
	}
	if deletedAt.Valid {
		img.DeletedAt = deletedAt.String
	}
	if purgeError.Valid {
		img.PurgeError = purgeError.String
	}
	img.Favorite = favorite == 1
	return img, err
}

func (a *App) findDuplicate(ctx context.Context, hash, storageID string) (Image, error) {
	return scanImage(a.db.QueryRowContext(ctx, "SELECT "+imageColumns+" FROM images i WHERE i.hash=? AND i.storage_id=? AND i.deleted_at IS NULL ORDER BY i.created_at DESC LIMIT 1", hash, storageID))
}

func (a *App) getImage(w http.ResponseWriter, r *http.Request) {
	img, err := scanImage(a.db.QueryRowContext(r.Context(), "SELECT "+imageColumns+" FROM images i WHERE i.id=? AND i.deleted_at IS NULL", r.PathValue("id")))
	if isNotFound(err) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片失败")
		return
	}
	img.Variants, err = listImageVariants(r.Context(), a.db, img.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片派生版本失败")
		return
	}
	writeData(w, r, http.StatusOK, img)
}

func (a *App) randomImage(w http.ResponseWriter, r *http.Request) {
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format != "" && format != "json" {
		writeError(w, r, http.StatusBadRequest, "INVALID_FORMAT", "format 只能是 json 或留空")
		return
	}
	storageID := strings.TrimSpace(r.URL.Query().Get("storage_id"))
	maxQuery := "SELECT COALESCE(MAX(rowid),0) FROM images WHERE deleted_at IS NULL"
	maxArgs := []any{}
	if storageID != "" {
		maxQuery += " AND storage_id=?"
		maxArgs = append(maxArgs, storageID)
	}
	var maxRowID int64
	if err := a.db.QueryRowContext(r.Context(), maxQuery, maxArgs...).Scan(&maxRowID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取随机图片失败")
		return
	}
	query := "SELECT " + imageColumns + " FROM images i WHERE i.deleted_at IS NULL"
	args := []any{}
	if storageID != "" {
		query += " AND i.storage_id=?"
		args = append(args, storageID)
	}
	if maxRowID > 0 {
		query += " AND i.rowid>=((random() & 9223372036854775807) % ?) ORDER BY i.rowid LIMIT 1"
		args = append(args, maxRowID+1)
	} else {
		query += " LIMIT 1"
	}
	img, err := scanImage(a.db.QueryRowContext(r.Context(), query, args...))
	if isNotFound(err) && maxRowID > 0 {
		fallback := "SELECT " + imageColumns + " FROM images i WHERE i.deleted_at IS NULL"
		fallbackArgs := []any{}
		if storageID != "" {
			fallback += " AND i.storage_id=?"
			fallbackArgs = append(fallbackArgs, storageID)
		}
		fallback += " ORDER BY i.rowid LIMIT 1"
		img, err = scanImage(a.db.QueryRowContext(r.Context(), fallback, fallbackArgs...))
	}
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
			"id": img.ID, "url": img.PublicURL, "thumbnail_url": img.ThumbnailURL,
			"mime_type": img.MIMEType, "width": img.Width, "height": img.Height,
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
	order := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("order")))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ORDER", "order 只能是 asc 或 desc")
		return
	}
	query := "SELECT " + imageColumns + " FROM images i WHERE i.deleted_at IS NULL"
	args := []any{}
	if storage := r.URL.Query().Get("storage_id"); storage != "" {
		query += " AND i.storage_id=?"
		args = append(args, storage)
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		if len([]rune(search)) >= 3 {
			query += " AND i.rowid IN (SELECT rowid FROM image_search WHERE original_name MATCH ?)"
			args = append(args, `"`+strings.ReplaceAll(search, `"`, `""`)+`"`)
		} else {
			query += " AND i.original_name LIKE ? ESCAPE '\\'"
			args = append(args, "%"+escapeLike(search)+"%")
		}
	}
	if favorite := r.URL.Query().Get("favorite"); favorite != "" {
		if favorite != "true" && favorite != "false" {
			writeError(w, r, http.StatusBadRequest, "INVALID_FAVORITE", "favorite 必须是 true 或 false")
			return
		}
		query += " AND i.favorite=?"
		args = append(args, favorite == "true")
	}
	for _, filter := range []struct {
		Name     string
		Operator string
	}{
		{Name: "from", Operator: ">="},
		{Name: "to", Operator: "<="},
	} {
		if value := r.URL.Query().Get(filter.Name); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(w, r, http.StatusBadRequest, "INVALID_DATE", filter.Name+" 必须是 ISO 8601 时间")
				return
			}
			query += " AND i.created_at" + filter.Operator + "?"
			args = append(args, formatTime(parsed))
		}
	}
	if tagID := strings.TrimSpace(r.URL.Query().Get("tag_id")); tagID != "" {
		query += " AND EXISTS(SELECT 1 FROM image_tags it WHERE it.image_id=i.id AND it.tag_id=?)"
		args = append(args, tagID)
	}
	if albumID := strings.TrimSpace(r.URL.Query().Get("album_id")); albumID != "" {
		query += " AND EXISTS(SELECT 1 FROM album_images ai WHERE ai.image_id=i.id AND ai.album_id=?)"
		args = append(args, albumID)
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		cursorOrder, created, id, ok := decodeCursor(cursor)
		if !ok || cursorOrder != order {
			writeError(w, r, http.StatusBadRequest, "INVALID_CURSOR", "分页游标无效或排序方向已改变")
			return
		}
		operator := "<"
		if order == "asc" {
			operator = ">"
		}
		query += " AND (i.created_at" + operator + "? OR (i.created_at=? AND i.id" + operator + "?))"
		args = append(args, created, created, id)
	}
	direction := "DESC"
	if order == "asc" {
		direction = "ASC"
	}
	query += " ORDER BY i.created_at " + direction + ",i.id " + direction + " LIMIT ?"
	args = append(args, limit+1)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片列表失败")
		return
	}
	defer rows.Close()
	items := make([]Image, 0, limit+1)
	for rows.Next() {
		img, scanErr := scanImage(rows)
		if scanErr != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片列表失败")
			return
		}
		items = append(items, img)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片列表失败")
		return
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(order, last.CreatedAt, last.ID)
		items = items[:limit]
	}
	writeData(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

func encodeCursor(order, created, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(order + "\x00" + created + "\x00" + id))
}

func decodeCursor(value string) (string, string, string, bool) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", "", false
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) != 3 || (parts[0] != "asc" && parts[0] != "desc") || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

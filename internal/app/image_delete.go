package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxImageBatch = 100

func (a *App) deleteImage(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.ExecContext(r.Context(), `UPDATE images
		SET deleted_at=?,purge_error=NULL,delete_error=NULL
		WHERE id=? AND deleted_at IS NULL`, nowUTC(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "移入回收站失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"trashed": true})
}

func (a *App) bulkImages(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
		Value  *bool    `json:"value"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	ids, message := cleanRequiredIDs(input.IDs, maxImageBatch)
	if message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMAGE_IDS", message)
		return
	}
	var (
		requested int
		affected  int64
		notFound  int64
		err       error
	)
	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "trash":
		requested, affected, notFound, err = a.bulkTrashImages(r.Context(), ids)
	case "favorite":
		if input.Value == nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_FAVORITE", "favorite 操作必须提供布尔值 value")
			return
		}
		requested, affected, notFound, err = a.bulkSetFavorite(r.Context(), ids, *input.Value)
	default:
		writeError(w, r, http.StatusBadRequest, "INVALID_BULK_ACTION", "不支持的批量操作")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "批量更新图片失败")
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{
		"requested": requested,
		"affected":  affected,
		"not_found": notFound,
	})
}

func (a *App) bulkTrashImages(ctx context.Context, ids []string) (int, int64, int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return len(ids), 0, 0, err
	}
	defer tx.Rollback()
	deletedAt := nowUTC()
	var affected int64
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE images
			SET deleted_at=?,purge_error=NULL,delete_error=NULL
			WHERE id=? AND deleted_at IS NULL`, deletedAt, id)
		if err != nil {
			return len(ids), 0, 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return len(ids), 0, 0, err
		}
		affected += count
	}
	if err := tx.Commit(); err != nil {
		return len(ids), 0, 0, err
	}
	return len(ids), affected, int64(len(ids)) - affected, nil
}

func (a *App) listTrash(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	query := "SELECT " + imageColumns + " FROM images i WHERE i.deleted_at IS NOT NULL"
	args := []any{}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		deletedAt, id, ok := decodeTrashCursor(cursor)
		if !ok {
			writeError(w, r, http.StatusBadRequest, "INVALID_CURSOR", "回收站分页游标无效")
			return
		}
		query += " AND (i.deleted_at<? OR (i.deleted_at=? AND i.id<?))"
		args = append(args, deletedAt, deletedAt, id)
	}
	query += " ORDER BY i.deleted_at DESC,i.id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
		return
	}
	defer rows.Close()
	items := make([]Image, 0, limit+1)
	for rows.Next() {
		item, err := scanImage(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
		return
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeTrashCursor(last.DeletedAt, last.ID)
		items = items[:limit]
	}
	writeData(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *App) restoreImage(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.ExecContext(r.Context(), `UPDATE images
		SET deleted_at=NULL,purge_error=NULL,delete_error=NULL
		WHERE id=? AND deleted_at IS NOT NULL`, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "恢复图片失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "回收站中没有这张图片")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"restored": true})
}

func (a *App) purgeImage(w http.ResponseWriter, r *http.Request) {
	err := a.permanentlyDeleteImage(r.Context(), r.PathValue("id"))
	if isNotFound(err) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "回收站中没有这张图片")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PURGE_FAILED", "永久删除图片失败，已保留记录供重试")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

type purgeResult struct {
	ID      string    `json:"id"`
	Success bool      `json:"success"`
	Error   *apiError `json:"error,omitempty"`
}

func (a *App) purgeTrash(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var ids []string
	if input.All {
		if len(input.IDs) != 0 {
			writeError(w, r, http.StatusBadRequest, "INVALID_PURGE_REQUEST", "all 与 ids 不能同时提供")
			return
		}
		rows, err := a.db.QueryContext(r.Context(), `SELECT id FROM images
			WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC,id DESC`)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
			return
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
				return
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取回收站失败")
			return
		}
	} else {
		var message string
		ids, message = cleanRequiredIDs(input.IDs, maxImageBatch)
		if message != "" {
			writeError(w, r, http.StatusBadRequest, "INVALID_IMAGE_IDS", message)
			return
		}
	}
	results := make([]purgeResult, 0, len(ids))
	failures := 0
	for _, id := range ids {
		err := a.permanentlyDeleteImage(r.Context(), id)
		if err == nil {
			results = append(results, purgeResult{ID: id, Success: true})
			continue
		}
		failures++
		code, message := "PURGE_FAILED", "永久删除失败，已保留记录供重试"
		if isNotFound(err) {
			code, message = "IMAGE_NOT_FOUND", "回收站中没有这张图片"
		}
		results = append(results, purgeResult{ID: id, Error: &apiError{Code: code, Message: message}})
	}
	status := http.StatusOK
	if failures > 0 {
		status = http.StatusMultiStatus
	}
	writeData(w, r, status, map[string]any{
		"items":     results,
		"total":     len(results),
		"succeeded": len(results) - failures,
		"failed":    failures,
	})
}

func (a *App) permanentlyDeleteImage(ctx context.Context, id string) error {
	img, err := scanImage(a.db.QueryRowContext(ctx, "SELECT "+imageColumns+" FROM images i WHERE i.id=? AND i.deleted_at IS NOT NULL", id))
	if err != nil {
		return err
	}
	variants, err := listImageVariants(ctx, a.db, id)
	if err != nil {
		a.recordPurgeError(id, err)
		return err
	}
	record, err := a.storageRecord(ctx, img.StorageID)
	if err != nil {
		a.recordPurgeError(id, err)
		return err
	}
	backend, err := a.backend(record)
	if err != nil {
		a.recordPurgeError(id, err)
		return err
	}
	deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var deleteErrors []error
	for _, variant := range variants {
		if err := backend.Delete(deleteCtx, variant.ObjectKey); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("删除派生对象 %s 失败: %w", variant.Kind, err))
		}
	}
	if err := backend.Delete(deleteCtx, img.ObjectKey); err != nil {
		deleteErrors = append(deleteErrors, fmt.Errorf("删除原图失败: %w", err))
	}
	if err := errors.Join(deleteErrors...); err != nil {
		a.recordPurgeError(id, err)
		return err
	}
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dbCancel()
	if _, err := a.db.ExecContext(dbCtx, "DELETE FROM images WHERE id=?", id); err != nil {
		a.recordPurgeError(id, err)
		return err
	}
	return nil
}

func (a *App) recordPurgeError(id string, purgeErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.db.ExecContext(ctx, `UPDATE images SET purge_error=? WHERE id=? AND deleted_at IS NOT NULL`,
		truncate(purgeErr.Error(), 500), id)
	if err != nil {
		a.logger.Error("记录图片清理错误失败", "image_id", id, "error", err)
	}
}

func truncate(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum])
	}
	return value
}

func encodeTrashCursor(deletedAt, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("trash\x00" + deletedAt + "\x00" + id))
}

func decodeTrashCursor(value string) (string, string, bool) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) != 3 || parts[0] != "trash" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

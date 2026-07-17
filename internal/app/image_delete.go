package app

import (
	"context"
	"net/http"
	"time"
)

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

func (a *App) permanentlyDeleteImage(ctx context.Context, id string) error {
	img, err := scanImage(a.db.QueryRowContext(ctx, "SELECT "+imageColumns+" FROM images i WHERE i.id=? AND i.deleted_at IS NOT NULL", id))
	if err != nil {
		return err
	}
	variants, err := listImageVariants(ctx, a.db, id)
	if err != nil {
		return err
	}
	record, err := a.storageRecord(ctx, img.StorageID)
	if err != nil {
		return err
	}
	backend, err := a.backend(record)
	if err != nil {
		return err
	}
	deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, variant := range variants {
		if err := backend.Delete(deleteCtx, variant.ObjectKey); err != nil {
			return err
		}
	}
	if err := backend.Delete(deleteCtx, img.ObjectKey); err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, "DELETE FROM images WHERE id=?", id)
	return err
}

func truncate(value string, maximum int) string {
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

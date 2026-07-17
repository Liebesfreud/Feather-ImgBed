package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

const maxOrganizationBatch = 100

var tagColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Tag struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	ImageCount int    `json:"image_count"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type Album struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	CoverImageID string `json:"cover_image_id,omitempty"`
	CoverURL     string `json:"cover_url,omitempty"`
	ImageCount   int    `json:"image_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func (a *App) registerOrganizationRoutes() {
	a.mux.Handle("PATCH /api/v1/images/{id}", a.requireAuth(http.HandlerFunc(a.patchImage)))
	a.mux.Handle("GET /api/v1/images/{id}/tags", a.requireAuth(http.HandlerFunc(a.getImageTags)))
	a.mux.Handle("PUT /api/v1/images/{id}/tags", a.requireAuth(http.HandlerFunc(a.putImageTags)))
	a.mux.Handle("POST /api/v1/images/bulk/tags", a.requireAuth(http.HandlerFunc(a.bulkImageTags)))

	a.mux.Handle("GET /api/v1/tags", a.requireAuth(http.HandlerFunc(a.listTags)))
	a.mux.Handle("POST /api/v1/tags", a.requireAuth(http.HandlerFunc(a.createTag)))
	a.mux.Handle("PUT /api/v1/tags/{id}", a.requireAuth(http.HandlerFunc(a.updateTag)))
	a.mux.Handle("DELETE /api/v1/tags/{id}", a.requireAuth(http.HandlerFunc(a.deleteTag)))

	a.mux.Handle("GET /api/v1/albums", a.requireAuth(http.HandlerFunc(a.listAlbums)))
	a.mux.Handle("POST /api/v1/albums", a.requireAuth(http.HandlerFunc(a.createAlbum)))
	a.mux.Handle("GET /api/v1/albums/{id}", a.requireAuth(http.HandlerFunc(a.getAlbum)))
	a.mux.Handle("PUT /api/v1/albums/{id}", a.requireAuth(http.HandlerFunc(a.updateAlbum)))
	a.mux.Handle("DELETE /api/v1/albums/{id}", a.requireAuth(http.HandlerFunc(a.deleteAlbum)))
	a.mux.Handle("POST /api/v1/albums/{id}/images", a.requireAuth(http.HandlerFunc(a.addAlbumImages)))
	a.mux.Handle("DELETE /api/v1/albums/{id}/images/{image_id}", a.requireAuth(http.HandlerFunc(a.removeAlbumImage)))
}

func (a *App) patchImage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Favorite *bool `json:"favorite"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Favorite == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMAGE_UPDATE", "必须提供 favorite")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE images SET favorite=? WHERE id=? AND deleted_at IS NULL`, *input.Favorite, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新图片失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	img, err := scanImage(a.db.QueryRowContext(r.Context(), "SELECT "+imageColumns+" FROM images i WHERE i.id=?", r.PathValue("id")))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片失败")
		return
	}
	writeData(w, r, http.StatusOK, img)
}

func (a *App) bulkSetFavorite(ctx context.Context, ids []string, favorite bool) (int, int64, int64, error) {
	ids, message := cleanRequiredIDs(ids, maxOrganizationBatch)
	if message != "" {
		return 0, 0, 0, errors.New(message)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	var affected int64
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE images SET favorite=? WHERE id=? AND deleted_at IS NULL`, favorite, id)
		if err != nil {
			return 0, 0, 0, err
		}
		count, _ := result.RowsAffected()
		affected += count
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	requested := len(ids)
	return requested, affected, int64(requested) - affected, nil
}

func (a *App) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := queryTags(r.Context(), a.db, "")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取标签失败")
		return
	}
	writeData(w, r, http.StatusOK, tags)
}

func (a *App) createTag(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Color = strings.ToLower(strings.TrimSpace(input.Color))
	if message := validateTag(input.Name, input.Color); message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_TAG", message)
		return
	}
	now := nowUTC()
	id := newUUIDv7()
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO tags(id,name,color,created_at,updated_at) VALUES(?,?,?,?,?)`, id, input.Name, input.Color, now, now)
	if isUniqueConstraint(err) {
		writeError(w, r, http.StatusConflict, "TAG_NAME_EXISTS", "标签名称已存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "创建标签失败")
		return
	}
	tag := Tag{ID: id, Name: input.Name, Color: input.Color, CreatedAt: now, UpdatedAt: now}
	writeData(w, r, http.StatusCreated, tag)
}

func (a *App) updateTag(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Color = strings.ToLower(strings.TrimSpace(input.Color))
	if message := validateTag(input.Name, input.Color); message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_TAG", message)
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE tags SET name=?,color=?,updated_at=? WHERE id=?`, input.Name, input.Color, nowUTC(), r.PathValue("id"))
	if isUniqueConstraint(err) {
		writeError(w, r, http.StatusConflict, "TAG_NAME_EXISTS", "标签名称已存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新标签失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "TAG_NOT_FOUND", "标签不存在")
		return
	}
	tags, err := queryTags(r.Context(), a.db, r.PathValue("id"))
	if err != nil || len(tags) != 1 {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取标签失败")
		return
	}
	writeData(w, r, http.StatusOK, tags[0])
}

func (a *App) deleteTag(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.ExecContext(r.Context(), `DELETE FROM tags WHERE id=?`, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "删除标签失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "TAG_NOT_FOUND", "标签不存在")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

func (a *App) getImageTags(w http.ResponseWriter, r *http.Request) {
	if !recordExists(r.Context(), a.db, `SELECT 1 FROM images WHERE id=? AND deleted_at IS NULL`, r.PathValue("id")) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT t.id,t.name,t.color,t.created_at,t.updated_at
		FROM tags t JOIN image_tags it ON it.tag_id=t.id
		WHERE it.image_id=? ORDER BY t.name COLLATE NOCASE,t.id`, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片标签失败")
		return
	}
	defer rows.Close()
	items := make([]Tag, 0)
	for rows.Next() {
		var item Tag
		if err := rows.Scan(&item.ID, &item.Name, &item.Color, &item.CreatedAt, &item.UpdatedAt); err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取图片标签失败")
			return
		}
		items = append(items, item)
	}
	writeData(w, r, http.StatusOK, items)
}

func (a *App) putImageTags(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TagIDs []string `json:"tag_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	tagIDs, message := cleanIDs(input.TagIDs, maxOrganizationBatch)
	if message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_TAG_IDS", message)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新图片标签失败")
		return
	}
	defer tx.Rollback()
	if !recordExists(r.Context(), tx, `SELECT 1 FROM images WHERE id=? AND deleted_at IS NULL`, r.PathValue("id")) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在")
		return
	}
	if ok, err := allIDsExist(r.Context(), tx, "tags", tagIDs); err != nil || !ok {
		writeError(w, r, http.StatusBadRequest, "TAG_NOT_FOUND", "包含不存在的标签")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM image_tags WHERE image_id=?`, r.PathValue("id")); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新图片标签失败")
		return
	}
	for _, tagID := range tagIDs {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO image_tags(image_id,tag_id) VALUES(?,?)`, r.PathValue("id"), tagID); err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新图片标签失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新图片标签失败")
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"tag_ids": tagIDs})
}

func (a *App) bulkImageTags(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
		TagIDs []string `json:"tag_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Action != "add" && input.Action != "remove" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ACTION", "action 必须是 add 或 remove")
		return
	}
	imageIDs, imageMessage := cleanRequiredIDs(input.IDs, maxOrganizationBatch)
	tagIDs, tagMessage := cleanRequiredIDs(input.TagIDs, maxOrganizationBatch)
	if imageMessage != "" || tagMessage != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_IDS", firstNonEmpty(imageMessage, tagMessage))
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "批量更新标签失败")
		return
	}
	defer tx.Rollback()
	if ok, err := allImageIDsExist(r.Context(), tx, imageIDs); err != nil || !ok {
		writeError(w, r, http.StatusBadRequest, "IMAGE_NOT_FOUND", "包含不存在或已删除的图片")
		return
	}
	if ok, err := allIDsExist(r.Context(), tx, "tags", tagIDs); err != nil || !ok {
		writeError(w, r, http.StatusBadRequest, "TAG_NOT_FOUND", "包含不存在的标签")
		return
	}
	affected := int64(0)
	for _, imageID := range imageIDs {
		for _, tagID := range tagIDs {
			var result sql.Result
			if input.Action == "add" {
				result, err = tx.ExecContext(r.Context(), `INSERT INTO image_tags(image_id,tag_id) VALUES(?,?) ON CONFLICT DO NOTHING`, imageID, tagID)
			} else {
				result, err = tx.ExecContext(r.Context(), `DELETE FROM image_tags WHERE image_id=? AND tag_id=?`, imageID, tagID)
			}
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "批量更新标签失败")
				return
			}
			count, _ := result.RowsAffected()
			affected += count
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "批量更新标签失败")
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{
		"images": len(imageIDs), "tags": len(tagIDs), "affected": affected,
	})
}

func (a *App) listAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := queryAlbums(r.Context(), a.db, "")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册失败")
		return
	}
	writeData(w, r, http.StatusOK, albums)
}

func (a *App) createAlbum(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeAlbumInput(w, r)
	if !ok {
		return
	}
	if message := validateAlbumInput(input.Name, input.Description); message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ALBUM", message)
		return
	}
	if input.CoverImageID != "" && !recordExists(r.Context(), a.db, `SELECT 1 FROM images WHERE id=? AND deleted_at IS NULL`, input.CoverImageID) {
		writeError(w, r, http.StatusBadRequest, "COVER_IMAGE_NOT_FOUND", "封面图片不存在")
		return
	}
	id, now := newUUIDv7(), nowUTC()
	var cover any
	if input.CoverImageID != "" {
		cover = input.CoverImageID
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO albums(id,name,description,cover_image_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		id, input.Name, input.Description, cover, now, now)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "创建相册失败")
		return
	}
	albums, err := queryAlbums(r.Context(), a.db, id)
	if err != nil || len(albums) != 1 {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册失败")
		return
	}
	writeData(w, r, http.StatusCreated, albums[0])
}

func (a *App) getAlbum(w http.ResponseWriter, r *http.Request) {
	albums, err := queryAlbums(r.Context(), a.db, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册失败")
		return
	}
	if len(albums) == 0 {
		writeError(w, r, http.StatusNotFound, "ALBUM_NOT_FOUND", "相册不存在")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+imageColumns+`
		FROM album_images ai JOIN images i ON i.id=ai.image_id
		WHERE ai.album_id=? AND i.deleted_at IS NULL
		ORDER BY ai.position,ai.added_at,i.id`, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册图片失败")
		return
	}
	defer rows.Close()
	images := make([]Image, 0)
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册图片失败")
			return
		}
		images = append(images, img)
	}
	writeData(w, r, http.StatusOK, map[string]any{"album": albums[0], "images": images})
}

type albumInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	CoverImageID string `json:"cover_image_id"`
}

func decodeAlbumInput(w http.ResponseWriter, r *http.Request) (albumInput, bool) {
	var input albumInput
	if !decodeJSON(w, r, &input) {
		return input, false
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.CoverImageID = strings.TrimSpace(input.CoverImageID)
	return input, true
}

func (a *App) updateAlbum(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeAlbumInput(w, r)
	if !ok {
		return
	}
	if message := validateAlbumInput(input.Name, input.Description); message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ALBUM", message)
		return
	}
	if input.CoverImageID != "" && !recordExists(r.Context(), a.db, `SELECT 1 FROM album_images ai JOIN images i ON i.id=ai.image_id WHERE ai.album_id=? AND ai.image_id=? AND i.deleted_at IS NULL`, r.PathValue("id"), input.CoverImageID) {
		writeError(w, r, http.StatusBadRequest, "COVER_IMAGE_NOT_IN_ALBUM", "封面图片必须属于当前相册")
		return
	}
	var cover any
	if input.CoverImageID != "" {
		cover = input.CoverImageID
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE albums SET name=?,description=?,cover_image_id=?,updated_at=? WHERE id=?`,
		input.Name, input.Description, cover, nowUTC(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "更新相册失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "ALBUM_NOT_FOUND", "相册不存在")
		return
	}
	albums, err := queryAlbums(r.Context(), a.db, r.PathValue("id"))
	if err != nil || len(albums) != 1 {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "读取相册失败")
		return
	}
	writeData(w, r, http.StatusOK, albums[0])
}

func (a *App) deleteAlbum(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.ExecContext(r.Context(), `DELETE FROM albums WHERE id=?`, r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "删除相册失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "ALBUM_NOT_FOUND", "相册不存在")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

func (a *App) addAlbumImages(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	ids, message := cleanRequiredIDs(input.IDs, maxOrganizationBatch)
	if message != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_IDS", message)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "添加相册图片失败")
		return
	}
	defer tx.Rollback()
	if !recordExists(r.Context(), tx, `SELECT 1 FROM albums WHERE id=?`, r.PathValue("id")) {
		writeError(w, r, http.StatusNotFound, "ALBUM_NOT_FOUND", "相册不存在")
		return
	}
	if ok, err := allImageIDsExist(r.Context(), tx, ids); err != nil || !ok {
		writeError(w, r, http.StatusBadRequest, "IMAGE_NOT_FOUND", "包含不存在或已删除的图片")
		return
	}
	var nextPosition int
	if err := tx.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(position),-1)+1 FROM album_images WHERE album_id=?`, r.PathValue("id")).Scan(&nextPosition); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "添加相册图片失败")
		return
	}
	var added int64
	for _, imageID := range ids {
		result, err := tx.ExecContext(r.Context(), `INSERT INTO album_images(album_id,image_id,position,added_at)
			VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, r.PathValue("id"), imageID, nextPosition, nowUTC())
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "添加相册图片失败")
			return
		}
		count, _ := result.RowsAffected()
		if count > 0 {
			added += count
			nextPosition++
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "添加相册图片失败")
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"requested": len(ids), "added": added})
}

func (a *App) removeAlbumImage(w http.ResponseWriter, r *http.Request) {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "移除相册图片失败")
		return
	}
	defer tx.Rollback()
	if !recordExists(r.Context(), tx, `SELECT 1 FROM albums WHERE id=?`, r.PathValue("id")) {
		writeError(w, r, http.StatusNotFound, "ALBUM_NOT_FOUND", "相册不存在")
		return
	}
	result, err := tx.ExecContext(r.Context(), `DELETE FROM album_images WHERE album_id=? AND image_id=?`, r.PathValue("id"), r.PathValue("image_id"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "移除相册图片失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, http.StatusNotFound, "ALBUM_IMAGE_NOT_FOUND", "图片不在该相册中")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE albums SET cover_image_id=NULL,updated_at=?
		WHERE id=? AND cover_image_id=?`, nowUTC(), r.PathValue("id"), r.PathValue("image_id")); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "移除相册图片失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "移除相册图片失败")
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"removed": true})
}

func validateTag(name, color string) string {
	if name == "" || len([]rune(name)) > 50 {
		return "标签名称不能为空且最多 50 个字符"
	}
	if !tagColorPattern.MatchString(color) {
		return "标签颜色必须是 #RRGGBB 格式"
	}
	return ""
}

func validateAlbumInput(name, description string) string {
	if name == "" || len([]rune(name)) > 100 {
		return "相册名称不能为空且最多 100 个字符"
	}
	if len([]rune(description)) > 1000 {
		return "相册描述最多 1000 个字符"
	}
	return ""
}

func queryTags(ctx context.Context, db queryContext, id string) ([]Tag, error) {
	query := `SELECT t.id,t.name,t.color,t.created_at,t.updated_at,
		(SELECT COUNT(*) FROM image_tags it JOIN images i ON i.id=it.image_id
		 WHERE it.tag_id=t.id AND i.deleted_at IS NULL)
		FROM tags t`
	args := []any{}
	if id != "" {
		query += " WHERE t.id=?"
		args = append(args, id)
	}
	query += " ORDER BY t.name COLLATE NOCASE,t.id"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Tag, 0)
	for rows.Next() {
		var item Tag
		if err := rows.Scan(&item.ID, &item.Name, &item.Color, &item.CreatedAt, &item.UpdatedAt, &item.ImageCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func queryAlbums(ctx context.Context, db queryContext, id string) ([]Album, error) {
	query := `SELECT a.id,a.name,a.description,COALESCE(a.cover_image_id,''),
		COALESCE((SELECT COALESCE(v.public_url,i.public_url) FROM images i
		 LEFT JOIN image_variants v ON v.image_id=i.id AND v.kind='thumbnail'
		 WHERE i.id=a.cover_image_id AND i.deleted_at IS NULL),''),
		(SELECT COUNT(*) FROM album_images ai JOIN images i ON i.id=ai.image_id
		 WHERE ai.album_id=a.id AND i.deleted_at IS NULL),
		a.created_at,a.updated_at
		FROM albums a`
	args := []any{}
	if id != "" {
		query += " WHERE a.id=?"
		args = append(args, id)
	}
	query += " ORDER BY a.updated_at DESC,a.id DESC"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Album, 0)
	for rows.Next() {
		var item Album
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.CoverImageID, &item.CoverURL, &item.ImageCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type queryContext interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type existenceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func recordExists(ctx context.Context, db existenceQueryer, query string, args ...any) bool {
	var one int
	return db.QueryRowContext(ctx, query, args...).Scan(&one) == nil
}

func allImageIDsExist(ctx context.Context, tx *sql.Tx, ids []string) (bool, error) {
	for _, id := range ids {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM images WHERE id=? AND deleted_at IS NULL`, id).Scan(&one)
		if isNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func allIDsExist(ctx context.Context, tx *sql.Tx, table string, ids []string) (bool, error) {
	if table != "tags" {
		return false, errors.New("不支持的数据表")
	}
	for _, id := range ids {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id=?`, id).Scan(&one)
		if isNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func cleanRequiredIDs(values []string, maximum int) ([]string, string) {
	result, message := cleanIDs(values, maximum)
	if message != "" {
		return nil, message
	}
	if len(result) == 0 {
		return nil, "ID 数组不能为空"
	}
	return result, ""
}

func cleanIDs(values []string, maximum int) ([]string, string) {
	if len(values) > maximum {
		return nil, "单次最多处理 100 个 ID"
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, "ID 不能为空"
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

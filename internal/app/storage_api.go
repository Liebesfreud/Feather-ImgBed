package app

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

var storageIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

var secretFields = map[string][]string{
	"s3":       {"access_key", "secret_key"},
	"webdav":   {"password"},
	"telegram": {"bot_token"},
}

func maskedConfig(storageType string, config map[string]any) map[string]any {
	result := make(map[string]any, len(config))
	for key, value := range config {
		result[key] = value
	}
	for _, key := range secretFields[storageType] {
		if value := stringValue(config, key); value != "" {
			result[key] = ""
			result[key+"_configured"] = true
		} else {
			delete(result, key)
		}
	}
	return result
}

func mergeSecrets(storageType string, incoming, existing map[string]any) {
	for _, key := range secretFields[storageType] {
		if stringValue(incoming, key) == "" {
			if old, ok := existing[key]; ok {
				incoming[key] = old
			}
		}
	}
}

func validateStorage(record StorageRecord) string {
	if !storageIDPattern.MatchString(record.ID) {
		return "存储 ID 只能包含字母、数字、下划线和短横线，最多 64 个字符"
	}
	if strings.TrimSpace(record.Name) == "" {
		return "存储名称不能为空"
	}
	switch record.Type {
	case "local":
	case "s3":
		if stringValue(record.Config, "endpoint") == "" || stringValue(record.Config, "bucket") == "" || stringValue(record.Config, "access_key") == "" || stringValue(record.Config, "secret_key") == "" {
			return "S3 Endpoint、Bucket、Access Key 和 Secret Key 不能为空"
		}
		if !isCloudflareR2Endpoint(record.Config) && stringValue(record.Config, "public_url") == "" {
			return "非 Cloudflare R2 的 S3 存储必须填写访问域名"
		}
	case "webdav":
		if stringValue(record.Config, "url") == "" || stringValue(record.Config, "username") == "" || stringValue(record.Config, "password") == "" || stringValue(record.Config, "public_url") == "" {
			return "WebDAV 地址、用户名、密码和访问域名不能为空"
		}
	case "telegram":
		if stringValue(record.Config, "bot_token") == "" || stringValue(record.Config, "chat_id") == "" {
			return "Telegram Bot Token 和 Chat ID 不能为空"
		}
	default:
		return "存储类型必须是 local、s3、webdav 或 telegram"
	}
	return ""
}

func (a *App) listStorages(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), "SELECT id,name,type,enabled,config,created_at,updated_at FROM storages ORDER BY created_at")
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取存储配置失败")
		return
	}
	defer rows.Close()
	items := make([]StorageRecord, 0)
	for rows.Next() {
		var item StorageRecord
		var enabled int
		var encrypted string
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &enabled, &encrypted, &item.CreatedAt, &item.UpdatedAt); err != nil {
			writeError(w, r, 500, "DATABASE_ERROR", "读取存储配置失败")
			return
		}
		item.Enabled = enabled == 1
		item.Config = map[string]any{}
		if decryptJSON(a.masterKey, encrypted, &item.Config) != nil {
			a.logger.Error("存储配置解密失败", "storage_id", item.ID)
			continue
		}
		item.Config = maskedConfig(item.Type, item.Config)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取存储配置失败")
		return
	}
	writeData(w, r, 200, items)
}

func (a *App) testStorage(w http.ResponseWriter, r *http.Request) {
	var input StorageRecord
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ID = "test"
	input.Name = "连接测试"
	if input.Config == nil {
		input.Config = map[string]any{}
	}
	if existingID := r.URL.Query().Get("storage_id"); existingID != "" {
		if existing, err := a.storageRecord(r.Context(), existingID); err == nil {
			mergeSecrets(input.Type, input.Config, existing.Config)
		}
	}
	if message := validateStorage(input); message != "" {
		writeError(w, r, 400, "INVALID_STORAGE", message)
		return
	}
	backend, err := a.backend(input)
	if err != nil {
		writeError(w, r, 400, "INVALID_STORAGE", err.Error())
		return
	}
	ctx, cancel := contextWithTimeout(r, 10*time.Second)
	defer cancel()
	if err := backend.Test(ctx); err != nil {
		a.logger.Warn("存储连接测试失败", "request_id", requestID(r), "type", input.Type, "error", err)
		writeError(w, r, 400, "STORAGE_TEST_FAILED", "连接测试失败，请检查地址、凭据和权限")
		return
	}
	writeData(w, r, 200, map[string]bool{"connected": true})
}

func (a *App) putStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input StorageRecord
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ID = id
	input.Name = strings.TrimSpace(input.Name)
	if input.Config == nil {
		input.Config = map[string]any{}
	}
	existing, err := a.storageRecord(r.Context(), id)
	exists := err == nil
	if err != nil && !isNotFound(err) {
		writeError(w, r, 500, "DATABASE_ERROR", "读取存储配置失败")
		return
	}
	if exists {
		if input.Type == "" {
			input.Type = existing.Type
		}
		if input.Type != existing.Type {
			writeError(w, r, 400, "TYPE_IMMUTABLE", "已有存储不能修改类型")
			return
		}
		mergeSecrets(input.Type, input.Config, existing.Config)
	}
	if message := validateStorage(input); message != "" {
		writeError(w, r, 400, "INVALID_STORAGE", message)
		return
	}
	settings, err := loadSettings(r.Context(), a.db)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取系统设置失败")
		return
	}
	if exists && id == settings.DefaultStorageID && !input.Enabled {
		writeError(w, r, 409, "DEFAULT_STORAGE_REQUIRED", "请先指定新的默认存储，再停用当前存储")
		return
	}
	encrypted, err := encryptJSON(a.masterKey, input.Config)
	if err != nil {
		writeError(w, r, 500, "ENCRYPTION_ERROR", "保存存储配置失败")
		return
	}
	now := nowUTC()
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,config=excluded.config,updated_at=excluded.updated_at`, id, input.Name, input.Type, input.Enabled, encrypted, now, now)
	}
	if err == nil && exists {
		urlPrefix := publicURL(input, "")
		_, err = tx.ExecContext(r.Context(), `UPDATE images
			SET public_url=? || ltrim(object_key, '/')
			WHERE storage_id=?`, urlPrefix, id)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE image_variants
				SET public_url=? || ltrim(object_key, '/')
				WHERE image_id IN (SELECT id FROM images WHERE storage_id=?)`, urlPrefix, id)
		}
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "保存存储配置失败")
		return
	}
	saved, err := a.storageRecord(r.Context(), id)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取存储配置失败")
		return
	}
	saved.Config = maskedConfig(saved.Type, saved.Config)
	status := 200
	if !exists {
		status = 201
	}
	writeData(w, r, status, saved)
}

func (a *App) deleteStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	settings, err := loadSettings(r.Context(), a.db)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取系统设置失败")
		return
	}
	if id == settings.DefaultStorageID {
		writeError(w, r, 409, "DEFAULT_STORAGE_REQUIRED", "请先指定新的默认存储，再删除当前存储")
		return
	}
	var count int
	if err := a.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM images WHERE storage_id=?", id).Scan(&count); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "检查存储使用状态失败")
		return
	}
	if count > 0 {
		writeError(w, r, 409, "STORAGE_IN_USE", "该存储仍有关联图片，不能删除")
		return
	}
	result, err := a.db.ExecContext(r.Context(), "DELETE FROM storages WHERE id=?", id)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "删除存储配置失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, r, 404, "STORAGE_NOT_FOUND", "存储配置不存在")
		return
	}
	writeData(w, r, 200, map[string]bool{"deleted": true})
}

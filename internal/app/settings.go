package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func loadSettings(ctx context.Context, db queryer) (Settings, error) {
	settings := defaultSettings()
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM configs WHERE key='settings'").Scan(&value)
	if isNotFound(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal([]byte(value), &settings)
	return settings, err
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func saveSettingsTx(ctx context.Context, tx *sql.Tx, settings Settings) error {
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO configs(key,value,encrypted,updated_at) VALUES('settings',?,0,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, string(data), nowUTC())
	return err
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings(r.Context(), a.db)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取设置失败")
		return
	}
	writeData(w, r, 200, settings)
}

func validateSettings(settings Settings) string {
	if settings.SiteName == "" || len(settings.SiteName) > 100 {
		return "站点名称不能为空且最多 100 个字符"
	}
	if settings.SiteURL != "" {
		parsed, err := url.Parse(settings.SiteURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "站点访问地址必须是有效的 HTTP 或 HTTPS 地址"
		}
	}
	if settings.MaxFileSize < 1024 || settings.MaxFileSize > 1024*1024*1024 {
		return "单文件上限必须在 1 KB 到 1 GB 之间"
	}
	if settings.MaxBatchCount < 1 || settings.MaxBatchCount > 100 {
		return "单批文件数量必须在 1 到 100 之间"
	}
	allowed := map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true}
	if len(settings.AllowedTypes) == 0 {
		return "至少需要允许一种图片格式"
	}
	for _, value := range settings.AllowedTypes {
		if !allowed[value] {
			return "包含不支持的图片格式"
		}
		if value == "image/avif" {
			return "当前构建未启用 AVIF 解码能力"
		}
	}
	if settings.NamingRule != "random" && settings.NamingRule != "date" && settings.NamingRule != "original" {
		return "图片命名规则必须是 random、date 或 original"
	}
	if settings.Processing.WebPQuality < 1 || settings.Processing.WebPQuality > 100 {
		return "WebP 质量必须在 1 到 100 之间"
	}
	if len([]rune(settings.Processing.WatermarkText)) > 200 {
		return "水印文字最多 200 个字符"
	}
	if settings.Processing.WatermarkEnabled && strings.TrimSpace(settings.Processing.WatermarkText) == "" {
		return "启用水印时必须填写水印文字"
	}
	switch settings.Processing.WatermarkPosition {
	case "top-left", "top-right", "bottom-left", "bottom-right", "center":
	default:
		return "水印位置必须是 top-left、top-right、bottom-left、bottom-right 或 center"
	}
	return ""
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if !decodeJSON(w, r, &settings) {
		return
	}
	settings.SiteName = strings.TrimSpace(settings.SiteName)
	settings.SiteURL = strings.TrimRight(strings.TrimSpace(settings.SiteURL), "/")
	settings.Processing.WatermarkText = strings.TrimSpace(settings.Processing.WatermarkText)
	if message := validateSettings(settings); message != "" {
		writeError(w, r, 400, "INVALID_SETTINGS", message)
		return
	}
	var enabled int
	if err := a.db.QueryRowContext(r.Context(), "SELECT enabled FROM storages WHERE id=?", settings.DefaultStorageID).Scan(&enabled); err != nil || enabled == 0 {
		writeError(w, r, 400, "INVALID_DEFAULT_STORAGE", "默认存储不存在或未启用")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "保存设置失败")
		return
	}
	defer tx.Rollback()
	if err := saveSettingsTx(r.Context(), tx, settings); err != nil || tx.Commit() != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "保存设置失败")
		return
	}
	writeData(w, r, 200, settings)
}

func (a *App) systemInfo(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := a.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM storages WHERE enabled=1").Scan(&count); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取系统信息失败")
		return
	}
	writeData(w, r, 200, map[string]any{"version": a.cfg.Version, "database": "ok", "enabled_storages": count})
}

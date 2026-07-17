package app

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type App struct {
	cfg            Config
	db             *sql.DB
	masterKey      []byte
	logger         *slog.Logger
	mux            *http.ServeMux
	limiter        *rateLimiter
	trustedProxies []netip.Prefix
	backendFactory backendFactory
}

func New(cfg Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	if cfg.MasterKeyFile == "" {
		cfg.MasterKeyFile = filepath.Join(cfg.DataDir, "master.key")
	}
	key, err := loadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "feather.db"))
	if err != nil {
		return nil, err
	}
	trustedProxies, err := parseTrustedProxies(cfg.TrustedProxyCIDRs)
	if err != nil {
		db.Close()
		return nil, err
	}
	application := &App{
		cfg: cfg, db: db, masterKey: key, logger: logger,
		mux: http.NewServeMux(), limiter: newRateLimiter(), trustedProxies: trustedProxies,
	}
	application.backendFactory = application.defaultBackend
	application.routes()
	return application, nil
}

func (a *App) Close() error { return a.db.Close() }

func (a *App) Handler() http.Handler {
	return a.recoverPanic(a.requestID(a.securityHeaders(a.logRequest(a.mux))))
}

func (a *App) routes() {
	a.mux.HandleFunc("GET /healthz", a.health)
	a.mux.HandleFunc("GET /readyz", a.ready)
	a.mux.HandleFunc("GET /api/v1/auth/status", a.authStatus)
	a.mux.HandleFunc("POST /api/v1/auth/initialize", a.limit("initialize", 5, time.Minute, a.initialize))
	a.mux.HandleFunc("POST /api/v1/auth/login", a.limit("login", 10, time.Minute, a.login))
	a.mux.Handle("POST /api/v1/auth/logout", a.requireAuth(http.HandlerFunc(a.logout)))
	a.mux.Handle("PUT /api/v1/auth/password", a.requireAuth(http.HandlerFunc(a.changePassword)))
	a.mux.Handle("GET /api/v1/tokens", a.requireAuth(http.HandlerFunc(a.listTokens)))
	a.mux.Handle("POST /api/v1/tokens", a.requireAuth(http.HandlerFunc(a.createToken)))
	a.mux.Handle("DELETE /api/v1/tokens/{id}", a.requireAuth(http.HandlerFunc(a.deleteToken)))

	a.mux.Handle("GET /api/v1/images", a.requireAuth(http.HandlerFunc(a.listImages)))
	a.mux.Handle("POST /api/v1/images", a.requireAuth(a.limitHandler("upload", 60, time.Minute, http.HandlerFunc(a.uploadImages))))
	a.mux.Handle("POST /api/v1/upload", a.requireAuth(a.limitHandler("upload", 60, time.Minute, http.HandlerFunc(a.uploadImages))))
	a.mux.HandleFunc("GET /api/v1/random", a.randomImage)
	a.mux.Handle("GET /api/v1/images/{id}", a.requireAuth(http.HandlerFunc(a.getImage)))
	a.mux.Handle("DELETE /api/v1/images/{id}", a.requireAuth(http.HandlerFunc(a.deleteImage)))
	a.mux.Handle("POST /api/v1/images/bulk", a.requireAuth(http.HandlerFunc(a.bulkImages)))
	a.mux.Handle("GET /api/v1/trash", a.requireAuth(http.HandlerFunc(a.listTrash)))
	a.mux.Handle("GET /api/v1/trash/{id}/file/{kind}", a.requireAuth(http.HandlerFunc(a.serveTrashFile)))
	a.mux.Handle("POST /api/v1/trash/{id}/restore", a.requireAuth(http.HandlerFunc(a.restoreImage)))
	a.mux.Handle("DELETE /api/v1/trash/{id}", a.requireAuth(http.HandlerFunc(a.purgeImage)))
	a.mux.Handle("POST /api/v1/trash/purge", a.requireAuth(http.HandlerFunc(a.purgeTrash)))

	a.mux.Handle("GET /api/v1/storages", a.requireAuth(http.HandlerFunc(a.listStorages)))
	a.mux.Handle("POST /api/v1/storages/test", a.requireAuth(http.HandlerFunc(a.testStorage)))
	a.mux.Handle("PUT /api/v1/storages/{id}", a.requireAuth(http.HandlerFunc(a.putStorage)))
	a.mux.Handle("DELETE /api/v1/storages/{id}", a.requireAuth(http.HandlerFunc(a.deleteStorage)))
	a.mux.Handle("GET /api/v1/settings", a.requireAuth(http.HandlerFunc(a.getSettings)))
	a.mux.Handle("PUT /api/v1/settings", a.requireAuth(http.HandlerFunc(a.putSettings)))
	a.mux.Handle("GET /api/v1/system", a.requireAuth(http.HandlerFunc(a.systemInfo)))

	a.registerOrganizationRoutes()
	a.registerImportRoutes()

	a.mux.HandleFunc("GET /files/", a.serveLocalFile)
	a.mux.HandleFunc("GET /s3-files/", a.serveS3File)
	a.mux.HandleFunc("GET /tg-files/", a.serveTelegramFile)
	a.mux.HandleFunc("GET /", a.serveFrontend)
}

func (a *App) serveLocalFile(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/files/")
	var storageID string
	if err := a.db.QueryRowContext(r.Context(), `SELECT i.storage_id
		FROM images i
		WHERE i.storage_type='local' AND i.deleted_at IS NULL
			AND (i.object_key=? OR EXISTS (
				SELECT 1 FROM image_variants v WHERE v.image_id=i.id AND v.object_key=?
			))
		ORDER BY i.created_at DESC LIMIT 1`, key, key).Scan(&storageID); err != nil {
		http.NotFound(w, r)
		return
	}
	record, err := a.storageRecord(r.Context(), storageID)
	if err != nil || record.Type != "local" {
		http.NotFound(w, r)
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	storage, ok := backend.(*localStorage)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, err := storage.safe(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, target)
}

func (a *App) serveS3File(w http.ResponseWriter, r *http.Request) {
	pathValue := strings.TrimPrefix(r.URL.Path, "/s3-files/")
	storageID, key, ok := strings.Cut(pathValue, "/")
	if !ok || storageID == "" || key == "" {
		http.NotFound(w, r)
		return
	}
	var exists int
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM images i
		WHERE i.storage_type='s3' AND i.storage_id=? AND i.deleted_at IS NULL
			AND (i.object_key=? OR EXISTS (
				SELECT 1 FROM image_variants v WHERE v.image_id=i.id AND v.object_key=?
			))
	)`, storageID, key, key).Scan(&exists); err != nil || exists != 1 {
		http.NotFound(w, r)
		return
	}
	record, err := a.storageRecord(r.Context(), storageID)
	if err != nil || record.Type != "s3" || !record.Enabled || !isCloudflareR2Endpoint(record.Config) {
		http.NotFound(w, r)
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		http.Error(w, "S3 存储暂不可用", http.StatusBadGateway)
		return
	}
	reader, err := backend.Open(r.Context(), key)
	if err != nil {
		a.logger.Warn("S3 文件回读失败", "storage_id", storageID, "object_key", key, "error", err)
		http.Error(w, "S3 文件暂不可用", http.StatusBadGateway)
		return
	}
	defer reader.Close()

	buffered := bufio.NewReader(reader)
	sample, _ := buffered.Peek(512)
	if len(sample) > 0 {
		w.Header().Set("Content-Type", http.DetectContentType(sample))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, buffered); err != nil {
		a.logger.Warn("S3 文件响应中断", "storage_id", storageID, "object_key", key, "error", err)
	}
}

func (a *App) serveTelegramFile(w http.ResponseWriter, r *http.Request) {
	pathValue := strings.TrimPrefix(r.URL.Path, "/tg-files/")
	storageID, key, ok := strings.Cut(pathValue, "/")
	if !ok || storageID == "" || key == "" {
		http.NotFound(w, r)
		return
	}
	var exists int
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM images i
		WHERE i.storage_type='telegram' AND i.storage_id=? AND i.deleted_at IS NULL
			AND (i.object_key=? OR EXISTS (
				SELECT 1 FROM image_variants v WHERE v.image_id=i.id AND v.object_key=?
			))
	)`, storageID, key, key).Scan(&exists); err != nil || exists != 1 {
		http.NotFound(w, r)
		return
	}
	record, err := a.storageRecord(r.Context(), storageID)
	if err != nil || record.Type != "telegram" {
		http.NotFound(w, r)
		return
	}
	backend, err := a.backend(record)
	if err != nil {
		http.Error(w, "Telegram 存储暂不可用", http.StatusBadGateway)
		return
	}
	reader, err := backend.Open(r.Context(), key)
	if err != nil {
		a.logger.Warn("Telegram 文件回读失败", "storage_id", storageID, "error", err)
		http.Error(w, "Telegram 文件暂不可用", http.StatusBadGateway)
		return
	}
	defer reader.Close()

	buffered := bufio.NewReader(reader)
	sample, _ := buffered.Peek(512)
	if len(sample) > 0 {
		w.Header().Set("Content-Type", http.DetectContentType(sample))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, buffered); err != nil {
		a.logger.Warn("Telegram 文件响应中断", "storage_id", storageID, "error", err)
	}
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	writeData(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := a.db.PingContext(ctx); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "数据库暂不可用")
		return
	}
	writeData(w, r, http.StatusOK, map[string]string{"status": "ready"})
}

type rateEntry struct {
	count int
	reset time.Time
}

type rateLimiter struct {
	mu          sync.Mutex
	entries     map[string]rateEntry
	lastCleanup time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{entries: make(map[string]rateEntry)} }

func (l *rateLimiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= time.Minute {
		for candidate, entry := range l.entries {
			if !now.Before(entry.reset) {
				delete(l.entries, candidate)
			}
		}
		l.lastCleanup = now
	}
	entry := l.entries[key]
	if now.After(entry.reset) {
		entry = rateEntry{reset: now.Add(window)}
	}
	if entry.count >= max {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

func isNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("可信代理网段 %q 无效: %w", value, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

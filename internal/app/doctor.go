package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type DoctorStatus string

const (
	DoctorOK      DoctorStatus = "ok"
	DoctorWarning DoctorStatus = "warning"
	DoctorError   DoctorStatus = "error"
)

type DoctorCheck struct {
	Name    string       `json:"name"`
	Status  DoctorStatus `json:"status"`
	Message string       `json:"message"`
}

type DoctorReport struct {
	Status             DoctorStatus  `json:"status"`
	ApplicationVersion string        `json:"application_version"`
	DatabaseVersion    int           `json:"database_version"`
	Checks             []DoctorCheck `json:"checks"`
}

func (r DoctorReport) ExitCode() int {
	switch r.Status {
	case DoctorError:
		return 2
	case DoctorWarning:
		return 1
	default:
		return 0
	}
}

func RunDoctor(ctx context.Context, cfg Config, network bool) DoctorReport {
	report := DoctorReport{
		Status: DoctorOK, ApplicationVersion: cfg.Version, Checks: make([]DoctorCheck, 0, 12),
	}
	add := func(name string, status DoctorStatus, message string) {
		report.Checks = append(report.Checks, DoctorCheck{Name: name, Status: status, Message: message})
		if doctorSeverity(status) > doctorSeverity(report.Status) {
			report.Status = status
		}
	}

	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		add("data_directory", DoctorError, "数据目录路径无效: "+err.Error())
		return report
	}
	if info, statErr := os.Stat(dataDir); statErr != nil {
		add("data_directory", DoctorError, "数据目录不可访问: "+statErr.Error())
		return report
	} else if !info.IsDir() {
		add("data_directory", DoctorError, "数据目录路径不是目录")
		return report
	}
	probe, probeErr := os.CreateTemp(dataDir, ".doctor-*")
	if probeErr != nil {
		add("data_directory", DoctorError, "数据目录不可写: "+probeErr.Error())
	} else {
		probeName := probe.Name()
		_ = probe.Close()
		_ = os.Remove(probeName)
		add("data_directory", DoctorOK, "数据目录存在且可写")
	}

	keyPath := cfg.MasterKeyFile
	if keyPath == "" {
		keyPath = filepath.Join(dataDir, "master.key")
	}
	key, keyStatus, keyMessage := inspectMasterKey(keyPath)
	add("master_key", keyStatus, keyMessage)

	dbPath := filepath.Join(dataDir, "feather.db")
	db, dbErr := openReadOnlyDB(dbPath)
	if dbErr != nil {
		add("database", DoctorError, "数据库无法只读打开: "+dbErr.Error())
		addDiskCheck(dataDir, add)
		return report
	}
	defer db.Close()
	var quick string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quick); err != nil {
		add("database_quick_check", DoctorError, "SQLite 快速检查失败: "+err.Error())
	} else if quick != "ok" {
		add("database_quick_check", DoctorError, "SQLite 快速检查返回: "+quick)
	} else {
		add("database_quick_check", DoctorOK, "SQLite 快速检查通过")
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&report.DatabaseVersion); err != nil {
		add("database_version", DoctorError, "读取数据库版本失败: "+err.Error())
	} else if report.DatabaseVersion > schemaVersion {
		add("database_version", DoctorError, fmt.Sprintf("数据库版本 %d 高于当前支持版本 %d", report.DatabaseVersion, schemaVersion))
	} else if report.DatabaseVersion < schemaVersion {
		add("database_version", DoctorWarning, fmt.Sprintf("数据库版本为 %d，当前程序支持 %d；启动服务后将迁移", report.DatabaseVersion, schemaVersion))
	} else {
		add("database_version", DoctorOK, fmt.Sprintf("数据库版本为 %d", report.DatabaseVersion))
	}
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		add("wal_mode", DoctorWarning, "无法读取 SQLite 日志模式: "+err.Error())
	} else if !strings.EqualFold(journalMode, "wal") {
		add("wal_mode", DoctorWarning, "SQLite 当前未使用 WAL 模式: "+journalMode)
	} else {
		add("wal_mode", DoctorOK, "SQLite 正在使用 WAL 模式")
	}

	if keyStatus != DoctorError {
		runStorageDoctorChecks(ctx, cfg, db, key, network, add)
	} else {
		add("storage_configs", DoctorError, "缺少有效主密钥，无法验证存储配置")
	}
	runCookieDoctorCheck(ctx, cfg, db, add)
	addDiskCheck(dataDir, add)
	return report
}

func doctorSeverity(status DoctorStatus) int {
	switch status {
	case DoctorError:
		return 2
	case DoctorWarning:
		return 1
	default:
		return 0
	}
}

func inspectMasterKey(filename string) ([]byte, DoctorStatus, string) {
	if encoded := os.Getenv("FEATHER_MASTER_KEY"); encoded != "" {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, DoctorError, "FEATHER_MASTER_KEY 格式无效"
		}
		return key, DoctorOK, "环境变量主密钥格式正确"
	}
	info, err := os.Stat(filename)
	if err != nil {
		return nil, DoctorError, "主密钥文件不可访问: " + err.Error()
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, DoctorError, "主密钥文件不可读: " + err.Error()
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, DoctorError, "主密钥文件格式无效"
	}
	if info.Mode().Perm()&0077 != 0 {
		return key, DoctorWarning, fmt.Sprintf("主密钥权限 %04o 过宽，建议设为 0600", info.Mode().Perm())
	}
	return key, DoctorOK, "主密钥存在、格式和权限正确"
}

func runStorageDoctorChecks(
	ctx context.Context,
	cfg Config,
	db *sql.DB,
	key []byte,
	network bool,
	add func(string, DoctorStatus, string),
) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,type,enabled,config,created_at,updated_at FROM storages ORDER BY id`)
	if err != nil {
		add("storage_configs", DoctorError, "读取存储配置失败: "+err.Error())
		return
	}
	defer rows.Close()
	var records []StorageRecord
	var decryptFailures int
	for rows.Next() {
		var record StorageRecord
		var enabled int
		var encrypted string
		if err := rows.Scan(&record.ID, &record.Name, &record.Type, &enabled, &encrypted, &record.CreatedAt, &record.UpdatedAt); err != nil {
			decryptFailures++
			continue
		}
		record.Enabled = enabled == 1
		record.Config = make(map[string]any)
		if err := decryptJSON(key, encrypted, &record.Config); err != nil {
			decryptFailures++
			continue
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		add("storage_configs", DoctorError, "遍历存储配置失败: "+err.Error())
		return
	}
	if decryptFailures > 0 {
		add("storage_configs", DoctorError, fmt.Sprintf("%d 个存储配置无法解密", decryptFailures))
	} else {
		add("storage_configs", DoctorOK, fmt.Sprintf("%d 个存储配置可正常解密", len(records)))
	}

	var missingFiles int
	var checkedFiles int
	for _, record := range records {
		if record.Type != "local" {
			continue
		}
		root := stringValue(record.Config, "data_dir")
		if root == "" {
			root = "images"
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(cfg.DataDir, root)
		}
		if err := localDirectoryWritable(root); err != nil {
			add("local_storage_"+record.ID, DoctorWarning, err.Error())
		} else {
			add("local_storage_"+record.ID, DoctorOK, "本地存储目录可写")
		}
		imageRows, err := db.QueryContext(ctx, `SELECT object_key FROM images WHERE storage_id=?`, record.ID)
		if err != nil {
			add("local_files_"+record.ID, DoctorWarning, "无法检查图片文件: "+err.Error())
			continue
		}
		local := &localStorage{root: root}
		for imageRows.Next() {
			var objectKey string
			if imageRows.Scan(&objectKey) != nil {
				continue
			}
			checkedFiles++
			target, err := local.safe(objectKey)
			if err != nil {
				missingFiles++
				continue
			}
			info, err := os.Stat(target)
			if err != nil || !info.Mode().IsRegular() {
				missingFiles++
			}
		}
		_ = imageRows.Close()
	}
	if missingFiles > 0 {
		add("local_image_files", DoctorWarning, fmt.Sprintf("检查 %d 个本地图片记录，发现 %d 个文件缺失或路径无效", checkedFiles, missingFiles))
	} else {
		add("local_image_files", DoctorOK, fmt.Sprintf("%d 个本地图片记录均可对应文件", checkedFiles))
	}

	if !network {
		add("remote_storage_network", DoctorOK, "未请求远程存储网络检查")
		return
	}
	application := &App{cfg: cfg, masterKey: key}
	var failures int
	var tested int
	for _, record := range records {
		if !record.Enabled || record.Type == "local" {
			continue
		}
		tested++
		backend, err := application.defaultBackend(record)
		if err == nil {
			testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = backend.Test(testCtx)
			cancel()
		}
		if err != nil {
			failures++
			add("remote_storage_"+record.ID, DoctorWarning, "连接测试失败: "+err.Error())
		} else {
			add("remote_storage_"+record.ID, DoctorOK, "连接测试通过")
		}
	}
	if tested == 0 {
		add("remote_storage_network", DoctorOK, "没有需要测试的已启用远程存储")
	} else if failures == 0 {
		add("remote_storage_network", DoctorOK, fmt.Sprintf("%d 个远程存储均连接成功", tested))
	} else {
		add("remote_storage_network", DoctorWarning, fmt.Sprintf("%d/%d 个远程存储连接失败", failures, tested))
	}
}

func localDirectoryWritable(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("本地存储目录不可访问: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("本地存储路径不是目录")
	}
	file, err := os.CreateTemp(root, ".doctor-*")
	if err != nil {
		return fmt.Errorf("本地存储目录不可写: %w", err)
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return nil
}

func runCookieDoctorCheck(ctx context.Context, cfg Config, db *sql.DB, add func(string, DoctorStatus, string)) {
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT value FROM configs WHERE key='settings'`).Scan(&raw); err != nil {
		if isNotFound(err) {
			add("cookie_site_url", DoctorOK, "尚未初始化站点设置")
		} else {
			add("cookie_site_url", DoctorWarning, "无法读取站点设置: "+err.Error())
		}
		return
	}
	settings, err := loadSettings(ctx, db)
	if err != nil {
		add("cookie_site_url", DoctorWarning, "站点设置格式无效: "+err.Error())
		return
	}
	parsed, err := url.Parse(settings.SiteURL)
	if err != nil || settings.SiteURL == "" {
		add("cookie_site_url", DoctorWarning, "未配置有效站点 URL，无法核对 Cookie 安全策略")
		return
	}
	if parsed.Scheme == "https" && !cfg.SecureCookie {
		add("cookie_site_url", DoctorWarning, "站点使用 HTTPS，但安全 Cookie 未启用")
		return
	}
	if parsed.Scheme == "http" && cfg.SecureCookie {
		add("cookie_site_url", DoctorWarning, "站点使用 HTTP，但安全 Cookie 已启用，浏览器可能不会发送会话")
		return
	}
	add("cookie_site_url", DoctorOK, "站点 URL 与 Cookie 安全策略匹配")
}

func addDiskCheck(dataDir string, add func(string, DoctorStatus, string)) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dataDir, &stat); err != nil {
		add("disk_space", DoctorWarning, "无法读取剩余磁盘空间: "+err.Error())
		return
	}
	free := stat.Bavail * uint64(stat.Bsize)
	const warnBelow = uint64(1 << 30)
	if free < warnBelow {
		add("disk_space", DoctorWarning, fmt.Sprintf("可用磁盘空间不足 1 GiB：%d 字节", free))
		return
	}
	add("disk_space", DoctorOK, fmt.Sprintf("可用磁盘空间 %d 字节", free))
}

func openReadOnlyDB(filename string) (*sql.DB, error) {
	if _, err := os.Stat(filename); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(filename) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

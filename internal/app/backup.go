package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	_ "modernc.org/sqlite"
)

const (
	maxBackupFiles     = 100_000
	maxBackupFileSize  = int64(2 << 30)
	maxBackupTotalSize = int64(20 << 30)
	maxManifestSize    = int64(8 << 20)
)

type BackupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type BackupManifest struct {
	ApplicationVersion string       `json:"application_version"`
	DatabaseVersion    int          `json:"database_version"`
	CreatedAt          string       `json:"created_at"`
	FileCount          int          `json:"file_count"`
	IncludesMasterKey  bool         `json:"includes_master_key"`
	Files              []BackupFile `json:"files"`
}

type BackupReport struct {
	Path      string         `json:"path"`
	Manifest  BackupManifest `json:"manifest"`
	Encrypted bool           `json:"encrypted"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type BackupOptions struct {
	Passphrase string
}

type BackupVerificationReport struct {
	Path       string         `json:"path"`
	Encrypted  bool           `json:"encrypted"`
	Manifest   BackupManifest `json:"manifest"`
	DatabaseOK bool           `json:"database_ok"`
	LocalFiles int            `json:"local_files"`
}

type backupSource struct {
	ArchivePath string
	SourcePath  string
	Info        os.FileInfo
}

// createDatabaseSnapshot uses SQLite's VACUUM INTO so backups can be created
// while the service is running without copying an inconsistent db/WAL pair.
func createDatabaseSnapshot(ctx context.Context, dataDir string) (string, func(), error) {
	databasePath := filepath.Join(dataDir, "feather.db")
	if _, err := os.Stat(databasePath); err != nil {
		return "", func() {}, fmt.Errorf("数据库文件不可用: %w", err)
	}
	tempDir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return "", func() {}, fmt.Errorf("创建数据库快照临时目录失败: %w", err)
	}
	temp, err := os.CreateTemp(tempDir, "feather-db-snapshot-*.db")
	if err != nil {
		return "", func() {}, err
	}
	snapshot := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(snapshot)
		return "", func() {}, err
	}
	if err := os.Remove(snapshot); err != nil {
		return "", func() {}, err
	}
	db, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if err != nil {
		return "", func() {}, err
	}
	db.SetMaxOpenConns(1)
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(snapshot)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("打开数据库快照源失败: %w", err)
	}
	if _, err := db.ExecContext(pingCtx, "VACUUM INTO ?", snapshot); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("创建数据库一致性快照失败: %w", err)
	}
	if info, err := os.Stat(snapshot); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		cleanup()
		if err == nil {
			err = errors.New("快照文件为空")
		}
		return "", func() {}, fmt.Errorf("数据库快照无效: %w", err)
	}
	// The returned cleanup must remain valid after this function returns, while
	// the database connection itself can be closed immediately.
	_ = db.Close()
	return snapshot, func() { _ = os.Remove(snapshot) }, nil
}

// CreateBackup keeps the legacy unencrypted tar.gz behavior. The database is
// snapshotted consistently; local objects are hashed again while archiving.
func CreateBackup(ctx context.Context, cfg Config, output string) (BackupReport, error) {
	return CreateBackupWithOptions(ctx, cfg, output, BackupOptions{})
}

func CreateBackupWithOptions(ctx context.Context, cfg Config, output string, options BackupOptions) (BackupReport, error) {
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return BackupReport{}, err
	}
	if options.Passphrase != "" && len([]rune(options.Passphrase)) < 12 {
		return BackupReport{}, errors.New("备份加密口令至少需要 12 个字符")
	}
	if output == "" {
		extension := ".tar.gz"
		if options.Passphrase != "" {
			extension += ".age"
		}
		output = filepath.Join(dataDir, "backups", "feather-"+time.Now().UTC().Format("20060102T150405Z")+extension)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return BackupReport{}, err
	}
	if _, err := os.Lstat(output); err == nil {
		return BackupReport{}, errors.New("备份输出文件已存在")
	} else if !os.IsNotExist(err) {
		return BackupReport{}, err
	}

	databaseSnapshot, cleanupSnapshot, err := createDatabaseSnapshot(ctx, dataDir)
	if err != nil {
		return BackupReport{}, err
	}
	defer cleanupSnapshot()
	sources, warnings, err := collectBackupSources(ctx, cfg, dataDir, databaseSnapshot)
	if err != nil {
		return BackupReport{}, err
	}
	warnings = append(warnings, externalLocalStorageWarnings(ctx, cfg, dataDir)...)
	manifest := BackupManifest{
		ApplicationVersion: cfg.Version,
		DatabaseVersion:    databaseVersionFromFile(databaseSnapshot),
		CreatedAt:          nowUTC(),
		IncludesMasterKey:  false,
		Files:              make([]BackupFile, 0, len(sources)),
	}
	for _, source := range sources {
		sum, err := hashFile(ctx, source.SourcePath, source.Info.Size())
		if err != nil {
			return BackupReport{}, fmt.Errorf("计算 %s 校验值失败: %w", source.ArchivePath, err)
		}
		manifest.Files = append(manifest.Files, BackupFile{
			Path: source.ArchivePath, Size: source.Info.Size(), SHA256: sum,
		})
		if source.ArchivePath == "master.key" {
			manifest.IncludesMasterKey = true
		}
	}
	manifest.FileCount = len(manifest.Files)
	if !manifest.IncludesMasterKey {
		warnings = append(warnings, "备份未包含主密钥；恢复远程存储凭据时必须另行提供原主密钥")
	}

	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return BackupReport{}, err
	}
	temp, err := os.CreateTemp(filepath.Dir(output), ".feather-backup-*.tmp")
	if err != nil {
		return BackupReport{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	archiveOutput := io.Writer(temp)
	var encryptedWriter io.WriteCloser
	if options.Passphrase != "" {
		recipient, recipientErr := age.NewScryptRecipient(options.Passphrase)
		if recipientErr != nil {
			_ = temp.Close()
			return BackupReport{}, recipientErr
		}
		encryptedWriter, err = age.Encrypt(temp, recipient)
		if err != nil {
			_ = temp.Close()
			return BackupReport{}, fmt.Errorf("初始化备份加密失败: %w", err)
		}
		archiveOutput = encryptedWriter
	}
	if err = writeBackupArchive(ctx, archiveOutput, sources, manifest); err == nil && encryptedWriter != nil {
		err = encryptedWriter.Close()
	}
	if err != nil {
		if encryptedWriter != nil {
			_ = encryptedWriter.Close()
		}
		_ = temp.Close()
		return BackupReport{}, err
	}
	if err = temp.Sync(); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err != nil {
		return BackupReport{}, err
	}
	if err := os.Rename(tempName, output); err != nil {
		return BackupReport{}, err
	}
	return BackupReport{Path: output, Manifest: manifest, Encrypted: options.Passphrase != "", Warnings: warnings}, nil
}

func externalLocalStorageWarnings(ctx context.Context, cfg Config, dataDir string) []string {
	db, err := openReadOnlyDB(filepath.Join(dataDir, "feather.db"))
	if err != nil {
		return nil
	}
	defer db.Close()
	keyPath := cfg.MasterKeyFile
	if keyPath == "" {
		keyPath = filepath.Join(dataDir, "master.key")
	}
	key, status, message := inspectMasterKey(keyPath)
	if status == DoctorError {
		return []string{"无法识别数据目录外的本地存储: " + message}
	}
	rows, err := db.QueryContext(ctx, `SELECT id,config FROM storages WHERE type='local' ORDER BY id`)
	if err != nil {
		return []string{"无法读取本地存储配置，数据目录外的文件可能未备份: " + err.Error()}
	}
	defer rows.Close()
	var warnings []string
	for rows.Next() {
		var id, encrypted string
		if err := rows.Scan(&id, &encrypted); err != nil {
			warnings = append(warnings, "无法读取一个本地存储配置，数据目录外的文件可能未备份")
			continue
		}
		config := make(map[string]any)
		if err := decryptJSON(key, encrypted, &config); err != nil {
			warnings = append(warnings, "本地存储 "+id+" 的配置无法解密，可能需要单独备份")
			continue
		}
		root := stringValue(config, "data_dir")
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		root, err = filepath.Abs(root)
		if err != nil {
			warnings = append(warnings, "本地存储 "+id+" 的绝对路径无效，可能需要单独备份")
			continue
		}
		if !pathWithin(dataDir, root) {
			warnings = append(warnings, "本地存储 "+id+" 位于数据目录外，未包含在归档中，必须单独备份: "+root)
		}
	}
	return warnings
}

func collectBackupSources(ctx context.Context, cfg Config, dataDir, databaseSnapshot string) ([]backupSource, []string, error) {
	var sources []backupSource
	var warnings []string
	var total int64
	err := filepath.Walk(dataDir, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dataDir, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		archivePath := filepath.ToSlash(rel)
		if databaseSnapshot != "" && (archivePath == "feather.db" || archivePath == "feather.db-wal" || archivePath == "feather.db-shm") {
			return nil
		}
		first := strings.SplitN(archivePath, "/", 2)[0]
		if info.IsDir() && (first == "backups" || first == "tmp") {
			return filepath.SkipDir
		}
		if first == "backups" || first == "tmp" {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, "已跳过符号链接: "+archivePath)
			return nil
		}
		if !info.Mode().IsRegular() {
			warnings = append(warnings, "已跳过非普通文件: "+archivePath)
			return nil
		}
		if err := validateBackupSize(len(sources)+1, info.Size(), total+info.Size()); err != nil {
			return err
		}
		total += info.Size()
		sources = append(sources, backupSource{ArchivePath: archivePath, SourcePath: current, Info: info})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	masterPath := cfg.MasterKeyFile
	if masterPath == "" {
		masterPath = filepath.Join(dataDir, "master.key")
	}
	masterPath, err = filepath.Abs(masterPath)
	if err != nil {
		return nil, nil, err
	}
	inside, err := filepath.Rel(dataDir, masterPath)
	if err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		// The ordinary data-directory walk already includes the key.
	} else if info, statErr := os.Stat(masterPath); statErr == nil && info.Mode().IsRegular() {
		if hasArchivePath(sources, "master.key") {
			return nil, nil, errors.New("数据目录中的 master.key 与外部主密钥路径冲突")
		}
		if err := validateBackupSize(len(sources)+1, info.Size(), total+info.Size()); err != nil {
			return nil, nil, err
		}
		sources = append(sources, backupSource{ArchivePath: "master.key", SourcePath: masterPath, Info: info})
		warnings = append(warnings, "主密钥位于数据目录外，已保存为归档根目录的 master.key")
	}
	if databaseSnapshot != "" {
		info, statErr := os.Stat(databaseSnapshot)
		if statErr != nil {
			return nil, nil, fmt.Errorf("数据库快照不可用: %w", statErr)
		}
		if err := validateBackupSize(len(sources)+1, info.Size(), total+info.Size()); err != nil {
			return nil, nil, err
		}
		sources = append(sources, backupSource{ArchivePath: "feather.db", SourcePath: databaseSnapshot, Info: info})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ArchivePath < sources[j].ArchivePath })
	return sources, warnings, nil
}

func hasArchivePath(sources []backupSource, candidate string) bool {
	for _, source := range sources {
		if source.ArchivePath == candidate {
			return true
		}
	}
	return false
}

func databaseVersionFromFile(filename string) int {
	if _, err := os.Stat(filename); err != nil {
		return 0
	}
	db, err := openReadOnlyDB(filename)
	if err != nil {
		return 0
	}
	defer db.Close()
	var version int
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)
	return version
}

func hashFile(ctx context.Context, filename string, expected int64) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := copyWithContext(ctx, hash, io.LimitReader(file, expected+1))
	if err != nil {
		return "", err
	}
	if written != expected {
		return "", errors.New("文件在备份期间发生变化")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeBackupArchive(ctx context.Context, output io.Writer, sources []backupSource, manifest BackupManifest) error {
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	closeWriters := func() error {
		if err := tarWriter.Close(); err != nil {
			_ = gzipWriter.Close()
			return err
		}
		return gzipWriter.Close()
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: "manifest.json", Mode: 0600, Size: int64(len(manifestJSON)), ModTime: time.Now().UTC(), Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	if _, err := tarWriter.Write(manifestJSON); err != nil {
		return err
	}
	for index, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := &tar.Header{
			Name: source.ArchivePath, Mode: int64(source.Info.Mode().Perm()), Size: source.Info.Size(),
			ModTime: source.Info.ModTime(), Typeflag: tar.TypeReg,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(source.SourcePath)
		if err != nil {
			return err
		}
		hash := sha256.New()
		written, copyErr := copyWithContext(ctx, io.MultiWriter(tarWriter, hash), io.LimitReader(file, source.Info.Size()+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != source.Info.Size() {
			return fmt.Errorf("%s 在备份期间发生变化", source.ArchivePath)
		}
		if index >= len(manifest.Files) ||
			!strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), manifest.Files[index].SHA256) {
			return fmt.Errorf("%s 在生成清单后发生变化", source.ArchivePath)
		}
	}
	return closeWriters()
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written, writeErr := dst.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func backupArchiveEncrypted(filename string) (bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, len("age-encryption.org/v1"))
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return false, err
	}
	return string(header[:n]) == "age-encryption.org/v1", nil
}

func materializeBackupArchive(ctx context.Context, archivePath, passphrase string) (string, bool, func(), error) {
	encrypted, err := backupArchiveEncrypted(archivePath)
	if err != nil {
		return "", false, func() {}, err
	}
	if !encrypted {
		return archivePath, false, func() {}, nil
	}
	if passphrase == "" {
		return "", true, func() {}, errors.New("备份已加密，必须提供备份口令")
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return "", true, func() {}, fmt.Errorf("初始化备份解密失败: %w", err)
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return "", true, func() {}, err
	}
	reader, err := age.Decrypt(input, identity)
	if err != nil {
		_ = input.Close()
		return "", true, func() {}, fmt.Errorf("解密备份失败，口令可能不正确: %w", err)
	}
	temp, err := os.CreateTemp("", "feather-decrypted-backup-*.tar.gz")
	if err != nil {
		_ = input.Close()
		return "", true, func() {}, err
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = input.Close()
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	limit := maxBackupTotalSize + maxBackupFileSize + maxManifestSize
	written, copyErr := copyWithContext(ctx, temp, io.LimitReader(reader, limit+1))
	if copyErr == nil && written > limit {
		copyErr = errors.New("解密后的备份超过安全大小限制")
	}
	if syncErr := temp.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := temp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	_ = input.Close()
	if copyErr != nil {
		cleanup()
		return "", true, func() {}, copyErr
	}
	return tempName, true, func() { _ = os.Remove(tempName) }, nil
}

func VerifyBackup(ctx context.Context, archivePath, passphrase string) (BackupVerificationReport, error) {
	if strings.TrimSpace(archivePath) == "" {
		return BackupVerificationReport{}, errors.New("必须指定备份归档")
	}
	absolute, err := filepath.Abs(archivePath)
	if err != nil {
		return BackupVerificationReport{}, err
	}
	prepared, encrypted, cleanup, err := materializeBackupArchive(ctx, absolute, passphrase)
	if err != nil {
		return BackupVerificationReport{}, err
	}
	defer cleanup()
	manifest, err := inspectBackupArchive(ctx, prepared)
	if err != nil {
		return BackupVerificationReport{}, err
	}
	stage, err := os.MkdirTemp("", "feather-backup-verify-*")
	if err != nil {
		return BackupVerificationReport{}, err
	}
	defer os.RemoveAll(stage)
	if err := extractAndVerifyBackup(ctx, prepared, stage, manifest); err != nil {
		return BackupVerificationReport{}, err
	}
	localFiles, err := validateRestoredSnapshot(ctx, stage)
	if err != nil {
		return BackupVerificationReport{}, err
	}
	return BackupVerificationReport{
		Path: absolute, Encrypted: encrypted, Manifest: manifest, DatabaseOK: true, LocalFiles: localFiles,
	}, nil
}

// RestoreBackup validates all paths, limits, file sizes and digests in a
// temporary sibling directory before atomically replacing the data directory.
func RestoreBackup(ctx context.Context, archivePath, dataDir string) (BackupManifest, error) {
	return RestoreBackupWithOptions(ctx, archivePath, dataDir, BackupOptions{})
}

func RestoreBackupWithOptions(ctx context.Context, archivePath, dataDir string, options BackupOptions) (BackupManifest, error) {
	if strings.TrimSpace(archivePath) == "" {
		return BackupManifest{}, errors.New("必须指定备份归档")
	}
	archivePath, err := filepath.Abs(archivePath)
	if err != nil {
		return BackupManifest{}, err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return BackupManifest{}, err
	}
	preparedArchive, _, cleanupArchive, err := materializeBackupArchive(ctx, archivePath, options.Passphrase)
	if err != nil {
		return BackupManifest{}, err
	}
	defer cleanupArchive()
	manifest, err := inspectBackupArchive(ctx, preparedArchive)
	if err != nil {
		return BackupManifest{}, err
	}
	parent := filepath.Dir(dataDir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return BackupManifest{}, err
	}
	stage, err := os.MkdirTemp(parent, ".feather-restore-*")
	if err != nil {
		return BackupManifest{}, err
	}
	defer os.RemoveAll(stage)
	if err := extractAndVerifyBackup(ctx, preparedArchive, stage, manifest); err != nil {
		return BackupManifest{}, err
	}
	if _, err := validateRestoredSnapshot(ctx, stage); err != nil {
		return BackupManifest{}, err
	}

	previous := filepath.Join(parent, ".feather-previous-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	dataExists := false
	if _, statErr := os.Stat(dataDir); statErr == nil {
		dataExists = true
		if err := os.Rename(dataDir, previous); err != nil {
			return BackupManifest{}, fmt.Errorf("保留原数据目录失败: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return BackupManifest{}, statErr
	}
	if err := os.Rename(stage, dataDir); err != nil {
		if dataExists {
			_ = os.Rename(previous, dataDir)
		}
		return BackupManifest{}, fmt.Errorf("切换恢复目录失败: %w", err)
	}
	if dataExists {
		if err := os.RemoveAll(previous); err != nil {
			return manifest, fmt.Errorf("恢复成功，但清理旧数据目录失败: %w", err)
		}
	}
	return manifest, nil
}

func validateRestoredSnapshot(ctx context.Context, stage string) (int, error) {
	db, err := openReadOnlyDB(filepath.Join(stage, "feather.db"))
	if err != nil {
		return 0, fmt.Errorf("恢复后的数据库无法打开: %w", err)
	}
	defer db.Close()
	var quick string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quick); err != nil || !strings.EqualFold(quick, "ok") {
		if err == nil {
			err = errors.New(quick)
		}
		return 0, fmt.Errorf("恢复后的数据库完整性检查失败: %w", err)
	}
	key, keyErr := backupValidationKey(stage)
	rows, err := db.QueryContext(ctx, "SELECT id,type,config FROM storages ORDER BY id")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type localStorageSnapshot struct{ id, root string }
	storageSnapshots := make([]localStorageSnapshot, 0)
	for rows.Next() {
		var storageID, storageType, encrypted string
		if err := rows.Scan(&storageID, &storageType, &encrypted); err != nil {
			return 0, err
		}
		if keyErr != nil {
			return 0, fmt.Errorf("备份包含存储配置，但主密钥不可用: %w", keyErr)
		}
		config := map[string]any{}
		if err := decryptJSON(key, encrypted, &config); err != nil {
			return 0, fmt.Errorf("存储 %s 配置无法使用备份主密钥解密: %w", storageID, err)
		}
		if storageType == "local" {
			root := stringValue(config, "data_dir")
			if root == "" {
				root = "images"
			}
			storageSnapshots = append(storageSnapshots, localStorageSnapshot{id: storageID, root: root})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	_ = rows.Close()
	localFiles := 0
	for _, snapshot := range storageSnapshots {
		storageID, root := snapshot.id, snapshot.root
		if !filepath.IsAbs(root) {
			root = filepath.Join(stage, root)
		} else if !pathWithin(stage, root) {
			// External local directories are intentionally not part of the
			// archive; CreateBackup already reports this limitation.
			continue
		}
		checkRows, queryErr := db.QueryContext(ctx, `SELECT object_key FROM images WHERE storage_id=? UNION ALL SELECT v.object_key FROM image_variants v JOIN images i ON i.id=v.image_id WHERE i.storage_id=?`, storageID, storageID)
		if queryErr != nil {
			return localFiles, queryErr
		}
		storage := &localStorage{root: root}
		for checkRows.Next() {
			var keyName string
			if err := checkRows.Scan(&keyName); err != nil {
				_ = checkRows.Close()
				return localFiles, err
			}
			target, err := storage.safe(keyName)
			if err != nil {
				_ = checkRows.Close()
				return localFiles, fmt.Errorf("存储 %s 包含无效对象路径: %w", storageID, err)
			}
			info, err := os.Stat(target)
			if err != nil || !info.Mode().IsRegular() {
				_ = checkRows.Close()
				if err == nil {
					err = errors.New("不是普通文件")
				}
				return localFiles, fmt.Errorf("存储 %s 缺少对象 %s: %w", storageID, keyName, err)
			}
			localFiles++
		}
		if err := checkRows.Err(); err != nil {
			_ = checkRows.Close()
			return localFiles, err
		}
		_ = checkRows.Close()
	}
	return localFiles, nil
}

func backupValidationKey(stage string) ([]byte, error) {
	keyPath := filepath.Join(stage, "master.key")
	data, err := os.ReadFile(keyPath)
	if err == nil {
		key, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("归档中的 master.key 格式无效")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取归档主密钥失败: %w", err)
	}
	encoded := strings.TrimSpace(os.Getenv("FEATHER_MASTER_KEY"))
	if encoded == "" {
		return nil, errors.New("归档未包含 master.key，且未提供 FEATHER_MASTER_KEY")
	}
	key, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
	if decodeErr != nil || len(key) != 32 {
		return nil, errors.New("FEATHER_MASTER_KEY 格式无效")
	}
	return key, nil
}

func inspectBackupArchive(ctx context.Context, archivePath string) (BackupManifest, error) {
	reader, closeReader, err := openTarGzip(archivePath)
	if err != nil {
		return BackupManifest{}, err
	}
	defer closeReader()
	var manifest BackupManifest
	seenManifest := false
	seen := make(map[string]struct{})
	var count int
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return BackupManifest{}, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return BackupManifest{}, fmt.Errorf("读取归档失败: %w", err)
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return BackupManifest{}, err
		}
		if _, exists := seen[name]; exists {
			return BackupManifest{}, fmt.Errorf("归档包含重复路径 %q", name)
		}
		seen[name] = struct{}{}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return BackupManifest{}, fmt.Errorf("归档包含不允许的条目类型: %s", name)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if name == "manifest.json" {
			if header.Size < 0 || header.Size > maxManifestSize {
				return BackupManifest{}, errors.New("备份清单大小无效")
			}
			data, err := io.ReadAll(io.LimitReader(reader, maxManifestSize+1))
			if err != nil || int64(len(data)) != header.Size {
				return BackupManifest{}, errors.New("读取备份清单失败")
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return BackupManifest{}, fmt.Errorf("备份清单格式无效: %w", err)
			}
			seenManifest = true
			continue
		}
		count++
		total += header.Size
		if err := validateBackupSize(count, header.Size, total); err != nil {
			return BackupManifest{}, err
		}
	}
	if !seenManifest {
		return BackupManifest{}, errors.New("归档缺少 manifest.json")
	}
	if err := validateManifest(manifest, seen); err != nil {
		return BackupManifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest BackupManifest, archiveEntries map[string]struct{}) error {
	if manifest.FileCount != len(manifest.Files) || manifest.FileCount > maxBackupFiles {
		return errors.New("备份清单文件数量无效")
	}
	expected := make(map[string]struct{}, len(manifest.Files))
	var total int64
	for _, file := range manifest.Files {
		name, err := safeArchivePath(file.Path)
		if err != nil {
			return fmt.Errorf("备份清单路径无效: %w", err)
		}
		if name == "manifest.json" {
			return errors.New("备份清单不能包含自身")
		}
		if _, exists := expected[name]; exists {
			return fmt.Errorf("备份清单包含重复路径 %q", name)
		}
		if len(file.SHA256) != sha256.Size*2 {
			return fmt.Errorf("%s 的 SHA-256 无效", name)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("%s 的 SHA-256 无效", name)
		}
		total += file.Size
		if err := validateBackupSize(len(expected)+1, file.Size, total); err != nil {
			return err
		}
		if _, exists := archiveEntries[name]; !exists {
			return fmt.Errorf("归档缺少清单文件 %q", name)
		}
		expected[name] = struct{}{}
	}
	for name := range archiveEntries {
		if name == "manifest.json" {
			continue
		}
		if _, exists := expected[name]; !exists && !archiveDirectoryForFiles(name, expected) {
			return fmt.Errorf("归档包含未列入清单的文件 %q", name)
		}
	}
	return nil
}

func archiveDirectoryForFiles(name string, files map[string]struct{}) bool {
	prefix := strings.TrimSuffix(name, "/") + "/"
	for filename := range files {
		if strings.HasPrefix(filename, prefix) {
			return true
		}
	}
	return false
}

func extractAndVerifyBackup(ctx context.Context, archivePath, stage string, manifest BackupManifest) error {
	expected := make(map[string]BackupFile, len(manifest.Files))
	for _, file := range manifest.Files {
		expected[file.Path] = file
	}
	reader, closeReader, err := openTarGzip(archivePath)
	if err != nil {
		return err
	}
	defer closeReader()
	verified := make(map[string]struct{}, len(expected))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if name == "manifest.json" || header.Typeflag == tar.TypeDir {
			continue
		}
		entry, exists := expected[name]
		if !exists || header.Size != entry.Size {
			return fmt.Errorf("%s 的归档大小与清单不一致", name)
		}
		target := filepath.Join(stage, filepath.FromSlash(name))
		if !pathWithin(stage, target) {
			return fmt.Errorf("归档路径越界: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0600|0600)
		if err != nil {
			return err
		}
		hash := sha256.New()
		written, copyErr := copyWithContext(ctx, io.MultiWriter(file, hash), io.LimitReader(reader, entry.Size+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != entry.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), entry.SHA256) {
			return fmt.Errorf("%s 校验失败", name)
		}
		verified[name] = struct{}{}
	}
	if len(verified) != len(expected) {
		return errors.New("归档未完整恢复全部清单文件")
	}
	return nil
}

func openTarGzip(filename string) (*tar.Reader, func() error, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("备份归档不是有效的 tar.gz: %w", err)
	}
	return tar.NewReader(gzipReader), func() error {
		gzipErr := gzipReader.Close()
		fileErr := file.Close()
		if gzipErr != nil {
			return gzipErr
		}
		return fileErr
	}, nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
		return "", fmt.Errorf("归档路径无效: %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("归档路径越界: %q", name)
	}
	return clean, nil
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateBackupSize(count int, single, total int64) error {
	switch {
	case count > maxBackupFiles:
		return fmt.Errorf("归档文件数量超过限制 %d", maxBackupFiles)
	case single < 0 || single > maxBackupFileSize:
		return fmt.Errorf("归档单文件大小超过限制 %d", maxBackupFileSize)
	case total < 0 || total > maxBackupTotalSize:
		return fmt.Errorf("归档总大小超过限制 %d", maxBackupTotalSize)
	default:
		return nil
	}
}

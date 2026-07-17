package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
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
	Path     string         `json:"path"`
	Manifest BackupManifest `json:"manifest"`
	Warnings []string       `json:"warnings,omitempty"`
}

type backupSource struct {
	ArchivePath string
	SourcePath  string
	Info        os.FileInfo
}

// CreateBackup creates an offline tar.gz snapshot. Callers must ensure the
// server is stopped so SQLite and local objects cannot change during the copy.
func CreateBackup(ctx context.Context, cfg Config, output string) (BackupReport, error) {
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return BackupReport{}, err
	}
	if output == "" {
		output = filepath.Join(dataDir, "backups", "feather-"+time.Now().UTC().Format("20060102T150405Z")+".tar.gz")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return BackupReport{}, err
	}

	sources, warnings, err := collectBackupSources(ctx, cfg, dataDir)
	if err != nil {
		return BackupReport{}, err
	}
	warnings = append(warnings, externalLocalStorageWarnings(ctx, cfg, dataDir)...)
	manifest := BackupManifest{
		ApplicationVersion: cfg.Version,
		DatabaseVersion:    databaseVersionFromFile(filepath.Join(dataDir, "feather.db")),
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
	if err = writeBackupArchive(ctx, temp, sources, manifest); err != nil {
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
	return BackupReport{Path: output, Manifest: manifest, Warnings: warnings}, nil
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

func collectBackupSources(ctx context.Context, cfg Config, dataDir string) ([]backupSource, []string, error) {
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

func writeBackupArchive(ctx context.Context, output *os.File, sources []backupSource, manifest BackupManifest) error {
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

// RestoreBackup validates all paths, limits, file sizes and digests in a
// temporary sibling directory before atomically replacing the data directory.
func RestoreBackup(ctx context.Context, archivePath, dataDir string) (BackupManifest, error) {
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
	manifest, err := inspectBackupArchive(ctx, archivePath)
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
	if err := extractAndVerifyBackup(ctx, archivePath, stage, manifest); err != nil {
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

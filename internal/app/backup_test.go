package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndRestoreBackup(t *testing.T) {
	t.Setenv("FEATHER_MASTER_KEY", "")
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "images", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := openDB(filepath.Join(source, "feather.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMasterKey(filepath.Join(source, "master.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "images", "nested", "photo.png"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "backups", "old.tar.gz"), []byte("excluded"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	report, err := CreateBackup(context.Background(), Config{DataDir: source, Version: "test"}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Manifest.FileCount != 3 || !report.Manifest.IncludesMasterKey {
		t.Fatalf("备份清单错误: %+v", report.Manifest)
	}

	target := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := RestoreBackup(context.Background(), archive, target)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FileCount != 3 {
		t.Fatalf("恢复清单错误: %+v", manifest)
	}
	content, err := os.ReadFile(filepath.Join(target, "images", "nested", "photo.png"))
	if err != nil || string(content) != "image" {
		t.Fatalf("图片未恢复: %q %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("旧数据目录未被切换: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "backups", "old.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("backups 目录不应进入归档: %v", err)
	}
}

func TestRestoreRejectsTraversalAndPreservesData(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "data")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "malicious.tar.gz")
	writeRawBackup(t, archive, []rawBackupEntry{
		{name: "manifest.json", content: []byte(`{"application_version":"x","database_version":1,"created_at":"now","file_count":1,"files":[{"path":"../escaped","size":3,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)},
		{name: "../escaped", content: []byte("bad")},
	})
	if _, err := RestoreBackup(context.Background(), archive, target); err == nil {
		t.Fatal("路径穿越归档应被拒绝")
	}
	content, err := os.ReadFile(filepath.Join(target, "sentinel"))
	if err != nil || string(content) != "safe" {
		t.Fatalf("失败恢复破坏了原数据: %q %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("路径穿越写出了数据目录: %v", err)
	}
}

func TestRestoreVerifiesDigestBeforeSwitch(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "data")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := BackupManifest{
		ApplicationVersion: "x", CreatedAt: "now", FileCount: 1,
		Files: []BackupFile{{Path: "feather.db", Size: 3, SHA256: hex.EncodeToString(make([]byte, sha256.Size))}},
	}
	manifestJSON, _ := json.Marshal(manifest)
	archive := filepath.Join(parent, "corrupt.tar.gz")
	writeRawBackup(t, archive, []rawBackupEntry{
		{name: "manifest.json", content: manifestJSON},
		{name: "feather.db", content: []byte("bad")},
	})
	if _, err := RestoreBackup(context.Background(), archive, target); err == nil {
		t.Fatal("摘要不匹配的归档应被拒绝")
	}
	content, err := os.ReadFile(filepath.Join(target, "sentinel"))
	if err != nil || string(content) != "safe" {
		t.Fatalf("校验失败破坏了原数据: %q %v", content, err)
	}
}

func TestRestoreRejectsSymlinkEntry(t *testing.T) {
	parent := t.TempDir()
	archive := filepath.Join(parent, "link.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	manifest := BackupManifest{ApplicationVersion: "x", CreatedAt: "now", Files: []BackupFile{}}
	data, _ := json.Marshal(manifest)
	_ = tw.WriteHeader(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0600})
	_, _ = tw.Write(data)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"})
	_ = tw.Close()
	_ = gz.Close()
	_ = file.Close()
	if _, err := RestoreBackup(context.Background(), archive, filepath.Join(parent, "data")); err == nil {
		t.Fatal("符号链接条目应被拒绝")
	}
}

func TestCreateBackupWarnsAboutExternalLocalStorage(t *testing.T) {
	t.Setenv("FEATHER_MASTER_KEY", "")
	a := newTestApp(t)
	external := filepath.Join(t.TempDir(), "external-images")
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptJSON(a.masterKey, map[string]any{"data_dir": external})
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := a.db.Exec(`INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at)
		VALUES('external','外部目录','local',1,?,1,?,?)`, encrypted, now, now); err != nil {
		t.Fatal(err)
	}
	dataDir := a.cfg.DataDir
	keyFile := a.cfg.MasterKeyFile
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := CreateBackup(context.Background(), Config{
		DataDir: dataDir, MasterKeyFile: keyFile, Version: "test",
	}, filepath.Join(t.TempDir(), "backup.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "external") && strings.Contains(warning, external) && strings.Contains(warning, "单独备份") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未警告外部本地存储: %+v", report.Warnings)
	}
	if _, err := VerifyBackup(context.Background(), report.Path, ""); err != nil {
		t.Fatalf("包含有效主密钥和存储配置的备份未通过校验: %v", err)
	}
}

func TestBackupArchiveRechecksDigestWhileWriting(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "feather.db")
	if err := os.WriteFile(sourcePath, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "backup-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	manifest := BackupManifest{
		FileCount: 1,
		Files: []BackupFile{{
			Path: "feather.db", Size: info.Size(),
			SHA256: hex.EncodeToString(make([]byte, sha256.Size)),
		}},
	}
	err = writeBackupArchive(context.Background(), output, []backupSource{{
		ArchivePath: "feather.db", SourcePath: sourcePath, Info: info,
	}}, manifest)
	if err == nil || !strings.Contains(err.Error(), "生成清单后发生变化") {
		t.Fatalf("归档写入未复检清单摘要: %v", err)
	}
}

func TestCreateBackupWarnsWhenMasterKeyIsMissing(t *testing.T) {
	t.Setenv("FEATHER_MASTER_KEY", "")
	dataDir := t.TempDir()
	db, err := openDB(filepath.Join(dataDir, "feather.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "note.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := CreateBackup(context.Background(), Config{DataDir: dataDir, Version: "test"}, filepath.Join(t.TempDir(), "backup.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Manifest.IncludesMasterKey {
		t.Fatal("不存在的主密钥不应标记为已包含")
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "未包含主密钥") {
			found = true
		}
	}
	if !found {
		t.Fatalf("主密钥缺失时未输出警告: %+v", report.Warnings)
	}
}

func TestEncryptedBackupCanVerifyAndRestore(t *testing.T) {
	t.Setenv("FEATHER_MASTER_KEY", "")
	source := t.TempDir()
	db, err := openDB(filepath.Join(source, "feather.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMasterKey(filepath.Join(source, "master.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "images", "one.png"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "encrypted.tar.gz.age")
	passphrase := "correct horse battery staple"
	report, err := CreateBackupWithOptions(context.Background(), Config{DataDir: source, Version: "test"}, archive, BackupOptions{Passphrase: passphrase})
	if err != nil || !report.Encrypted {
		t.Fatalf("创建加密备份失败: %+v %v", report, err)
	}
	if _, err := VerifyBackup(context.Background(), archive, "wrong passphrase"); err == nil {
		t.Fatal("错误口令不应通过备份校验")
	}
	verified, err := VerifyBackup(context.Background(), archive, passphrase)
	if err != nil || !verified.DatabaseOK || !verified.Encrypted {
		t.Fatalf("加密备份校验失败: %+v %v", verified, err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreBackupWithOptions(context.Background(), archive, target, BackupOptions{Passphrase: passphrase}); err != nil {
		t.Fatalf("恢复加密备份失败: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "images", "one.png")); err != nil || string(content) != "image" {
		t.Fatalf("加密备份图片恢复错误: %q %v", content, err)
	}
}

func TestCreateBackupDoesNotOverwriteExistingFile(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openDB(filepath.Join(dataDir, "feather.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "existing.tar.gz")
	if err := os.WriteFile(output, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateBackup(context.Background(), Config{DataDir: dataDir}, output); err == nil {
		t.Fatal("已有备份文件不应被覆盖")
	}
	if content, err := os.ReadFile(output); err != nil || string(content) != "keep" {
		t.Fatalf("已有文件被修改: %q %v", content, err)
	}
}

type rawBackupEntry struct {
	name    string
	content []byte
}

func writeRawBackup(t *testing.T, filename string, entries []rawBackupEntry) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: tar.TypeReg, Size: int64(len(entry.content)), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

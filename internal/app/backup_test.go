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
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "images", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "feather.db"), []byte("database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "master.key"), []byte("key"), 0600); err != nil {
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

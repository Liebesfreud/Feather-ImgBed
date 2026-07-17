package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorExitCodesAndStableJSON(t *testing.T) {
	missing := RunDoctor(context.Background(), Config{DataDir: filepath.Join(t.TempDir(), "missing"), Version: "test"}, false)
	if missing.ExitCode() != 2 || missing.Status != DoctorError {
		t.Fatalf("缺少数据目录应为阻止运行错误: %+v", missing)
	}
	encoded, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DoctorReport
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("诊断 JSON 不稳定: %v", err)
	}
	if decoded.ApplicationVersion != "test" || len(decoded.Checks) == 0 {
		t.Fatalf("诊断 JSON 缺少稳定字段: %s", encoded)
	}
}

func TestDoctorDetectsUnsafeMasterKeyPermissions(t *testing.T) {
	a := newTestApp(t)
	dataDir := a.cfg.DataDir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dataDir, "master.key")
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}
	report := RunDoctor(context.Background(), Config{
		DataDir: dataDir, MasterKeyFile: keyPath, Version: "test",
	}, false)
	if report.ExitCode() != 1 || report.Status != DoctorWarning {
		t.Fatalf("过宽主密钥权限应产生警告: %+v", report)
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "master_key" && check.Status == DoctorWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("诊断未报告主密钥权限: %+v", report.Checks)
	}
}

func TestDoctorChecksLocalVariantFiles(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	body, contentType := uploadBody(t, pngBytes(t))
	recorder, response := request(t, handler, "POST", "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != 201 {
		t.Fatalf("上传测试图片失败: %s", recorder.Body.String())
	}
	var image Image
	if err := json.Unmarshal(response.Data, &image); err != nil {
		t.Fatal(err)
	}
	var variantKey string
	if err := a.db.QueryRow(`SELECT object_key FROM image_variants WHERE image_id=? AND kind='thumbnail'`, image.ID).Scan(&variantKey); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(a.cfg.DataDir, "images", filepath.FromSlash(variantKey))); err != nil {
		t.Fatal(err)
	}
	dataDir, keyFile := a.cfg.DataDir, a.cfg.MasterKeyFile
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	report := RunDoctor(context.Background(), Config{DataDir: dataDir, MasterKeyFile: keyFile, Version: "test"}, false)
	found := false
	for _, check := range report.Checks {
		if check.Name == "local_image_files" && check.Status == DoctorWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("诊断未发现缺失的本地派生文件: %+v", report.Checks)
	}
}

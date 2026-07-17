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

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"feather-imgbed/internal/app"
)

func TestCommandRoutingRejectsUnknownAndMissingArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCommand([]string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("未知命令退出码错误: %d", code)
	}
	if !strings.Contains(stderr.String(), "未知子命令") {
		t.Fatalf("未知命令未输出说明: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCommand([]string{"backup", "restore"}, &stdout, &stderr); code != 2 {
		t.Fatalf("缺少恢复归档时退出码错误: %d", code)
	}
}

func TestValidateBackupSchedule(t *testing.T) {
	if err := validateBackupSchedule(app.Config{BackupRetention: -1}); err != nil {
		t.Fatalf("未启用自动备份时不应校验其余参数: %v", err)
	}
	valid := app.Config{
		BackupInterval:       "24h",
		BackupRetention:      7,
		BackupPassphraseFile: "/run/secrets/feather-backup",
		BackupVerifyRemote:   10,
	}
	if err := validateBackupSchedule(valid); err != nil {
		t.Fatalf("有效自动备份配置被拒绝: %v", err)
	}
	valid.BackupVerifyRemote = 1001
	if err := validateBackupSchedule(valid); err == nil {
		t.Fatal("过大的远程校验抽样数应被拒绝")
	}
}

func TestDoctorJSONCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCommand([]string{"doctor", "--json", "--data", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("阻止运行的诊断退出码错误: %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status":"error"`) || !strings.Contains(stdout.String(), `"checks"`) {
		t.Fatalf("诊断 JSON 输出错误: %s", stdout.String())
	}
}

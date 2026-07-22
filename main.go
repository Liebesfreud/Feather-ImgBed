package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"feather-imgbed/internal/app"
)

var version = "dev"

func main() {
	os.Exit(runCommand(os.Args[1:], os.Stdout, os.Stderr))
}

func runCommand(args []string, stdout, stderr io.Writer) int {
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	switch command {
	case "serve":
		return runServe(args, stdout, stderr)
	case "doctor":
		return runDoctor(args, stdout, stderr)
	case "backup":
		return runBackup(args, stdout, stderr)
	case "thumbnails":
		return runThumbnails(args, stdout, stderr)
	case "auth":
		return runAuth(args, stdout, stderr)
	case "storage":
		return runStorage(args, stdout, stderr)
	case "data":
		return runData(args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n", command)
		printUsage(stderr)
		return 2
	}
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `用法:
  feather-imgbed [serve] [选项]
  feather-imgbed doctor [--json] [--network] [选项]
  feather-imgbed backup create [--output 文件] [--passphrase-file 文件] [选项]
  feather-imgbed backup verify [--passphrase-file 文件] [选项] <归档文件>
  feather-imgbed backup restore [--passphrase-file 文件] [选项] <归档文件>
  feather-imgbed auth reset-password [--username 用户名] --password-file 文件 [选项]
  feather-imgbed storage verify [--storage ID] [--sample 数量] [选项]
  feather-imgbed storage migrate --from 源ID --to 目标ID [选项]
  feather-imgbed data import-dir [选项] <目录>
  feather-imgbed data export --output 目录 [选项]
  feather-imgbed thumbnails rebuild [选项]`)
}

func configFlags(name string, args []string, stderr io.Writer, includeBackup bool) (*flag.FlagSet, app.Config, error) {
	cfg := app.DefaultConfig()
	cfg.Version = version
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.Listen, "listen", cfg.Listen, "监听地址")
	flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
	flags.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "日志级别")
	flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
	if includeBackup {
		flags.StringVar(&cfg.BackupInterval, "backup-interval", cfg.BackupInterval, "自动备份间隔，例如 24h；留空关闭")
		flags.IntVar(&cfg.BackupRetention, "backup-retention", cfg.BackupRetention, "自动备份保留份数")
		flags.StringVar(&cfg.BackupDir, "backup-dir", cfg.BackupDir, "自动备份目录")
		flags.StringVar(&cfg.BackupPassphraseFile, "backup-passphrase-file", cfg.BackupPassphraseFile, "自动备份口令文件")
		flags.IntVar(&cfg.BackupVerifyRemote, "backup-verify-remote", cfg.BackupVerifyRemote, "每个远程存储自动抽样校验数量，0 表示关闭")
	}
	return flags, cfg, flags.Parse(args)
}

func loggerFor(cfg app.Config, output io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}

func runServe(args []string, stdout, stderr io.Writer) int {
	flags, cfg, err := configFlags("serve", args, stderr, true)
	if err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "serve 不接受位置参数")
		return 2
	}
	logger := loggerFor(cfg, stdout)
	if err := validateBackupSchedule(cfg); err != nil {
		logger.Error("自动备份配置无效", "error", err)
		return 2
	}
	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("服务初始化失败", "error", err)
		return 1
	}
	defer application.Close()
	backupCtx, backupCancel := context.WithCancel(context.Background())
	defer backupCancel()
	if cfg.BackupInterval != "" {
		go runBackupScheduler(backupCtx, application, cfg, logger)
	}

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("轻羽图床已启动", "listen", cfg.Listen, "version", version)
		serverErrors <- server.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "error", err)
			return 1
		}
		return 0
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("服务关闭超时", "error", err)
		return 1
	}
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	jsonOutput := false
	network := false
	cfg := app.DefaultConfig()
	cfg.Version = version
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&jsonOutput, "json", false, "输出 JSON")
	flags.BoolVar(&network, "network", false, "测试远程存储网络连接")
	flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
	flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor 不接受位置参数")
		return 2
	}
	report := app.RunDoctor(context.Background(), cfg, network)
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(stderr, "输出诊断报告失败:", err)
			return 2
		}
	} else {
		fmt.Fprintf(stdout, "诊断状态: %s\n", report.Status)
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Message)
		}
	}
	return report.ExitCode()
}

func runBackup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "backup 需要 create、verify 或 restore 子命令")
		return 2
	}
	action := args[0]
	args = args[1:]
	switch action {
	case "create":
		cfg := app.DefaultConfig()
		cfg.Version = version
		output := ""
		passphraseFile := cfg.BackupPassphraseFile
		flags := flag.NewFlagSet("backup create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&output, "output", "", "输出 tar.gz 文件")
		flags.StringVar(&passphraseFile, "passphrase-file", passphraseFile, "备份加密口令文件；留空表示不加密")
		if err := flags.Parse(args); err != nil {
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "backup create 不接受位置参数")
			return 2
		}
		passphrase, err := readSecretFile(passphraseFile)
		if err != nil {
			fmt.Fprintln(stderr, "读取备份口令失败:", err)
			return 2
		}
		report, err := app.CreateBackupWithOptions(context.Background(), cfg, output, app.BackupOptions{Passphrase: passphrase})
		if err != nil {
			fmt.Fprintln(stderr, "创建备份失败:", err)
			return 2
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return 0
	case "verify":
		cfg := app.DefaultConfig()
		passphraseFile := cfg.BackupPassphraseFile
		flags := flag.NewFlagSet("backup verify", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&passphraseFile, "passphrase-file", passphraseFile, "备份解密口令文件")
		if err := flags.Parse(args); err != nil {
			return 2
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "backup verify 必须指定一个归档文件")
			return 2
		}
		passphrase, err := readSecretFile(passphraseFile)
		if err != nil {
			fmt.Fprintln(stderr, "读取备份口令失败:", err)
			return 2
		}
		report, err := app.VerifyBackup(context.Background(), flags.Arg(0), passphrase)
		if err != nil {
			fmt.Fprintln(stderr, "校验备份失败:", err)
			return 1
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return 0
	case "restore":
		cfg := app.DefaultConfig()
		passphraseFile := cfg.BackupPassphraseFile
		flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&passphraseFile, "passphrase-file", passphraseFile, "备份解密口令文件")
		if err := flags.Parse(args); err != nil {
			return 2
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "backup restore 必须指定一个归档文件")
			return 2
		}
		passphrase, err := readSecretFile(passphraseFile)
		if err != nil {
			fmt.Fprintln(stderr, "读取备份口令失败:", err)
			return 2
		}
		manifest, err := app.RestoreBackupWithOptions(context.Background(), flags.Arg(0), cfg.DataDir, app.BackupOptions{Passphrase: passphrase})
		if err != nil {
			fmt.Fprintln(stderr, "恢复备份失败:", err)
			return 2
		}
		_ = json.NewEncoder(stdout).Encode(manifest)
		return 0
	default:
		fmt.Fprintf(stderr, "未知 backup 子命令 %q\n", action)
		return 2
	}
}

func runThumbnails(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "rebuild" {
		fmt.Fprintln(stderr, "thumbnails 需要 rebuild 子命令")
		return 2
	}
	flags, cfg, err := configFlags("thumbnails rebuild", args[1:], stderr, false)
	if err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "thumbnails rebuild 不接受位置参数")
		return 2
	}
	application, err := app.New(cfg, loggerFor(cfg, stderr))
	if err != nil {
		fmt.Fprintln(stderr, "服务初始化失败:", err)
		return 2
	}
	defer application.Close()
	report, err := application.RebuildThumbnails(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "缩略图回填失败:", err)
		return 2
	}
	_ = json.NewEncoder(stdout).Encode(report)
	if report.Failed > 0 {
		return 1
	}
	return 0
}

func readSecretFile(filename string) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return "", nil
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	secret := strings.TrimRight(string(data), "\r\n")
	if secret == "" {
		return "", errors.New("口令文件为空")
	}
	return secret, nil
}

func runAuth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(stderr, "auth 需要 reset-password 子命令")
		return 2
	}
	cfg := app.DefaultConfig()
	username, passwordFile := "", ""
	flags := flag.NewFlagSet("auth reset-password", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
	flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件（保留兼容，不读取）")
	flags.StringVar(&username, "username", "", "管理员用户名；单管理员可省略")
	flags.StringVar(&passwordFile, "password-file", "", "新密码文件")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || passwordFile == "" {
		fmt.Fprintln(stderr, "必须使用 --password-file 指定新密码文件")
		return 2
	}
	password, err := readSecretFile(passwordFile)
	if err != nil {
		fmt.Fprintln(stderr, "读取新密码失败:", err)
		return 2
	}
	if err := app.ResetAdminPassword(context.Background(), cfg, username, password); err != nil {
		fmt.Fprintln(stderr, "重置管理员密码失败:", err)
		return 1
	}
	fmt.Fprintln(stdout, "管理员密码已更新，现有登录会话已注销。")
	return 0
}

func runStorage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "storage 需要 verify 或 migrate 子命令")
		return 2
	}
	switch args[0] {
	case "verify":
		cfg := app.DefaultConfig()
		storageID, sample := "", 10
		includeLocal := false
		flags := flag.NewFlagSet("storage verify", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&storageID, "storage", "", "只校验指定存储")
		flags.IntVar(&sample, "sample", sample, "每个存储抽样对象数量")
		flags.BoolVar(&includeLocal, "include-local", false, "同时校验本地存储")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return 2
		}
		application, err := app.New(cfg, loggerFor(cfg, stderr))
		if err != nil {
			fmt.Fprintln(stderr, "服务初始化失败:", err)
			return 1
		}
		defer application.Close()
		report, err := application.VerifyStorageObjects(context.Background(), storageID, sample, includeLocal)
		if err != nil {
			fmt.Fprintln(stderr, "存储校验失败:", err)
			return 1
		}
		_ = json.NewEncoder(stdout).Encode(report)
		if report.Failed > 0 {
			return 1
		}
		return 0
	case "migrate":
		cfg := app.DefaultConfig()
		sourceID, targetID := "", ""
		limit := 0
		includeTrash, dryRun := true, false
		flags := flag.NewFlagSet("storage migrate", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&sourceID, "from", "", "源存储 ID")
		flags.StringVar(&targetID, "to", "", "目标存储 ID")
		flags.IntVar(&limit, "limit", 0, "最多迁移图片数，0 表示全部")
		flags.BoolVar(&includeTrash, "include-trash", true, "包含回收站图片")
		flags.BoolVar(&dryRun, "dry-run", false, "只检查对象数量，不写入")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || sourceID == "" || targetID == "" {
			fmt.Fprintln(stderr, "storage migrate 必须提供 --from 和 --to")
			return 2
		}
		application, err := app.New(cfg, loggerFor(cfg, stderr))
		if err != nil {
			fmt.Fprintln(stderr, "服务初始化失败:", err)
			return 1
		}
		defer application.Close()
		report, err := application.MigrateStorage(context.Background(), sourceID, targetID, limit, includeTrash, dryRun)
		if err != nil {
			fmt.Fprintln(stderr, "存储迁移失败:", err)
			return 1
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return boolExit(report.Failed == 0)
	default:
		fmt.Fprintf(stderr, "未知 storage 子命令 %q\n", args[0])
		return 2
	}
}

func runData(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "data 需要 import-dir 或 export 子命令")
		return 2
	}
	cfg := app.DefaultConfig()
	switch args[0] {
	case "import-dir":
		storageID, limit := "", 0
		recursive := true
		flags := flag.NewFlagSet("data import-dir", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&storageID, "storage", "", "目标存储 ID；留空使用默认存储")
		flags.BoolVar(&recursive, "recursive", true, "递归扫描子目录")
		flags.IntVar(&limit, "limit", 0, "最多导入图片数，0 表示全部")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 {
			fmt.Fprintln(stderr, "data import-dir 必须指定一个目录")
			return 2
		}
		application, err := app.New(cfg, loggerFor(cfg, stderr))
		if err != nil {
			fmt.Fprintln(stderr, "服务初始化失败:", err)
			return 1
		}
		defer application.Close()
		report, err := application.ImportDirectory(context.Background(), flags.Arg(0), storageID, recursive, limit)
		if err != nil {
			fmt.Fprintln(stderr, "目录导入失败:", err)
			return 1
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return boolExit(report.Failed == 0)
	case "export":
		output := ""
		includeTrash := false
		flags := flag.NewFlagSet("data export", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&output, "output", "", "导出目录")
		flags.BoolVar(&includeTrash, "include-trash", false, "包含回收站图片")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || output == "" {
			fmt.Fprintln(stderr, "data export 必须使用 --output 指定新目录")
			return 2
		}
		application, err := app.New(cfg, loggerFor(cfg, stderr))
		if err != nil {
			fmt.Fprintln(stderr, "服务初始化失败:", err)
			return 1
		}
		defer application.Close()
		report, err := application.ExportData(context.Background(), output, includeTrash)
		if err != nil {
			fmt.Fprintln(stderr, "数据导出失败:", err)
			return 1
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return 0
	default:
		fmt.Fprintf(stderr, "未知 data 子命令 %q\n", args[0])
		return 2
	}
}

func boolExit(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

func validateBackupSchedule(cfg app.Config) error {
	if cfg.BackupInterval == "" {
		return nil
	}
	if cfg.BackupRetention < 1 || cfg.BackupRetention > 365 {
		return errors.New("backup-retention 必须在 1 到 365 之间")
	}
	if cfg.BackupVerifyRemote < 0 || cfg.BackupVerifyRemote > 1000 {
		return errors.New("backup-verify-remote 必须在 0 到 1000 之间")
	}
	interval, err := time.ParseDuration(cfg.BackupInterval)
	if err != nil || interval < time.Minute {
		return errors.New("backup-interval 必须是至少 1 分钟的时间间隔")
	}
	if strings.TrimSpace(cfg.BackupPassphraseFile) == "" {
		return errors.New("启用自动备份时必须配置 backup-passphrase-file")
	}
	return nil
}

func runBackupScheduler(ctx context.Context, application *app.App, cfg app.Config, logger *slog.Logger) {
	interval, err := time.ParseDuration(cfg.BackupInterval)
	if err != nil {
		return
	}
	backupDir := cfg.BackupDir
	if backupDir == "" {
		backupDir = filepath.Join(cfg.DataDir, "backups")
	}
	_ = os.MkdirAll(backupDir, 0700)
	perform := func() {
		passphrase, readErr := readSecretFile(cfg.BackupPassphraseFile)
		if readErr != nil {
			logger.Error("自动备份口令读取失败", "error", readErr)
			return
		}
		extension := ".tar.gz.age"
		output := filepath.Join(backupDir, "feather-"+time.Now().UTC().Format("20060102T150405Z")+extension)
		report, backupErr := app.CreateBackupWithOptions(ctx, cfg, output, app.BackupOptions{Passphrase: passphrase})
		if backupErr != nil {
			logger.Error("自动备份失败", "error", backupErr)
			return
		}
		if verifyReport, verifyErr := app.VerifyBackup(ctx, report.Path, passphrase); verifyErr != nil {
			logger.Error("自动备份校验失败", "path", report.Path, "error", verifyErr)
			return
		} else {
			logger.Info("自动备份完成", "path", report.Path, "files", verifyReport.Manifest.FileCount)
		}
		if cfg.BackupVerifyRemote > 0 {
			if remoteReport, verifyErr := application.VerifyStorageObjects(ctx, "", cfg.BackupVerifyRemote, false); verifyErr != nil {
				logger.Error("远程对象抽样校验失败", "error", verifyErr)
			} else if remoteReport.Failed > 0 {
				logger.Warn("远程对象抽样存在失败", "failed", remoteReport.Failed)
			}
		}
		pruneScheduledBackups(backupDir, cfg.BackupRetention, logger)
	}
	perform()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			perform()
			timer.Reset(interval)
		}
	}
}

func pruneScheduledBackups(directory string, retention int, logger *slog.Logger) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		logger.Warn("自动备份清理失败", "error", err)
		return
	}
	type backupEntry struct {
		path    string
		modTime time.Time
	}
	backups := make([]backupEntry, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "feather-") || !(strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.gz.age")) {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil && info.Mode().IsRegular() {
			backups = append(backups, backupEntry{path: filepath.Join(directory, name), modTime: info.ModTime()})
		}
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].modTime.After(backups[j].modTime) })
	for _, entry := range backups[min(retention, len(backups)):] {
		if err := os.Remove(entry.path); err != nil {
			logger.Warn("自动备份文件清理失败", "path", entry.path, "error", err)
		}
	}
}

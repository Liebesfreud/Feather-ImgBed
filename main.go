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
  feather-imgbed backup create [--output 文件] [选项]
  feather-imgbed backup restore <归档文件> [选项]
  feather-imgbed thumbnails rebuild [选项]`)
}

func configFlags(name string, args []string, stderr io.Writer) (*flag.FlagSet, app.Config, error) {
	cfg := app.DefaultConfig()
	cfg.Version = version
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.Listen, "listen", cfg.Listen, "监听地址")
	flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
	flags.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "日志级别")
	flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
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
	flags, cfg, err := configFlags("serve", args, stderr)
	if err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "serve 不接受位置参数")
		return 2
	}
	logger := loggerFor(cfg, stdout)
	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("服务初始化失败", "error", err)
		return 1
	}
	defer application.Close()

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
		fmt.Fprintln(stderr, "backup 需要 create 或 restore 子命令")
		return 2
	}
	action := args[0]
	args = args[1:]
	switch action {
	case "create":
		cfg := app.DefaultConfig()
		cfg.Version = version
		output := ""
		flags := flag.NewFlagSet("backup create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		flags.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
		flags.StringVar(&output, "output", "", "输出 tar.gz 文件")
		if err := flags.Parse(args); err != nil {
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "backup create 不接受位置参数")
			return 2
		}
		report, err := app.CreateBackup(context.Background(), cfg, output)
		if err != nil {
			fmt.Fprintln(stderr, "创建备份失败:", err)
			return 2
		}
		_ = json.NewEncoder(stdout).Encode(report)
		return 0
	case "restore":
		cfg := app.DefaultConfig()
		flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
		if err := flags.Parse(args); err != nil {
			return 2
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "backup restore 必须指定一个归档文件")
			return 2
		}
		manifest, err := app.RestoreBackup(context.Background(), flags.Arg(0), cfg.DataDir)
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
	flags, cfg, err := configFlags("thumbnails rebuild", args[1:], stderr)
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

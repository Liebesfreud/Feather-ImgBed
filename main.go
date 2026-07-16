package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"featherimgbed/internal/app"
)

var version = "dev"

func main() {
	cfg := app.DefaultConfig()
	flag.StringVar(&cfg.Listen, "listen", cfg.Listen, "监听地址")
	flag.StringVar(&cfg.DataDir, "data", cfg.DataDir, "数据目录")
	flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "日志级别")
	flag.StringVar(&cfg.MasterKeyFile, "master-key-file", cfg.MasterKeyFile, "主密钥文件")
	flag.Parse()
	cfg.Version = version

	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("服务初始化失败", "error", err)
		os.Exit(1)
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

	go func() {
		logger.Info("轻羽图床已启动", "listen", cfg.Listen, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("服务关闭超时", "error", err)
	}
}

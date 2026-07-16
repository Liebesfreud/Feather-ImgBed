package app

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen            string
	DataDir           string
	LogLevel          string
	MasterKeyFile     string
	Version           string
	SecureCookie      bool
	TrustedProxyCIDRs []string
}

func DefaultConfig() Config {
	dataDir := env("FEATHER_DATA_DIR", "./data")
	secure, _ := strconv.ParseBool(env("FEATHER_SECURE_COOKIE", "false"))
	return Config{
		Listen:            env("FEATHER_LISTEN", ":8080"),
		DataDir:           dataDir,
		LogLevel:          env("FEATHER_LOG_LEVEL", "info"),
		MasterKeyFile:     env("FEATHER_MASTER_KEY_FILE", ""),
		SecureCookie:      secure,
		TrustedProxyCIDRs: splitCSV(env("FEATHER_TRUSTED_PROXIES", "")),
	}
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

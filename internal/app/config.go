package app

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen               string
	DataDir              string
	LogLevel             string
	MasterKeyFile        string
	Version              string
	SecureCookie         bool
	TrustedProxyCIDRs    []string
	BackupInterval       string
	BackupRetention      int
	BackupDir            string
	BackupPassphraseFile string
	BackupVerifyRemote   int
}

func DefaultConfig() Config {
	dataDir := env("FEATHER_DATA_DIR", "./data")
	secure, _ := strconv.ParseBool(env("FEATHER_SECURE_COOKIE", "false"))
	retention, err := strconv.Atoi(env("FEATHER_BACKUP_RETENTION", "7"))
	if err != nil {
		retention = -1
	}
	verifyRemote, err := strconv.Atoi(env("FEATHER_BACKUP_VERIFY_REMOTE", "0"))
	if err != nil {
		verifyRemote = -1
	}
	return Config{
		Listen:               env("FEATHER_LISTEN", ":8080"),
		DataDir:              dataDir,
		LogLevel:             env("FEATHER_LOG_LEVEL", "info"),
		MasterKeyFile:        env("FEATHER_MASTER_KEY_FILE", ""),
		SecureCookie:         secure,
		TrustedProxyCIDRs:    splitCSV(env("FEATHER_TRUSTED_PROXIES", "")),
		BackupInterval:       env("FEATHER_BACKUP_INTERVAL", ""),
		BackupRetention:      retention,
		BackupDir:            env("FEATHER_BACKUP_DIR", ""),
		BackupPassphraseFile: env("FEATHER_BACKUP_PASSPHRASE_FILE", ""),
		BackupVerifyRemote:   verifyRemote,
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

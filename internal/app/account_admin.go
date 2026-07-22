package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ResetAdminPassword is an offline-friendly recovery path for the single
// administrator account. The running service should be stopped before using
// the command so no login or migration races with the update.
func ResetAdminPassword(ctx context.Context, cfg Config, username, password string) error {
	username = strings.TrimSpace(username)
	if len(password) < 10 || len(password) > bcryptMaxPasswordBytes {
		return errors.New("密码必须为 10 到 72 字节")
	}
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	db, err := openDB(filepath.Join(dataDir, "feather.db"))
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer db.Close()
	if username == "" {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("无法自动确定管理员用户名，请使用 --username 指定")
		}
		if err := db.QueryRowContext(ctx, "SELECT username FROM users LIMIT 1").Scan(&username); err != nil {
			return err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("生成密码摘要失败: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=?,updated_at=? WHERE username=?", string(hash), nowUTC(), username)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE username=?)", username); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

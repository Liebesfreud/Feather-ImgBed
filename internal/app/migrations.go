package app

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	Version int
	Up      func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{Version: 1, Up: migrateV1},
	{Version: 2, Up: migrateV2},
	{Version: 3, Up: migrateV3},
	{Version: 4, Up: migrateV4},
	{Version: 5, Up: migrateV5},
	{Version: 6, Up: migrateV6},
}

var schemaVersion = migrations[len(migrations)-1].Version

func migrate(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("读取数据库版本失败: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于程序支持版本 %d", current, schemaVersion)
	}
	for _, item := range migrations {
		if item.Version <= current {
			continue
		}
		if item.Version != current+1 {
			return fmt.Errorf("数据库迁移版本不连续: 当前 %d，下一版本 %d", current, item.Version)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开始数据库迁移 v%d 失败: %w", item.Version, err)
		}
		if err = item.Up(ctx, tx); err == nil {
			_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", item.Version))
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("数据库迁移 v%d 失败: %w", item.Version, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("提交数据库迁移 v%d 失败: %w", item.Version, err)
		}
		current = item.Version
	}
	return nil
}

func execMigration(ctx context.Context, tx *sql.Tx, statements ...string) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateV1(ctx context.Context, tx *sql.Tx) error {
	return execMigration(ctx, tx,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT NOT NULL, encrypted INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
		`CREATE TABLE storages (id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, config TEXT NOT NULL, encrypted INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE images (id TEXT PRIMARY KEY, hash TEXT NOT NULL, original_name TEXT NOT NULL, object_key TEXT NOT NULL, storage_type TEXT NOT NULL, storage_id TEXT NOT NULL, mime_type TEXT NOT NULL, size INTEGER NOT NULL, width INTEGER, height INTEGER, public_url TEXT NOT NULL, delete_error TEXT, created_at TEXT NOT NULL)`,
		`CREATE INDEX idx_images_created ON images(created_at DESC, id DESC)`,
		`CREATE INDEX idx_images_storage ON images(storage_id, created_at DESC)`,
		`CREATE INDEX idx_images_hash ON images(hash)`,
		`CREATE INDEX idx_images_name ON images(original_name)`,
		`CREATE TABLE api_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, last_used_at TEXT, expires_at TEXT, created_at TEXT NOT NULL)`,
		`CREATE TABLE sessions (id_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, csrf_token TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE INDEX idx_sessions_expires ON sessions(expires_at)`,
	)
}

func migrateV2(ctx context.Context, tx *sql.Tx) error {
	return execMigration(ctx, tx,
		`ALTER TABLE images ADD COLUMN deleted_at TEXT`,
		`ALTER TABLE images ADD COLUMN purge_error TEXT`,
		`CREATE INDEX idx_images_deleted ON images(deleted_at, created_at DESC, id DESC)`,
	)
}

func migrateV3(ctx context.Context, tx *sql.Tx) error {
	return execMigration(ctx, tx,
		`CREATE TABLE image_variants (
			id TEXT PRIMARY KEY,
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			object_key TEXT NOT NULL,
			public_url TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(image_id, kind)
		)`,
		`CREATE INDEX idx_image_variants_image ON image_variants(image_id)`,
	)
}

func migrateV4(ctx context.Context, tx *sql.Tx) error {
	return execMigration(ctx, tx,
		`ALTER TABLE images ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX idx_images_favorite ON images(favorite, deleted_at, created_at DESC)`,
		`CREATE TABLE tags (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			color TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE image_tags (
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			PRIMARY KEY(image_id, tag_id)
		)`,
		`CREATE INDEX idx_image_tags_tag ON image_tags(tag_id, image_id)`,
		`CREATE TABLE albums (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			cover_image_id TEXT REFERENCES images(id) ON DELETE SET NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE album_images (
			album_id TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			added_at TEXT NOT NULL,
			PRIMARY KEY(album_id, image_id)
		)`,
		`CREATE INDEX idx_album_images_order ON album_images(album_id, position, added_at)`,
	)
}

func migrateV5(ctx context.Context, tx *sql.Tx) error {
	// Existing tokens retain their previous full-access behavior. Newly created
	// tokens default to a least-privilege upload scope in the API handler.
	return execMigration(ctx, tx,
		`ALTER TABLE api_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '["settings:admin"]'`,
	)
}

func migrateV6(ctx context.Context, tx *sql.Tx) error {
	return execMigration(ctx, tx,
		`CREATE VIRTUAL TABLE image_search USING fts5(
			original_name,
			content='images',
			content_rowid='rowid',
			tokenize='trigram'
		)`,
		`CREATE TRIGGER images_search_insert AFTER INSERT ON images BEGIN
			INSERT INTO image_search(rowid,original_name) VALUES(new.rowid,new.original_name);
		END`,
		`CREATE TRIGGER images_search_delete AFTER DELETE ON images BEGIN
			INSERT INTO image_search(image_search,rowid,original_name)
			VALUES('delete',old.rowid,old.original_name);
		END`,
		`CREATE TRIGGER images_search_update AFTER UPDATE OF original_name ON images BEGIN
			INSERT INTO image_search(image_search,rowid,original_name)
			VALUES('delete',old.rowid,old.original_name);
			INSERT INTO image_search(rowid,original_name) VALUES(new.rowid,new.original_name);
		END`,
		`INSERT INTO image_search(image_search) VALUES('rebuild')`,
	)
}

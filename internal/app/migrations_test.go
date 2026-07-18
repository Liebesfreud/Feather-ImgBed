package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func rawTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "migration.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateEmptyDatabaseToLatest(t *testing.T) {
	db := rawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("数据库版本为 %d，期望 %d", version, schemaVersion)
	}
	for _, table := range []string{"users", "images", "image_variants", "tags", "image_tags", "albums", "album_images", "image_search", "usage_stats"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
	for _, index := range []string{"idx_images_storage_object", "idx_image_variants_object"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("缺少索引 %s: %v", index, err)
		}
	}
}

func TestMigratePreservesV1Data(t *testing.T) {
	db := rawTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV1(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,created_at,updated_at) VALUES(1,'admin','hash',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configs(key,value,encrypted,updated_at) VALUES('site_name','Feather',0,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at)
		VALUES('local','本地','local',1,'{}',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO images(id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,public_url,created_at) VALUES('img','sum','a.png','a.png','local','local','image/png',10,'/files/a.png',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_tokens(id,name,token_hash,last_used_at,expires_at,created_at)
		VALUES('token','PicGo','secret',NULL,NULL,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id_hash,user_id,csrf_token,expires_at,created_at)
		VALUES('session',1,'csrf',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		query string
		want  string
	}{
		{`SELECT username FROM users WHERE id=1`, "admin"},
		{`SELECT value FROM configs WHERE key='site_name'`, "Feather"},
		{`SELECT name FROM storages WHERE id='local'`, "本地"},
		{`SELECT original_name FROM images WHERE id='img'`, "a.png"},
		{`SELECT name FROM api_tokens WHERE id='token'`, "PicGo"},
		{`SELECT scopes FROM api_tokens WHERE id='token'`, `["settings:admin"]`},
		{`SELECT csrf_token FROM sessions WHERE id_hash='session'`, "csrf"},
	}
	for _, check := range checks {
		var got string
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatalf("迁移后读取数据失败 (%s): %v", check.query, err)
		}
		if got != check.want {
			t.Fatalf("迁移后数据不一致 (%s): %q != %q", check.query, got, check.want)
		}
	}
}

func TestMigrationFailureRollsBackVersionAndSchema(t *testing.T) {
	db := rawTestDB(t)
	original := migrations
	originalVersion := schemaVersion
	t.Cleanup(func() {
		migrations = original
		schemaVersion = originalVersion
	})
	migrations = []migration{
		{Version: 1, Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE should_rollback(id INTEGER)`); err != nil {
				return err
			}
			return errors.New("故意失败")
		}},
	}
	schemaVersion = 1
	err := migrate(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "故意失败") {
		t.Fatalf("期望迁移失败，得到 %v", err)
	}
	var version int
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 0 {
		t.Fatalf("失败后版本号前进到 %d", version)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='should_rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败迁移的结构未回滚: count=%d err=%v", count, err)
	}
}

func TestRejectsNewerDatabase(t *testing.T) {
	db := rawTestDB(t)
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db); err == nil || !strings.Contains(err.Error(), "高于程序支持版本") {
		t.Fatalf("未拒绝新版数据库: %v", err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addLocalStorageForTest(t *testing.T, a *App, id, root, publicURL string) {
	t.Helper()
	config, err := encryptJSON(a.masterKey, map[string]any{"data_dir": root, "public_url": publicURL})
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := a.db.Exec(`INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, id, "local", 1, config, 1, now, now); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateStorageMovesOriginalAndVariants(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	body, contentType := uploadBody(t, pngBytes(t))
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	if err := json.Unmarshal(response.Data, &uploaded); err != nil {
		t.Fatal(err)
	}
	var oldObjectKey string
	if err := a.db.QueryRow(`SELECT object_key FROM images WHERE id=?`, uploaded.ID).Scan(&oldObjectKey); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(a.cfg.DataDir, "images", filepath.FromSlash(oldObjectKey))
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("源对象不存在: %v", err)
	}
	targetRoot := filepath.Join(t.TempDir(), "target")
	addLocalStorageForTest(t, a, "target", targetRoot, "https://target.example/files")
	report, err := a.MigrateStorage(context.Background(), "local", "target", 0, true, false)
	if err != nil || report.Failed != 0 || report.Migrated != 1 {
		t.Fatalf("迁移失败: %+v %v", report, err)
	}
	var storageID, objectKey, publicURL string
	if err := a.db.QueryRow(`SELECT storage_id,object_key,public_url FROM images WHERE id=?`, uploaded.ID).Scan(&storageID, &objectKey, &publicURL); err != nil {
		t.Fatal(err)
	}
	if storageID != "target" || !strings.HasPrefix(objectKey, "migrated/") || publicURL != "https://target.example/files/"+objectKey {
		t.Fatalf("迁移后的记录错误: %s %s %s", storageID, objectKey, publicURL)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(objectKey))); err != nil {
		t.Fatalf("目标原图不存在: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("源对象未清理: %v", err)
	}
	variantRows, err := a.db.Query(`SELECT object_key FROM image_variants WHERE image_id=?`, uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer variantRows.Close()
	variantCount := 0
	for variantRows.Next() {
		var variantKey string
		if err := variantRows.Scan(&variantKey); err != nil {
			t.Fatal(err)
		}
		variantCount++
		if !strings.HasPrefix(variantKey, "migrated/") {
			t.Fatalf("派生图未迁移: %s", variantKey)
		}
		if _, err := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(variantKey))); err != nil {
			t.Fatalf("目标派生图不存在: %v", err)
		}
	}
	if err := variantRows.Err(); err != nil {
		t.Fatal(err)
	}
	if variantCount == 0 {
		t.Fatal("测试图片未生成派生图")
	}
}

func TestVerifyLocalStorageObjects(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	body, contentType := uploadBody(t, pngBytes(t))
	recorder, _ := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传失败: %d", recorder.Code)
	}
	// 显式指定本地存储时无需再额外传 include-local。
	report, err := a.VerifyStorageObjects(context.Background(), "local", 10, false)
	if err != nil || report.Failed != 0 || report.Verified == 0 {
		t.Fatalf("本地对象校验失败: %+v %v", report, err)
	}
}

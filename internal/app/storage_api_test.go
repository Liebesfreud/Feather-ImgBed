package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func s3StorageRecord(publicAddress string) StorageRecord {
	return StorageRecord{
		ID:      "r2",
		Name:    "Cloudflare R2",
		Type:    "s3",
		Enabled: true,
		Config: map[string]any{
			"endpoint":   "https://account.r2.cloudflarestorage.com",
			"bucket":     "imgbed",
			"access_key": "access",
			"secret_key": "secret",
			"public_url": publicAddress,
		},
	}
}

func TestCloudflareR2UsesImageBedProxy(t *testing.T) {
	record := s3StorageRecord("")
	if message := validateStorage(record); message != "" {
		t.Fatalf("R2 不应要求公开访问域名，得到 %q", message)
	}
	if got := publicURL(record, "variants/image/thumbnail.jpg"); got != "/s3-files/r2/variants/image/thumbnail.jpg" {
		t.Fatalf("R2 应使用图床代理地址，得到 %q", got)
	}
	record.Config["public_url"] = "https://img.example.com"
	if got := publicURL(record, "image.jpg"); got != "https://img.example.com/s3-files/r2/image.jpg" {
		t.Fatalf("R2 应使用图床的外部代理地址，得到 %q", got)
	}
	record.Config["public_url"] = "https://account.r2.cloudflarestorage.com"
	if got := publicURL(record, "image.jpg"); got != "/s3-files/r2/image.jpg" {
		t.Fatalf("误填为 R2 Endpoint 时不应生成 R2 直链，得到 %q", got)
	}
}

func TestUpdatingStorageRewritesExistingObjectURLs(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)

	record := s3StorageRecord("https://old.example.com")
	encrypted, err := encryptJSON(a.masterKey, record.Config)
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := a.db.Exec(`INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at)
		VALUES(?,?,?,?,?,1,?,?)`, record.ID, record.Name, record.Type, 1, encrypted, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO images(
		id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,public_url,created_at
	) VALUES('image','hash','photo.jpg','image.jpg','s3','r2','image/jpeg',10,'https://old.example.com/image.jpg',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO image_variants(
		id,image_id,kind,object_key,public_url,mime_type,size,width,height,created_at
	) VALUES('variant','image','thumbnail','variants/image/thumbnail.jpg','https://old.example.com/variants/image/thumbnail.jpg','image/jpeg',5,2,2,?)`, now); err != nil {
		t.Fatal(err)
	}

	record.Config["public_url"] = ""
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	recorder, _ := request(t, handler, http.MethodPut, "/api/v1/storages/r2", strings.NewReader(string(payload)), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新存储失败: %d %s", recorder.Code, recorder.Body.String())
	}

	var originalURL, thumbnailURL string
	if err := a.db.QueryRow(`SELECT public_url FROM images WHERE id='image'`).Scan(&originalURL); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT public_url FROM image_variants WHERE id='variant'`).Scan(&thumbnailURL); err != nil {
		t.Fatal(err)
	}
	if originalURL != "/s3-files/r2/image.jpg" {
		t.Fatalf("原图链接未更新: %q", originalURL)
	}
	if thumbnailURL != "/s3-files/r2/variants/image/thumbnail.jpg" {
		t.Fatalf("缩略图链接未更新: %q", thumbnailURL)
	}
}

func TestS3ProxyReadsPrivateR2Object(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	initializeTestApp(t, handler)

	record := s3StorageRecord("")
	encrypted, err := encryptJSON(a.masterKey, record.Config)
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := a.db.Exec(`INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at)
		VALUES(?,?,?,?,?,1,?,?)`, record.ID, record.Name, record.Type, 1, encrypted, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO images(
		id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,public_url,created_at
	) VALUES('image','hash','photo.jpg','image.jpg','s3','r2','image/jpeg',10,'/s3-files/r2/image.jpg',?)`, now); err != nil {
		t.Fatal(err)
	}
	content := []byte("\xff\xd8\xffprivate-r2-image")
	a.backendFactory = func(StorageRecord) (storageBackend, error) {
		return &recordingUploadStorage{openContent: content}, nil
	}

	recorder, _ := request(t, handler, http.MethodGet, "/s3-files/r2/image.jpg", nil, nil, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("R2 代理读取失败: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != string(content) {
		t.Fatalf("R2 代理响应内容错误: %q", recorder.Body.Bytes())
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("R2 代理缺少缓存响应头: %q", got)
	}
}

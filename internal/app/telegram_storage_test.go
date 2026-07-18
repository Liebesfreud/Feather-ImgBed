package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramStorageUploadOpenAndDelete(t *testing.T) {
	const (
		token     = "test-token"
		chatID    = "-100123456"
		messageID = "42"
		fileID    = "telegram/file+id"
		filePath  = "documents/file.png"
	)
	content := pngBytes(t)
	var deletedMessageID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/bot"+token+"/sendDocument":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("解析 Telegram 上传请求失败: %v", err)
			}
			if r.FormValue("chat_id") != chatID {
				t.Errorf("chat_id=%q", r.FormValue("chat_id"))
			}
			file, _, err := r.FormFile("document")
			if err != nil {
				t.Fatalf("上传请求缺少 document: %v", err)
			}
			defer file.Close()
			uploaded, _ := io.ReadAll(file)
			if !bytes.Equal(uploaded, content) {
				t.Error("Telegram 收到的文件内容不一致")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 42,
					"document":   map[string]any{"file_id": fileID},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/bot"+token+"/getFile":
			if r.URL.Query().Get("file_id") != fileID {
				t.Errorf("file_id=%q", r.URL.Query().Get("file_id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": filePath},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/file/bot"+token+"/"+filePath:
			_, _ = w.Write(content)
		case r.Method == http.MethodGet && r.URL.Path == "/bot"+token+"/getMe":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.Method == http.MethodPost && r.URL.Path == "/bot"+token+"/deleteMessage":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("解析 Telegram 删除请求失败: %v", err)
			}
			deletedMessageID = r.FormValue("message_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	storage, err := newTelegramStorage(map[string]any{
		"bot_token": token,
		"chat_id":   chatID,
		"proxy_url": server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	storage.client = server.Client()
	if err := storage.Test(context.Background()); err != nil {
		t.Fatalf("通过代理测试连接失败: %v", err)
	}

	objectKey, err := storage.Put(context.Background(), "2026/07/test.png", bytes.NewReader(content), int64(len(content)), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "v2/" + messageID + "/" + base64.RawURLEncoding.EncodeToString([]byte(fileID)) + "/2026/07/test.png"
	if objectKey != wantKey {
		t.Fatalf("object key=%q，期望 %q", objectKey, wantKey)
	}

	reader, err := storage.Open(context.Background(), objectKey)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _ := io.ReadAll(reader)
	_ = reader.Close()
	if !bytes.Equal(downloaded, content) {
		t.Error("回读内容与上传内容不一致")
	}

	if err := storage.Delete(context.Background(), objectKey); err != nil {
		t.Fatal(err)
	}
	if deletedMessageID != messageID {
		t.Fatalf("删除 message_id=%q，期望 %q", deletedMessageID, messageID)
	}
}

func TestTelegramStorageKeepsLegacyDeleteButRejectsLegacyOpen(t *testing.T) {
	storage, err := newTelegramStorage(map[string]any{
		"bot_token": "token",
		"chat_id":   "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Open(context.Background(), "42/old/path.png"); err == nil || !strings.Contains(err.Error(), "缺少 file_id") {
		t.Fatalf("旧对象回读错误=%v", err)
	}
}

func TestTelegramPublicFileRoute(t *testing.T) {
	a := newTestApp(t)
	initializeTestApp(t, a.Handler())
	config, err := encryptJSON(a.masterKey, map[string]any{"bot_token": "token", "chat_id": "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE storages SET type='telegram', config=? WHERE id='local'`, config); err != nil {
		t.Fatal(err)
	}
	const objectKey = "v2/42/ZmlsZS1pZA/2026/07/test.png"
	content := pngBytes(t)
	if _, err := a.db.Exec(`INSERT INTO images(
			id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,public_url,created_at
		) VALUES('tg-image','hash','test.png',?,'telegram','local','image/png',?,?,?)`,
		objectKey, len(content), publicURL(StorageRecord{ID: "local", Type: "telegram"}, objectKey, ""), nowUTC()); err != nil {
		t.Fatal(err)
	}
	storage := &recordingUploadStorage{openContent: content}
	a.backendFactory = func(record StorageRecord) (storageBackend, error) {
		if record.ID != "local" || record.Type != "telegram" {
			t.Fatalf("读取了错误的存储: %+v", record)
		}
		return storage, nil
	}

	recorder, _ := request(t, a.Handler(), http.MethodGet, "/tg-files/local/"+objectKey, nil, nil, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("公开读取状态=%d，响应=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), content) {
		t.Error("公开读取内容不一致")
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type=%q", got)
	}
}

func TestTelegramConfigDoesNotRequireProxy(t *testing.T) {
	record := StorageRecord{
		ID:      "telegram",
		Name:    "Telegram",
		Type:    "telegram",
		Enabled: true,
		Config:  map[string]any{"bot_token": "token", "chat_id": "-100123"},
	}
	if message := validateStorage(record); message != "" {
		t.Fatalf("未配置代理时校验失败: %s", message)
	}
	if got := publicURL(record, "v2/42/ZmlsZS1pZA/test.png", ""); got != "/tg-files/telegram/v2/42/ZmlsZS1pZA/test.png" {
		t.Fatalf("公开地址=%q", got)
	}
}

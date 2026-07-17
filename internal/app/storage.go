package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type storageBackend interface {
	Put(context.Context, string, io.Reader, int64, string) (string, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	Test(context.Context) error
}

type backendFactory func(StorageRecord) (storageBackend, error)

func encryptJSON(key []byte, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return encrypt(key, data)
}
func decryptJSON(key []byte, value string, dst any) error {
	data, err := decrypt(key, value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func (a *App) storageRecord(ctx context.Context, id string) (StorageRecord, error) {
	var record StorageRecord
	var enabled int
	var config string
	err := a.db.QueryRowContext(ctx, "SELECT id,name,type,enabled,config,created_at,updated_at FROM storages WHERE id=?", id).Scan(&record.ID, &record.Name, &record.Type, &enabled, &config, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return record, err
	}
	record.Enabled = enabled == 1
	record.Config = map[string]any{}
	if err := decryptJSON(a.masterKey, config, &record.Config); err != nil {
		return record, err
	}
	return record, nil
}

func (a *App) backend(record StorageRecord) (storageBackend, error) {
	if a.backendFactory == nil {
		return a.defaultBackend(record)
	}
	return a.backendFactory(record)
}

func (a *App) defaultBackend(record StorageRecord) (storageBackend, error) {
	switch record.Type {
	case "local":
		dir := stringValue(record.Config, "data_dir")
		if dir == "" {
			dir = "images"
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(a.cfg.DataDir, dir)
		}
		return &localStorage{root: dir}, nil
	case "s3":
		return newS3Storage(record.Config)
	case "webdav":
		return newWebDAVStorage(record.Config)
	case "telegram":
		return newTelegramStorage(record.Config)
	default:
		return nil, fmt.Errorf("不支持的存储类型 %q", record.Type)
	}
}

func stringValue(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}
func boolValue(config map[string]any, key string) bool { value, _ := config[key].(bool); return value }

func publicURL(record StorageRecord, objectKey string) string {
	base := strings.TrimRight(stringValue(record.Config, "public_url"), "/")
	if base == "" {
		return "/files/" + strings.TrimLeft(objectKey, "/")
	}
	return base + "/" + strings.TrimLeft(objectKey, "/")
}

type localStorage struct{ root string }

func (s *localStorage) safe(key string) (string, error) {
	clean := path.Clean("/" + key)
	if clean == "/" || strings.Contains(key, "\\") {
		return "", errors.New("对象路径无效")
	}
	target := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	rel, err := filepath.Rel(s.root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("对象路径越界")
	}
	return target, nil
}
func (s *localStorage) Put(ctx context.Context, key string, reader io.Reader, _ int64, _ string) (string, error) {
	target, err := s.safe(key)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	select {
	case <-ctx.Done():
		_ = tmp.Close()
		return "", ctx.Err()
	default:
	}
	_, err = io.Copy(tmp, reader)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		default:
			err = os.Rename(name, target)
		}
	}
	return key, err
}
func (s *localStorage) Delete(_ context.Context, key string) error {
	target, err := s.safe(key)
	if err != nil {
		return err
	}
	err = os.Remove(target)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *localStorage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	target, err := s.safe(key)
	if err != nil {
		return nil, err
	}
	return os.Open(target)
}
func (s *localStorage) Test(_ context.Context) error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.root, ".test-*")
	if err != nil {
		return err
	}
	name := file.Name()
	_ = file.Close()
	return os.Remove(name)
}

type s3Storage struct {
	client *minio.Client
	bucket string
	region string
}

func newS3Storage(config map[string]any) (*s3Storage, error) {
	raw := stringValue(config, "endpoint")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("S3 Endpoint 必须是有效的 HTTP 或 HTTPS 地址")
	}
	client, err := minio.New(parsed.Host+parsed.Path, &minio.Options{Creds: credentials.NewStaticV4(stringValue(config, "access_key"), stringValue(config, "secret_key"), ""), Secure: parsed.Scheme == "https", Region: stringValue(config, "region"), BucketLookup: func() minio.BucketLookupType {
		if boolValue(config, "path_style") {
			return minio.BucketLookupPath
		}
		return minio.BucketLookupAuto
	}()})
	if err != nil {
		return nil, err
	}
	bucket := stringValue(config, "bucket")
	if bucket == "" {
		return nil, errors.New("S3 Bucket 不能为空")
	}
	return &s3Storage{client: client, bucket: bucket, region: stringValue(config, "region")}, nil
}
func (s *s3Storage) Put(ctx context.Context, key string, r io.Reader, size int64, mime string) (string, error) {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: mime})
	return key, err
}
func (s *s3Storage) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
func (s *s3Storage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		return nil, err
	}
	return object, nil
}
func (s *s3Storage) Test(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err == nil && !exists {
		return errors.New("S3 Bucket 不存在")
	}
	return err
}

type webDAVStorage struct {
	base, user, password, dir string
	client                    *http.Client
}

func newWebDAVStorage(c map[string]any) (*webDAVStorage, error) {
	base := strings.TrimRight(stringValue(c, "url"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("WebDAV 地址必须是有效的 HTTP 或 HTTPS 地址")
	}
	return &webDAVStorage{base: base, user: stringValue(c, "username"), password: stringValue(c, "password"), dir: strings.Trim(stringValue(c, "target_dir"), "/"), client: &http.Client{Timeout: 30 * time.Second}}, nil
}
func (s *webDAVStorage) request(ctx context.Context, method, key string, body io.Reader) (*http.Response, error) {
	target := s.base + "/" + path.Join(s.dir, key)
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.user, s.password)
	return s.client.Do(req)
}
func (s *webDAVStorage) ensureDirs(ctx context.Context, key string) error {
	parts := strings.Split(path.Dir(key), "/")
	current := ""
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = path.Join(current, part)
		resp, err := s.request(ctx, "MKCOL", current, nil)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 405 && resp.StatusCode != 301 {
			return fmt.Errorf("WebDAV 创建目录失败: HTTP %d", resp.StatusCode)
		}
	}
	return nil
}
func (s *webDAVStorage) Put(ctx context.Context, key string, r io.Reader, _ int64, mime string) (string, error) {
	if err := s.ensureDirs(ctx, key); err != nil {
		return "", err
	}
	resp, err := s.request(ctx, http.MethodPut, key, r)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("WebDAV 上传失败: HTTP %d", resp.StatusCode)
	}
	return key, nil
}
func (s *webDAVStorage) Delete(ctx context.Context, key string) error {
	resp, err := s.request(ctx, http.MethodDelete, key, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return fmt.Errorf("WebDAV 删除失败: HTTP %d", resp.StatusCode)
	}
	return nil
}
func (s *webDAVStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.request(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("WebDAV 下载失败: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}
func (s *webDAVStorage) Test(ctx context.Context) error {
	resp, err := s.request(ctx, "PROPFIND", "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 && resp.StatusCode != 200 {
		return fmt.Errorf("WebDAV 连接失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

type telegramStorage struct {
	token, chatID, publicURL string
	client                   *http.Client
}

func newTelegramStorage(c map[string]any) (*telegramStorage, error) {
	s := &telegramStorage{token: stringValue(c, "bot_token"), chatID: stringValue(c, "chat_id"), publicURL: stringValue(c, "public_url"), client: &http.Client{Timeout: 60 * time.Second}}
	if s.token == "" || s.chatID == "" || s.publicURL == "" {
		return nil, errors.New("Telegram Bot Token、Chat ID 和公开代理地址不能为空")
	}
	return s, nil
}
func (s *telegramStorage) api(method string) string {
	return "https://api.telegram.org/bot" + s.token + "/" + method
}
func (s *telegramStorage) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) (string, error) {
	body, pipe := io.Pipe()
	writer := multipart.NewWriter(pipe)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.api("sendDocument"), body)
	if err != nil {
		_ = body.Close()
		_ = pipe.Close()
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	go streamTelegramUpload(pipe, writer, s.chatID, key, r)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil || !result.OK {
		return "", fmt.Errorf("Telegram 上传失败: %s", result.Description)
	}
	return strconv.Itoa(result.Result.MessageID) + "/" + key, nil
}

func streamTelegramUpload(pipe *io.PipeWriter, writer *multipart.Writer, chatID, key string, source io.Reader) {
	err := writer.WriteField("chat_id", chatID)
	if err == nil {
		err = writer.WriteField("caption", key)
	}
	var part io.Writer
	if err == nil {
		part, err = writer.CreateFormFile("document", path.Base(key))
	}
	if err == nil {
		_, err = io.Copy(part, source)
	}
	if err == nil {
		err = writer.Close()
	}
	_ = pipe.CloseWithError(err)
}
func (s *telegramStorage) Delete(ctx context.Context, key string) error {
	messageID := strings.SplitN(key, "/", 2)[0]
	form := url.Values{"chat_id": {s.chatID}, "message_id": {messageID}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.api("deleteMessage"), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Telegram 删除失败: HTTP %d", resp.StatusCode)
	}
	return nil
}
func (s *telegramStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("旧 Telegram 对象不支持回读")
}
func (s *telegramStorage) Test(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.api("getMe"), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Telegram 连接失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

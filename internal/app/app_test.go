package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *apiError       `json:"error"`
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Config{DataDir: dir, MasterKeyFile: dir + "/master.key", Version: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func request(t *testing.T, handler http.Handler, method, target string, body io.Reader, cookie *http.Cookie, csrf, bearer, contentType string) (*httptest.ResponseRecorder, testResponse) {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	var response testResponse
	if strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("响应不是有效 JSON: %v\n%s", err, recorder.Body.String())
		}
	}
	return recorder, response
}

func initializeTestApp(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	body := strings.NewReader(`{"username":"admin","password":"very-secure-password","site_url":"http://img.test"}`)
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/auth/initialize", body, nil, "", "", "application/json")
	if recorder.Code != http.StatusOK || !response.Success {
		t.Fatalf("初始化失败: %d %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("初始化未返回会话 Cookie")
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(response.Data, &data)
	return cookies[0], data.CSRFToken
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func uploadBody(t *testing.T, content []byte) (*bytes.Buffer, string) {
	return uploadBodyToStorage(t, content, "")
}

func uploadBodyToStorage(t *testing.T, content []byte, storageID string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if storageID != "" {
		if err := writer.WriteField("storage_id", storageID); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = writer.Close()
	return &body, writer.FormDataContentType()
}

func TestAuthenticationUploadListDeleteFlow(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()

	recorder, _ := request(t, handler, http.MethodGet, "/api/v1/images", nil, nil, "", "", "")
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("未初始化时应拒绝管理 API，得到 %d", recorder.Code)
	}
	cookie, csrf := initializeTestApp(t, handler)
	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/auth/logout", nil, cookie, csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("退出失败: %d", recorder.Code)
	}
	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`), nil, "", "", "application/json")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码应被拒绝，得到 %d", recorder.Code)
	}
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"very-secure-password"}`), nil, "", "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("重新登录失败: %s", recorder.Body.String())
	}
	cookie = recorder.Result().Cookies()[0]
	var sessionData struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(response.Data, &sessionData)
	csrf = sessionData.CSRFToken

	body, contentType := uploadBody(t, pngBytes(t))
	recorder, response = request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated || !response.Success {
		t.Fatalf("上传失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	_ = json.Unmarshal(response.Data, &uploaded)
	if uploaded.Width != 2 || uploaded.Height != 3 || uploaded.MIMEType != "image/png" {
		t.Fatalf("图片元数据错误: %+v", uploaded)
	}

	recorder, response = request(t, handler, http.MethodGet, "/api/v1/images?limit=10", nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK || !bytes.Contains(response.Data, []byte(uploaded.ID)) {
		t.Fatalf("列表中没有上传图片: %s", recorder.Body.String())
	}

	recorder, _ = request(t, handler, http.MethodDelete, "/api/v1/images/"+uploaded.ID, nil, cookie, "wrong", "", "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("错误 CSRF 应被拒绝，得到 %d", recorder.Code)
	}
	recorder, response = request(t, handler, http.MethodDelete, "/api/v1/images/"+uploaded.ID, nil, cookie, csrf, "", "")
	if recorder.Code != http.StatusOK || !response.Success {
		t.Fatalf("删除失败: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestTokenAuthenticationAndSecretMasking(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)

	recorder, response := request(t, handler, http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"PicGo"}`), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("创建 Token 失败: %s", recorder.Body.String())
	}
	var tokenData struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(response.Data, &tokenData)
	recorder, _ = request(t, handler, http.MethodGet, "/api/v1/settings", nil, nil, "", tokenData.Token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("Bearer Token 鉴权失败: %d", recorder.Code)
	}

	storage := `{"name":"对象存储","type":"s3","enabled":true,"config":{"endpoint":"https://s3.example.com","bucket":"images","access_key":"access-secret","secret_key":"top-secret","public_url":"https://img.example.com"}}`
	recorder, _ = request(t, handler, http.MethodPut, "/api/v1/storages/remote", strings.NewReader(storage), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("保存存储配置失败: %s", recorder.Body.String())
	}
	recorder, _ = request(t, handler, http.MethodGet, "/api/v1/storages", nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK || bytes.Contains(recorder.Body.Bytes(), []byte("top-secret")) || bytes.Contains(recorder.Body.Bytes(), []byte("access-secret")) {
		t.Fatalf("敏感字段发生回显: %s", recorder.Body.String())
	}
}

func TestRejectsInvalidImageAndDeduplicates(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)

	body, contentType := uploadBody(t, []byte("not an image"))
	recorder, _ := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非图片应被拒绝，得到 %d", recorder.Code)
	}
	content := pngBytes(t)
	body, contentType = uploadBody(t, content)
	_, first := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	body, contentType = uploadBody(t, content)
	_, second := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	var one, two Image
	_ = json.Unmarshal(first.Data, &one)
	_ = json.Unmarshal(second.Data, &two)
	if one.ID == "" || one.ID != two.ID {
		t.Fatalf("重复图片没有返回已有记录: %q != %q", one.ID, two.ID)
	}
}

func TestPublicFilesDoNotExposeDirectoryListing(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	recorder, _ := request(t, handler, http.MethodGet, "/files/", nil, nil, "", "", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("本地文件根目录不应允许列目录，得到 %d", recorder.Code)
	}
}

func TestRandomImageAPI(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()

	recorder, response := request(t, handler, http.MethodGet, "/api/v1/random", nil, nil, "", "", "")
	if recorder.Code != http.StatusNotFound || response.Error == nil || response.Error.Code != "RANDOM_IMAGE_NOT_FOUND" {
		t.Fatalf("空图库随机图响应错误: %d %s", recorder.Code, recorder.Body.String())
	}

	cookie, csrf := initializeTestApp(t, handler)
	body, contentType := uploadBody(t, pngBytes(t))
	recorder, response = request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传随机图测试图片失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	_ = json.Unmarshal(response.Data, &uploaded)

	// 随机图接口是公开接口，可直接作为 img 的 src 使用。
	recorder, _ = request(t, handler, http.MethodGet, "/api/v1/random", nil, nil, "", "", "")
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != uploaded.PublicURL {
		t.Fatalf("随机图跳转错误: %d Location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("随机图响应不应被缓存: %q", recorder.Header().Get("Cache-Control"))
	}

	recorder, response = request(t, handler, http.MethodGet, "/api/v1/random?format=json&storage_id=local", nil, nil, "", "", "")
	if recorder.Code != http.StatusOK || !response.Success {
		t.Fatalf("随机图 JSON 响应错误: %d %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		ID       string `json:"id"`
		URL      string `json:"url"`
		MIMEType string `json:"mime_type"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	_ = json.Unmarshal(response.Data, &result)
	if result.ID != uploaded.ID || result.URL != uploaded.PublicURL || result.MIMEType != "image/png" || result.Width != 2 || result.Height != 3 {
		t.Fatalf("随机图 JSON 数据错误: %+v", result)
	}

	recorder, response = request(t, handler, http.MethodGet, "/api/v1/random?storage_id=missing", nil, nil, "", "", "")
	if recorder.Code != http.StatusNotFound || response.Error == nil || response.Error.Code != "RANDOM_IMAGE_NOT_FOUND" {
		t.Fatalf("随机图存储筛选错误: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, response = request(t, handler, http.MethodGet, "/api/v1/random?format=xml", nil, nil, "", "", "")
	if recorder.Code != http.StatusBadRequest || response.Error == nil || response.Error.Code != "INVALID_FORMAT" {
		t.Fatalf("随机图非法格式未被拒绝: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestChangePasswordRejectsInvalidAndOversizedInput(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)

	recorder, response := request(t, handler, http.MethodPut, "/api/v1/auth/password", strings.NewReader("{"), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest || response.Error == nil || response.Error.Code != "INVALID_JSON" {
		t.Fatalf("非法 JSON 响应错误: %d %s", recorder.Code, recorder.Body.String())
	}

	payload, _ := json.Marshal(map[string]string{"current_password": "very-secure-password", "new_password": strings.Repeat("x", bcryptMaxPasswordBytes+1)})
	recorder, response = request(t, handler, http.MethodPut, "/api/v1/auth/password", bytes.NewReader(payload), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest || response.Error == nil || response.Error.Code != "WEAK_PASSWORD" {
		t.Fatalf("超长密码未被拒绝: %d %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"very-secure-password"}`), nil, "", "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("拒绝超长密码后原密码应仍可登录: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDecodeJSONRejectsTrailingValue(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"one"}{"name":"two"}`), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest || response.Error == nil || response.Error.Code != "INVALID_JSON" {
		t.Fatalf("多余 JSON 值未被拒绝: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCustomLocalStorageFilesAreServed(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	customDir := filepath.Join(a.cfg.DataDir, "custom-images")
	payload, _ := json.Marshal(map[string]any{
		"name": "自定义本地存储", "type": "local", "enabled": true,
		"config": map[string]any{"data_dir": customDir},
	})
	recorder, _ := request(t, handler, http.MethodPut, "/api/v1/storages/custom", bytes.NewReader(payload), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("创建自定义本地存储失败: %d %s", recorder.Code, recorder.Body.String())
	}
	body, contentType := uploadBodyToStorage(t, pngBytes(t), "custom")
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传到自定义本地存储失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	_ = json.Unmarshal(response.Data, &uploaded)
	recorder, _ = request(t, handler, http.MethodGet, uploaded.PublicURL, nil, nil, "", "", "")
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), pngBytes(t)) {
		t.Fatalf("自定义本地存储文件不可访问: %d", recorder.Code)
	}
}

type rollbackStorage struct {
	deleteContextError error
}

func (s *rollbackStorage) Put(context.Context, string, io.Reader, int64, string) (string, error) {
	return "stored.png", nil
}
func (s *rollbackStorage) Delete(ctx context.Context, _ string) error {
	s.deleteContextError = ctx.Err()
	return nil
}
func (s *rollbackStorage) Test(context.Context) error { return nil }

func TestUploadRollbackUsesIndependentContext(t *testing.T) {
	a := newTestApp(t)
	body, contentType := uploadBody(t, pngBytes(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/images", body)
	req.Header.Set("Content-Type", contentType)
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	backend := &rollbackStorage{}
	_, apiErr := a.uploadOne(req, defaultSettings(), StorageRecord{ID: "local", Type: "local"}, backend, req.MultipartForm.File["file"][0])
	if apiErr == nil || apiErr.Code != "DATABASE_ERROR" {
		t.Fatalf("预期元数据保存失败，得到 %+v", apiErr)
	}
	if backend.deleteContextError != nil {
		t.Fatalf("回滚上下文不应继承已取消的请求: %v", backend.deleteContextError)
	}
}

func TestTrustedProxyClientIPAndLimiterCleanup(t *testing.T) {
	a := &App{trustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.1")
	if got := a.clientIP(req); got != "198.51.100.8" {
		t.Fatalf("可信代理客户端 IP 错误: %s", got)
	}
	req.RemoteAddr = "203.0.113.2:1234"
	if got := a.clientIP(req); got != "203.0.113.2" {
		t.Fatalf("不可信来源不应采用转发头: %s", got)
	}

	limiter := newRateLimiter()
	limiter.entries["expired"] = rateEntry{count: 1, reset: time.Now().Add(-time.Minute)}
	limiter.lastCleanup = time.Now().Add(-2 * time.Minute)
	if !limiter.allow("active", 1, time.Minute) {
		t.Fatal("新限流条目应被允许")
	}
	if _, exists := limiter.entries["expired"]; exists {
		t.Fatal("过期限流条目未清理")
	}
}

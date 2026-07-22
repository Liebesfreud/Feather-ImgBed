package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestEmbeddedFrontendContainsReferencedChunks(t *testing.T) {
	index, err := fs.ReadFile(frontendAssets, "web/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	entryMatch := regexp.MustCompile(`src="/(assets/[^"]+\.js)"`).FindSubmatch(index)
	if len(entryMatch) != 2 {
		t.Fatalf("未找到入口脚本: %s", index)
	}
	entry, err := fs.ReadFile(frontendAssets, "web/dist/"+string(entryMatch[1]))
	if err != nil {
		t.Fatal(err)
	}
	references := regexp.MustCompile(`["'](assets/[^"']+)["']`).FindAllSubmatch(entry, -1)
	if len(references) == 0 {
		t.Fatal("入口脚本未包含动态资源引用")
	}
	for _, reference := range references {
		name := "web/dist/" + string(reference[1])
		if _, err := fs.Stat(frontendAssets, name); err != nil {
			t.Errorf("入口脚本引用的资源未被嵌入: %s: %v", name, err)
		}
	}
}

func TestAuthStatusIncludesUploadBootstrapForBrowserSession(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()

	recorder, response := request(t, handler, http.MethodGet, "/api/v1/auth/status", nil, nil, "", "", "")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("未初始化状态响应错误: %d cache=%q", recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	var anonymous struct {
		Initialized   bool `json:"initialized"`
		Authenticated bool `json:"authenticated"`
		Upload        any  `json:"upload"`
	}
	if err := json.Unmarshal(response.Data, &anonymous); err != nil {
		t.Fatal(err)
	}
	if anonymous.Initialized || anonymous.Authenticated || anonymous.Upload != nil {
		t.Fatalf("匿名启动状态不应包含上传数据: %+v", anonymous)
	}

	cookie, csrf := initializeTestApp(t, handler)
	recorder, response = request(t, handler, http.MethodGet, "/api/v1/auth/status", nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("认证状态响应错误: %d cache=%q", recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	var authenticated struct {
		Initialized   bool   `json:"initialized"`
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
		Upload        *struct {
			Storages   []StorageRecord  `json:"storages"`
			Settings   Settings         `json:"settings"`
			Statistics uploadStatistics `json:"statistics"`
		} `json:"upload"`
	}
	if err := json.Unmarshal(response.Data, &authenticated); err != nil {
		t.Fatal(err)
	}
	if !authenticated.Initialized || !authenticated.Authenticated || authenticated.CSRFToken != csrf || authenticated.Upload == nil {
		t.Fatalf("认证启动数据不完整: %+v", authenticated)
	}
	if len(authenticated.Upload.Storages) != 1 || authenticated.Upload.Storages[0].ID != "local" {
		t.Fatalf("启动存储数据错误: %+v", authenticated.Upload.Storages)
	}
	if authenticated.Upload.Settings.DefaultStorageID != "local" || authenticated.Upload.Statistics.ImageCount != 0 {
		t.Fatalf("启动配置或统计错误: %+v", authenticated.Upload)
	}
}

func TestFrontendCompressionCachingAndValidators(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()

	serve := func(method, target string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	index := serve(http.MethodGet, "/", nil)
	if index.Code != http.StatusOK || !strings.Contains(index.Header().Get("Cache-Control"), "must-revalidate") {
		t.Fatalf("HTML 缓存策略错误: %d %q", index.Code, index.Header().Get("Cache-Control"))
	}
	if index.Header().Get("ETag") == "" || index.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("HTML 缺少缓存校验头: %#v", index.Header())
	}
	indexNotModified := serve(http.MethodGet, "/", map[string]string{"If-None-Match": index.Header().Get("ETag")})
	if indexNotModified.Code != http.StatusNotModified || indexNotModified.Body.Len() != 0 {
		t.Fatalf("HTML 条件请求未返回 304: %d body=%d", indexNotModified.Code, indexNotModified.Body.Len())
	}

	match := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(index.Body.String())
	if len(match) != 2 {
		t.Fatalf("未找到入口脚本: %s", index.Body.String())
	}
	assetPath := match[1]
	identity := serve(http.MethodGet, assetPath, nil)
	if identity.Code != http.StatusOK || !strings.Contains(identity.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("静态资源缓存策略错误: %d %q", identity.Code, identity.Header().Get("Cache-Control"))
	}
	if identity.Header().Get("Content-Encoding") != "" || identity.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("原始资源协商头错误: %#v", identity.Header())
	}

	brotli := serve(http.MethodGet, assetPath, map[string]string{"Accept-Encoding": "gzip, br"})
	if brotli.Code != http.StatusOK || brotli.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("未优先返回 Brotli: %d encoding=%q", brotli.Code, brotli.Header().Get("Content-Encoding"))
	}
	if brotli.Body.Len() >= identity.Body.Len() || brotli.Header().Get("ETag") == identity.Header().Get("ETag") {
		t.Fatalf("Brotli 表示无效: br=%d identity=%d", brotli.Body.Len(), identity.Body.Len())
	}
	if length, err := strconv.Atoi(brotli.Header().Get("Content-Length")); err != nil || length != brotli.Body.Len() {
		t.Fatalf("Brotli Content-Length 错误: %q body=%d", brotli.Header().Get("Content-Length"), brotli.Body.Len())
	}

	gzipped := serve(http.MethodGet, assetPath, map[string]string{"Accept-Encoding": "br;q=0, gzip;q=1"})
	if gzipped.Code != http.StatusOK || gzipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip 协商失败: %d encoding=%q", gzipped.Code, gzipped.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(bytes.NewReader(gzipped.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(decoded, identity.Body.Bytes()) {
		t.Fatal("gzip 解压结果与原始资源不一致")
	}

	notModified := serve(http.MethodGet, assetPath, map[string]string{
		"Accept-Encoding": "br",
		"If-None-Match":   brotli.Header().Get("ETag"),
	})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("压缩资源条件请求未返回 304: %d body=%d", notModified.Code, notModified.Body.Len())
	}
	head := serve(http.MethodHead, assetPath, map[string]string{"Accept-Encoding": "br"})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD 响应错误: %d body=%d length=%q", head.Code, head.Body.Len(), head.Header().Get("Content-Length"))
	}

	appRoute := serve(http.MethodGet, "/gallery", nil)
	if appRoute.Code != http.StatusOK || !strings.Contains(appRoute.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("前端路由未回退到应用壳: %d %q", appRoute.Code, appRoute.Body.String())
	}
	missingAsset := serve(http.MethodGet, "/assets/missing.js", nil)
	if missingAsset.Code != http.StatusNotFound || strings.Contains(missingAsset.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("缺失静态资源不应回退到应用壳: %d %q", missingAsset.Code, missingAsset.Body.String())
	}
}

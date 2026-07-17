package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type fakeImportResolver struct {
	addresses map[string][]netip.Addr
	calls     []string
}

func (r *fakeImportResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	r.calls = append(r.calls, host)
	addresses, exists := r.addresses[host]
	if !exists {
		return nil, errors.New("host not found")
	}
	return addresses, nil
}

func TestImportURLValidationBlocksInternalDestinations(t *testing.T) {
	resolver := &fakeImportResolver{}
	blocked := []string{
		"file:///etc/passwd",
		"ftp://example.com/image.png",
		"http://user:secret@example.com/image.png",
		"http://127.0.0.1/image.png",
		"http://10.0.0.1/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/image.png",
		"http://[fe80::1]/image.png",
		"http://224.0.0.1/image.png",
		"http://0.0.0.0/image.png",
		"http://100.64.0.1/image.png",
	}
	for _, raw := range blocked {
		target, _ := url.Parse(raw)
		if _, err := validateAndResolveImportURL(t.Context(), target, resolver); !errors.Is(err, errUnsafeImportURL) {
			t.Errorf("%s 应被 SSRF 防护拒绝，得到 %v", raw, err)
		}
	}
	target, _ := url.Parse("https://93.184.216.34/image.png")
	addresses, err := validateAndResolveImportURL(t.Context(), target, resolver)
	if err != nil || len(addresses) != 1 || addresses[0].String() != "93.184.216.34" {
		t.Fatalf("公开地址应通过校验: addresses=%v err=%v", addresses, err)
	}
}

func TestImportURLRevalidatesEveryRedirect(t *testing.T) {
	resolver := &fakeImportResolver{addresses: map[string][]netip.Addr{
		"public.test":   {netip.MustParseAddr("93.184.216.34")},
		"internal.test": {netip.MustParseAddr("10.0.0.8")},
	}}
	hops := 0
	fetcher := safeURLFetcher{
		resolver: resolver,
		doHop: func(_ context.Context, target *url.URL, _ []netip.Addr) (*http.Response, func(), error) {
			hops++
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://internal.test/secret.png"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    &http.Request{URL: target},
			}, func() {}, nil
		},
	}
	if _, _, err := fetcher.Fetch(t.Context(), "https://public.test/image.png"); !errors.Is(err, errUnsafeImportURL) {
		t.Fatalf("重定向到私网应被拒绝: %v", err)
	}
	if hops != 1 {
		t.Fatalf("不应连接重定向后的私网地址，实际发起 %d 次请求", hops)
	}
	if strings.Join(resolver.calls, ",") != "public.test,internal.test" {
		t.Fatalf("每次重定向必须重新解析: %+v", resolver.calls)
	}
}

func TestImportURLLimitsRedirects(t *testing.T) {
	resolver := &fakeImportResolver{addresses: map[string][]netip.Addr{
		"public.test": {netip.MustParseAddr("93.184.216.34")},
	}}
	hops := 0
	fetcher := safeURLFetcher{
		resolver: resolver,
		doHop: func(_ context.Context, target *url.URL, _ []netip.Addr) (*http.Response, func(), error) {
			hops++
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/again"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    &http.Request{URL: target},
			}, func() {}, nil
		},
	}
	if _, _, err := fetcher.Fetch(t.Context(), "https://public.test/start"); !errors.Is(err, errTooManyRedirects) {
		t.Fatalf("超过三次重定向应被拒绝: %v", err)
	}
	if hops != maxImportRedirects+1 {
		t.Fatalf("重定向请求次数错误: %d", hops)
	}
}

func TestPinnedImportDialerUsesOnlyValidatedIP(t *testing.T) {
	var dialed string
	expectedErr := errors.New("stop")
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		return nil, expectedErr
	}
	pinned := pinnedImportDialer("cdn.example", "443", []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
	}, dial)
	if _, err := pinned(t.Context(), "tcp", "cdn.example:443"); !errors.Is(err, expectedErr) {
		t.Fatalf("固定 IP 拨号错误: %v", err)
	}
	if dialed != "93.184.216.34:443" {
		t.Fatalf("拨号未固定到已验证 IP: %q", dialed)
	}
	if _, err := pinned(t.Context(), "tcp", "changed.example:443"); !errors.Is(err, errUnsafeImportURL) {
		t.Fatalf("传输层目标变化应被拒绝: %v", err)
	}
}

type staticImportFetcher struct {
	response *http.Response
	err      error
	cleaned  *bool
}

func (f staticImportFetcher) Fetch(context.Context, string) (*http.Response, func(), error) {
	return f.response, func() {
		if f.cleaned != nil {
			*f.cleaned = true
		}
	}, f.err
}

func TestImportURLHandlerUsesCommonIngestPipeline(t *testing.T) {
	a := newTestApp(t)
	baseHandler := a.Handler()
	cookie, csrf := initializeTestApp(t, baseHandler)
	cleaned := false
	target, _ := url.Parse("https://cdn.example/photos/remote.png")
	fetcher := staticImportFetcher{
		response: &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader(string(pngBytes(t)))),
			ContentLength: int64(len(pngBytes(t))),
			Request:       &http.Request{URL: target},
		},
		cleaned: &cleaned,
	}
	a.mux = http.NewServeMux()
	a.mux.Handle("POST /api/v1/images/import-url", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.importImageURLWithFetcher(w, r, fetcher)
	})))
	handler := a.Handler()
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images/import-url",
		strings.NewReader(`{"url":"https://cdn.example/photos/remote.png"}`),
		cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("URL 导入失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var image Image
	if err := json.Unmarshal(response.Data, &image); err != nil {
		t.Fatal(err)
	}
	if image.OriginalName != "remote.png" || image.Width != 2 || image.Height != 3 {
		t.Fatalf("URL 导入未经过通用接收流水线: %+v", image)
	}
	if !cleaned {
		t.Fatal("URL 导入完成后未清理 HTTP 传输")
	}
}

func TestImportURLHandlerMasksUnsafeAddressDetails(t *testing.T) {
	a := newTestApp(t)
	baseHandler := a.Handler()
	cookie, csrf := initializeTestApp(t, baseHandler)
	a.mux = http.NewServeMux()
	a.mux.Handle("POST /api/v1/images/import-url", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.importImageURLWithFetcher(w, r, staticImportFetcher{err: fmt.Errorf("%w: 10.0.0.1", errUnsafeImportURL)})
	})))
	recorder, response := request(t, a.Handler(), http.MethodPost, "/api/v1/images/import-url",
		strings.NewReader(`{"url":"http://internal.test/image.png"}`),
		cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest || response.Error == nil || response.Error.Code != "UNSAFE_IMPORT_URL" {
		t.Fatalf("不安全 URL 响应错误: %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.1") {
		t.Fatalf("响应泄露内部地址细节: %s", recorder.Body.String())
	}
}

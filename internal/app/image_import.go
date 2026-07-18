package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const maxImportRedirects = 3

var (
	errUnsafeImportURL    = errors.New("导入地址不安全")
	errTooManyRedirects   = errors.New("导入地址重定向次数过多")
	errImportHTTPStatus   = errors.New("远程服务器响应状态异常")
	forbiddenImportRanges = mustPrefixes(
		"0.0.0.0/8",
		"100.64.0.0/10",
		"192.0.0.0/24",
		"198.18.0.0/15",
		"240.0.0.0/4",
		"::/128",
	)
)

type importResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type importFetcher interface {
	Fetch(context.Context, string) (*http.Response, func(), error)
}

type safeURLFetcher struct {
	resolver importResolver
	doHop    func(context.Context, *url.URL, []netip.Addr) (*http.Response, func(), error)
}

func newSafeURLFetcher() safeURLFetcher {
	return safeURLFetcher{resolver: net.DefaultResolver, doHop: performSafeImportHop}
}

func (a *App) registerImportRoutes() {
	a.mux.Handle("POST /api/v1/images/import-url",
		a.protect(tokenScopeUpload, a.limitHandler("import-url", 15, time.Minute, http.HandlerFunc(a.importImageURL))))
}

func (a *App) importImageURL(w http.ResponseWriter, r *http.Request) {
	a.importImageURLWithFetcher(w, r, newSafeURLFetcher())
}

func (a *App) importImageURLWithFetcher(w http.ResponseWriter, r *http.Request, fetcher importFetcher) {
	var input struct {
		URL       string `json:"url"`
		StorageID string `json:"storage_id"`
		Filename  string `json:"filename"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.URL = strings.TrimSpace(input.URL)
	input.StorageID = strings.TrimSpace(input.StorageID)
	input.Filename = strings.TrimSpace(input.Filename)
	if input.URL == "" {
		writeError(w, r, http.StatusBadRequest, "URL_REQUIRED", "必须提供图片 URL")
		return
	}
	if len(input.Filename) > 255 {
		writeError(w, r, http.StatusBadRequest, "INVALID_FILENAME", "文件名最多 255 个字符")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	response, cleanup, err := fetcher.Fetch(ctx, input.URL)
	if err != nil {
		status, code, message := http.StatusBadGateway, "IMPORT_DOWNLOAD_FAILED", "下载远程图片失败"
		if errors.Is(err, errUnsafeImportURL) || errors.Is(err, errTooManyRedirects) {
			status, code, message = http.StatusBadRequest, "UNSAFE_IMPORT_URL", "图片 URL 不安全或重定向次数过多"
		}
		a.logger.Warn("URL 图片下载失败", "request_id", requestID(r), "error", err)
		writeError(w, r, status, code, message)
		return
	}
	defer cleanup()
	defer response.Body.Close()
	filename := input.Filename
	if filename == "" {
		filename = filenameFromImportURL(response.Request.URL)
	}
	image, err := a.ingestImage(ctx, response.Body, filename, response.ContentLength, input.StorageID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			writeError(w, r, http.StatusBadRequest, apiErr.Code, apiErr.Message)
		} else {
			a.logger.Error("URL 图片导入失败", "request_id", requestID(r), "error", err)
			writeError(w, r, http.StatusInternalServerError, "IMPORT_FAILED", "导入图片失败")
		}
		return
	}
	writeData(w, r, http.StatusCreated, image)
}

func (f safeURLFetcher) Fetch(ctx context.Context, rawURL string) (*http.Response, func(), error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: URL 格式无效", errUnsafeImportURL)
	}
	if f.resolver == nil {
		f.resolver = net.DefaultResolver
	}
	if f.doHop == nil {
		f.doHop = performSafeImportHop
	}
	for redirects := 0; ; redirects++ {
		addresses, err := validateAndResolveImportURL(ctx, current, f.resolver)
		if err != nil {
			return nil, nil, err
		}
		response, cleanup, err := f.doHop(ctx, current, addresses)
		if err != nil {
			return nil, nil, err
		}
		if !isRedirectStatus(response.StatusCode) {
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				_ = response.Body.Close()
				cleanup()
				return nil, nil, fmt.Errorf("%w: HTTP %d", errImportHTTPStatus, response.StatusCode)
			}
			return response, cleanup, nil
		}
		if redirects >= maxImportRedirects {
			_ = response.Body.Close()
			cleanup()
			return nil, nil, errTooManyRedirects
		}
		location := response.Header.Get("Location")
		_ = response.Body.Close()
		cleanup()
		if location == "" {
			return nil, nil, fmt.Errorf("%w: 重定向缺少 Location", errImportHTTPStatus)
		}
		next, err := current.Parse(location)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: 重定向地址无效", errUnsafeImportURL)
		}
		current = next
	}
}

func validateAndResolveImportURL(ctx context.Context, target *url.URL, resolver importResolver) ([]netip.Addr, error) {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("%w: 只允许 HTTP 或 HTTPS", errUnsafeImportURL)
	}
	if target.User != nil {
		return nil, fmt.Errorf("%w: 禁止 URL 用户信息", errUnsafeImportURL)
	}
	if target.Hostname() == "" {
		return nil, fmt.Errorf("%w: 缺少主机名", errUnsafeImportURL)
	}
	if port := target.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("%w: 端口无效", errUnsafeImportURL)
		}
	}
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(target.Hostname()); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		resolved, err := resolver.LookupNetIP(ctx, "ip", target.Hostname())
		if err != nil {
			return nil, fmt.Errorf("解析导入地址失败: %w", err)
		}
		addresses = resolved
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: 主机名没有 IP 地址", errUnsafeImportURL)
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !safeImportAddress(address) {
			return nil, fmt.Errorf("%w: 主机名解析到受限地址", errUnsafeImportURL)
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			result = append(result, address)
		}
	}
	return result, nil
}

func safeImportAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsLoopback() ||
		address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range forbiddenImportRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func performSafeImportHop(ctx context.Context, target *url.URL, addresses []netip.Addr) (*http.Response, func(), error) {
	expectedHost := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		DialContext:           pinnedImportDialer(expectedHost, port, addresses, dialer.DialContext),
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, err
	}
	request.Header.Set("Accept", "image/*")
	request.Header.Set("User-Agent", "Feather-ImgBed/"+versionForUserAgent())
	response, err := client.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, err
	}
	return response, transport.CloseIdleConnections, nil
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func pinnedImportDialer(expectedHost, expectedPort string, addresses []netip.Addr, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, requestedAddress string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(requestedAddress)
		if err != nil || !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(expectedHost, ".")) || port != expectedPort {
			return nil, fmt.Errorf("%w: 连接目标发生变化", errUnsafeImportURL)
		}
		var lastErr error
		for _, address := range addresses {
			connection, err := dial(ctx, network, net.JoinHostPort(address.String(), expectedPort))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("没有可用 IP 地址")
		}
		return nil, lastErr
	}
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func filenameFromImportURL(target *url.URL) string {
	if target == nil {
		return "imported-image"
	}
	filename := path.Base(target.EscapedPath())
	if decoded, err := url.PathUnescape(filename); err == nil {
		filename = decoded
	}
	filename = safeOriginalName(filename)
	if filename == "" || filename == "." || filename == "/" {
		return "imported-image"
	}
	return filename
}

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func versionForUserAgent() string {
	return "import"
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type envelope struct {
	Success   bool      `json:"success"`
	Data      any       `json:"data,omitempty"`
	Error     *apiError `json:"error,omitempty"`
	RequestID string    `json:"request_id"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responseMetrics struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseMetrics) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseMetrics) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *responseMetrics) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, envelope{Success: true, Data: data, RequestID: requestID(r)})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, envelope{Success: false, Error: &apiError{Code: code, Message: message}, RequestID: requestID(r)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON", "请求内容格式不正确")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON", "请求内容只能包含一个 JSON 对象")
		return false
	}
	return true
}

func requestID(r *http.Request) string {
	if value, ok := r.Context().Value(requestIDKey).(string); ok {
		return value
	}
	return "unknown"
}

func (a *App) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if len(id) < 8 || len(id) > 100 {
			token, _ := randomToken(16)
			id = "req_" + token
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: http: data: blob:; script-src 'self'; style-src 'self'; connect-src 'self'; font-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		metrics := &responseMetrics{ResponseWriter: w}
		next.ServeHTTP(metrics, r)
		status := metrics.status
		if status == 0 {
			status = http.StatusOK
		}
		if r.Method == http.MethodGet && status >= 200 && status < 300 && metrics.bytes > 0 && isImageTrafficPath(r.URL.Path) {
			if err := a.recordTraffic(metrics.bytes); err != nil {
				a.logger.Warn("累计图片流量失败", "path", r.URL.Path, "bytes", metrics.bytes, "error", err)
			}
		}
		a.logger.Info("请求完成",
			"request_id", requestID(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"response_bytes", metrics.bytes,
			"client_ip", a.clientIP(r),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func isImageTrafficPath(path string) bool {
	return strings.HasPrefix(path, "/files/") ||
		strings.HasPrefix(path, "/s3-files/") ||
		strings.HasPrefix(path, "/tg-files/") ||
		strings.HasPrefix(path, "/api/v1/trash/")
}

func (a *App) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("请求处理异常", "request_id", requestID(r), "error", fmt.Sprint(recovered))
				writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器暂时无法处理请求")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !a.trustedProxy(peer) {
		return peer.String()
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			return peer.String()
		}
		candidate = candidate.Unmap()
		if !a.trustedProxy(candidate) || i == 0 {
			return candidate.String()
		}
	}
	return peer.String()
}

func (a *App) trustedProxy(ip netip.Addr) bool {
	for _, prefix := range a.trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *App) limit(scope string, max int, window time.Duration, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.limitHandler(scope, max, window, handler).ServeHTTP(w, r)
	}
}

func (a *App) limitHandler(scope string, max int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.limiter.allow(scope+":"+a.clientIP(r), max, window) {
			w.Header().Set("Retry-After", "60")
			writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "请求过于频繁，请稍后重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func contextWithTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

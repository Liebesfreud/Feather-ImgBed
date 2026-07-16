package app

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "feather_session"
const bcryptMaxPasswordBytes = 72

func (a *App) initialized(ctx context.Context) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	return count > 0, err
}

func (a *App) authStatus(w http.ResponseWriter, r *http.Request) {
	initialized, err := a.initialized(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "无法读取初始化状态")
		return
	}
	p, authenticated := a.authenticate(r)
	data := map[string]any{"initialized": initialized, "authenticated": authenticated}
	// Let an authenticated browser tab recover its CSRF value after a hard refresh
	// or when the session cookie is opened in a new tab. Bearer clients do not need it.
	if authenticated && p.ViaSession {
		data["csrf_token"] = p.CSRFToken
	}
	writeData(w, r, http.StatusOK, data)
}

func (a *App) initialize(w http.ResponseWriter, r *http.Request) {
	initialized, err := a.initialized(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "初始化失败")
		return
	}
	if initialized {
		writeError(w, r, http.StatusConflict, "ALREADY_INITIALIZED", "系统已经完成初始化")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		SiteURL  string `json:"site_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if len(input.Username) < 3 || len(input.Username) > 64 || len(input.Password) < 10 || len(input.Password) > bcryptMaxPasswordBytes {
		writeError(w, r, http.StatusBadRequest, "INVALID_CREDENTIALS", "用户名为 3 到 64 个字符，密码为 10 到 72 字节")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "HASH_ERROR", "初始化失败")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "初始化失败")
		return
	}
	defer tx.Rollback()
	now := nowUTC()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO users(username,password_hash,created_at,updated_at) VALUES(?,?,?,?)", input.Username, string(hash), now, now)
	if err != nil {
		writeError(w, r, http.StatusConflict, "INITIALIZATION_CONFLICT", "初始化已由另一请求完成")
		return
	}
	userID, _ := result.LastInsertId()
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO configs(key,value,encrypted,updated_at) VALUES('initialized','true',0,?)", now); err != nil {
		writeError(w, r, http.StatusConflict, "INITIALIZATION_CONFLICT", "初始化已由另一请求完成")
		return
	}
	settings := defaultSettings()
	settings.SiteURL = strings.TrimRight(input.SiteURL, "/")
	storageID := "local"
	settings.DefaultStorageID = storageID
	if err := saveSettingsTx(r.Context(), tx, settings); err != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "保存初始设置失败")
		return
	}
	localConfig, err := encryptJSON(a.masterKey, map[string]any{"data_dir": "images", "public_url": strings.TrimRight(input.SiteURL, "/") + "/files"})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ENCRYPTION_ERROR", "初始化存储失败")
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO storages(id,name,type,enabled,config,encrypted,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", storageID, "本地存储", "local", 1, localConfig, 1, now, now)
	if err != nil || tx.Commit() != nil {
		writeError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "初始化失败")
		return
	}
	a.createSession(w, r, userID)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	initialized, _ := a.initialized(r.Context())
	if !initialized {
		writeError(w, r, http.StatusPreconditionRequired, "NOT_INITIALIZED", "请先完成管理员初始化")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var id int64
	var hash string
	err := a.db.QueryRowContext(r.Context(), "SELECT id,password_hash FROM users WHERE username=?", strings.TrimSpace(input.Username)).Scan(&id, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		writeError(w, r, http.StatusUnauthorized, "INVALID_LOGIN", "用户名或密码不正确")
		return
	}
	a.createSession(w, r, id)
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request, userID int64) {
	_, _ = a.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE expires_at<=?", nowUTC())
	raw, err := randomToken(32)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SESSION_ERROR", "无法创建登录会话")
		return
	}
	csrf, _ := randomToken(24)
	expires := time.Now().UTC().Add(24 * time.Hour)
	expiresAt := formatTime(expires)
	_, err = a.db.ExecContext(r.Context(), "INSERT INTO sessions(id_hash,user_id,csrf_token,expires_at,created_at) VALUES(?,?,?,?,?)", hashToken(raw), userID, csrf, expiresAt, nowUTC())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SESSION_ERROR", "无法创建登录会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: raw, Path: "/", MaxAge: 86400, HttpOnly: true, Secure: a.cfg.SecureCookie || r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeData(w, r, http.StatusOK, map[string]any{"csrf_token": csrf, "expires_at": expiresAt})
}

func (a *App) authenticate(r *http.Request) (principal, bool) {
	authorization := r.Header.Get("Authorization")
	if strings.HasPrefix(authorization, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		var id string
		var expires sql.NullString
		err := a.db.QueryRowContext(r.Context(), "SELECT id,expires_at FROM api_tokens WHERE token_hash=?", hashToken(token)).Scan(&id, &expires)
		if err == nil && (!expires.Valid || expires.String > nowUTC()) {
			_, _ = a.db.ExecContext(r.Context(), "UPDATE api_tokens SET last_used_at=? WHERE id=?", nowUTC(), id)
			return principal{}, true
		}
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return principal{}, false
	}
	var p principal
	err = a.db.QueryRowContext(r.Context(), "SELECT user_id,csrf_token FROM sessions WHERE id_hash=? AND expires_at>?", hashToken(cookie.Value), nowUTC()).Scan(&p.UserID, &p.CSRFToken)
	if err != nil {
		return principal{}, false
	}
	p.ViaSession = true
	return p, true
}

func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initialized, err := a.initialized(r.Context())
		if err != nil || !initialized {
			writeError(w, r, http.StatusPreconditionRequired, "NOT_INITIALIZED", "请先完成管理员初始化")
			return
		}
		p, ok := a.authenticate(r)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "请先登录或提供有效的 API Token")
			return
		}
		if p.ViaSession && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-CSRF-Token") != p.CSRFToken {
			writeError(w, r, http.StatusForbidden, "CSRF_INVALID", "请求安全校验失败，请刷新页面后重试")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE id_hash=?", hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.SecureCookie || r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeData(w, r, http.StatusOK, map[string]bool{"logged_out": true})
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	if !p.ViaSession || p.UserID == 0 {
		writeError(w, r, http.StatusForbidden, "SESSION_REQUIRED", "修改密码需要使用管理员会话登录")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.NewPassword) < 10 || len(input.NewPassword) > bcryptMaxPasswordBytes {
		writeError(w, r, http.StatusBadRequest, "WEAK_PASSWORD", "新密码必须为 10 到 72 字节")
		return
	}
	var currentHash string
	if err := a.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE id=?", p.UserID).Scan(&currentHash); err != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(input.CurrentPassword)) != nil {
		writeError(w, r, http.StatusUnauthorized, "INVALID_PASSWORD", "当前密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		writeError(w, r, 500, "HASH_ERROR", "修改密码失败")
		return
	}
	_, err = a.db.ExecContext(r.Context(), "UPDATE users SET password_hash=?,updated_at=? WHERE id=?", string(hash), nowUTC(), p.UserID)
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "修改密码失败")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id=?", p.UserID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeData(w, r, http.StatusOK, map[string]bool{"password_changed": true})
}

func (a *App) listTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), "SELECT id,name,last_used_at,expires_at,created_at FROM api_tokens ORDER BY created_at DESC")
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取 Token 失败")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, created string
		var last, expires sql.NullString
		if err := rows.Scan(&id, &name, &last, &expires, &created); err != nil {
			writeError(w, r, 500, "DATABASE_ERROR", "读取 Token 失败")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "last_used_at": nullable(last), "expires_at": nullable(expires), "created_at": created})
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "读取 Token 失败")
		return
	}
	writeData(w, r, 200, items)
}

func nullable(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func (a *App) createToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 {
		writeError(w, r, 400, "INVALID_NAME", "Token 名称不能为空且最多 100 个字符")
		return
	}
	if input.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, input.ExpiresAt); err != nil || !t.After(time.Now()) {
			writeError(w, r, 400, "INVALID_EXPIRY", "过期时间必须是未来的 ISO 8601 时间")
			return
		} else {
			input.ExpiresAt = formatTime(t)
		}
	}
	raw, _ := randomToken(32)
	id, _ := randomToken(12)
	id = "tok_" + id
	var expires any
	if input.ExpiresAt != "" {
		expires = input.ExpiresAt
	}
	_, err := a.db.ExecContext(r.Context(), "INSERT INTO api_tokens(id,name,token_hash,expires_at,created_at) VALUES(?,?,?,?,?)", id, input.Name, hashToken(raw), expires, nowUTC())
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "创建 Token 失败")
		return
	}
	writeData(w, r, 201, map[string]any{"id": id, "name": input.Name, "token": raw, "expires_at": expires})
}

func (a *App) deleteToken(w http.ResponseWriter, r *http.Request) {
	result, err := a.db.ExecContext(r.Context(), "DELETE FROM api_tokens WHERE id=?", r.PathValue("id"))
	if err != nil {
		writeError(w, r, 500, "DATABASE_ERROR", "撤销 Token 失败")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, r, 404, "TOKEN_NOT_FOUND", "Token 不存在")
		return
	}
	writeData(w, r, 200, map[string]bool{"revoked": true})
}

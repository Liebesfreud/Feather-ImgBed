package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetAdminPassword(t *testing.T) {
	a := newTestApp(t)
	_, _ = initializeTestApp(t, a.Handler())
	dataDir := a.cfg.DataDir
	_ = a.Close()
	if err := ResetAdminPassword(context.Background(), Config{DataDir: dataDir}, "", "new-very-secure-password"); err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{DataDir: dataDir, MasterKeyFile: filepath.Join(dataDir, "master.key"), Version: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	oldRecorder, _ := request(t, b.Handler(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"very-secure-password"}`), nil, "", "", "application/json")
	if oldRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码仍可登录: %d", oldRecorder.Code)
	}
	newRecorder, _ := request(t, b.Handler(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"new-very-secure-password"}`), nil, "", "", "application/json")
	if newRecorder.Code != http.StatusOK {
		t.Fatalf("新密码无法登录: %d %s", newRecorder.Code, newRecorder.Body.String())
	}
}

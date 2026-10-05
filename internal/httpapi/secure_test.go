package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/auth"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/database"
)

func secureFixture(t *testing.T) (http.Handler, *database.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.OpenWithSecrets(dir, dir+"/keys")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := auth.New(db)
	password, err := a.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return NewWithOptions(Options{Auth: a, DevHTTP: true}), db, password
}

func TestLoginStrictJSONSessionCSRFAndExpiry(t *testing.T) {
	h, db, password := secureFixture(t)
	bad := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"x","extra":true}`))
	badRequest.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(bad, badRequest)
	if bad.Code != 400 {
		t.Fatalf("лишнее поле: %d", bad.Code)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("Origin", "http://localhost")
	h.ServeHTTP(login, req)
	if login.Code != 200 {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	var session sessionResponse
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || session.CSRFToken == "" {
		t.Fatalf("ответ login: %+v", session)
	}
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("небезопасная session cookie")
	}
	mutation := func(csrf string) int {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/auth/logout", nil)
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := mutation(""); code != 403 {
		t.Fatalf("logout без CSRF=%d", code)
	}
	if _, err := db.SQL.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/auth/session", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatalf("просроченная сессия: %d %s", w.Code, w.Body.String())
	}
}

func TestLoginRejectsCrossOriginAndOversizedPassword(t *testing.T) {
	h, _, _ := secureFixture(t)
	for _, tc := range []struct {
		origin, password string
		want             int
	}{{"https://evil.example", "x", 403}, {"", strings.Repeat("x", 1025), 400}} {
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": tc.password})
		r := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/auth/login", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("origin=%q len=%d: %d", tc.origin, len(tc.password), w.Code)
		}
	}
}

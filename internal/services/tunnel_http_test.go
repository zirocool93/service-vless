package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/zirocool93/service-vless/internal/tunnel"
)

func TestTunnelStatusTruthfullyUnavailableWithoutProductionEnvironment(t *testing.T) {
	root := t.TempDir()
	service := New(&memoryStore{}, "missing-xray", 1080, 8080)
	service.SetTunnel(tunnel.New(root, "missing-xray"))
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/tunnel/status", nil)
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var status tunnel.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Available || status.State != "disabled" || status.Message == "" {
		t.Fatalf("нечестный статус: %+v", status)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("status создал файлы: %v", entries)
	}
}

func TestTunnelStatusWithoutManagerIsDisabledAndReadOnly(t *testing.T) {
	service := New(&memoryStore{}, "missing-xray", 1080, 8080)
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/tunnel/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var status tunnel.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "disabled" || status.Message != "Full Tunnel не настроен" {
		t.Fatalf("status=%+v", status)
	}
}

func TestTunnelWithoutManagerRejectsMutationsAndUnknownRoutes(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/tunnel/apply", http.StatusServiceUnavailable},
		{"/api/v1/tunnel/unknown", http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.path, nil)
		w := httptest.NewRecorder()
		s.tunnelAction(w, r, tc.path)
		if w.Code != tc.status {
			t.Fatalf("%s: status=%d, want %d", tc.path, w.Code, tc.status)
		}
	}
}

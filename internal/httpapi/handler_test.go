package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/web"
)

func TestNewServesEmbeddedFrontendAndAsset(t *testing.T) {
	h := New()
	assets, err := fs.Glob(web.Dist, "assets/*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) == 0 {
		t.Fatal("во встроенном dist не найден JavaScript-ресурс")
	}
	for _, tc := range []struct{ path, contentType string }{
		{"/", "text/html"},
		{"/" + assets[0], "text/javascript"},
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != http.StatusOK {
			t.Fatalf("%s вернул статус %d; встроенная файловая система содержит dist/index.html=%v", tc.path, r.Code, fsExists(web.Dist, "index.html"))
		}
		if !strings.HasPrefix(r.Header().Get("Content-Type"), tc.contentType) {
			t.Errorf("%s: Content-Type = %q", tc.path, r.Header().Get("Content-Type"))
		}
		if r.Body.Len() == 0 {
			t.Errorf("%s вернул пустое тело", tc.path)
		}
	}
}

func fsExists(fsys fs.FS, name string) bool { _, err := fs.Stat(fsys, name); return err == nil }

func TestHealthAndStatusContracts(t *testing.T) {
	h := NewWithAssets(fstest.MapFS{"index.html": {Data: []byte("app")}})
	for _, tc := range []struct{ path, key, value string }{
		{"/api/v1/health", "status", "ok"}, {"/api/v1/status", "state", "disconnected"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("response = %d %q", r.Code, r.Header().Get("Content-Type"))
			}
			var got map[string]any
			if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got[tc.key] != tc.value {
				t.Fatalf("%s = %v", tc.key, got[tc.key])
			}
			if _, ok := got["message"].(string); !ok {
				t.Fatal("поле message должно содержать текст")
			}
			if _, fake := got["metrics"]; fake {
				t.Fatal("status не должен содержать вымышленные метрики")
			}
		})
	}
}

func TestUnknownAPIIsNotFound(t *testing.T) {
	h := NewWithAssets(fstest.MapFS{"index.html": {Data: []byte("app")}})
	for _, p := range []string{"/api", "/api/v1/missing"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, p, nil))
		if r.Code != http.StatusNotFound {
			t.Fatalf("%s code = %d", p, r.Code)
		}
	}
}

func TestSPAAllowsOnlyGetAndHead(t *testing.T) {
	h := NewWithAssets(fstest.MapFS{"index.html": {Data: []byte("app")}})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, "/home", nil))
		if r.Code != http.StatusOK {
			t.Errorf("%s code = %d", method, r.Code)
		}
		if method == http.MethodHead && r.Body.Len() != 0 {
			t.Error("HEAD вернул тело ответа")
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/home", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST code = %d", r.Code)
	}
}

func TestSPAAndStaticAsset404(t *testing.T) {
	h := NewWithAssets(fstest.MapFS{"index.html": {Data: []byte("<main>app</main>")}, "assets/app.js": {Data: []byte("ok")}})
	for _, tc := range []struct {
		path, want string
		code       int
	}{{"/", "<main>app</main>", 200}, {"/settings", "<main>app</main>", 200}, {"/assets/app.js", "ok", 200}, {"/assets/missing.js", "404 page not found\n", 404}, {"/favicon.ico", "404 page not found\n", 404}} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != tc.code || r.Body.String() != tc.want {
			t.Errorf("%s => %d %q", tc.path, r.Code, r.Body.String())
		}
	}
}

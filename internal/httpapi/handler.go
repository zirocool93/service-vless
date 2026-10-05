package httpapi

import (
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/provider"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/web"
)

type response struct {
	Status  string         `json:"status,omitempty"`
	State   provider.State `json:"state,omitempty"`
	Message string         `json:"message"`
}

func New() http.Handler {
	return NewWithAssets(web.Dist)
}

// NewWithAssets позволяет передать тестовые файлы фронтенда.
func NewWithAssets(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response{Status: "ok", Message: "Сервис работает"})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response{State: provider.StateDisconnected, Message: "Провайдер не подключен"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, response{Message: "API-маршрут не найден"})
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, response{Message: "API-маршрут не найден"})
	})
	mux.Handle("/", spaHandler{assets: assets})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, value response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

type spaHandler struct{ assets fs.FS }

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(h.assets, name)
	if err != nil {
		// Маршруты клиента не имеют расширения. Отсутствующие файлы ресурсов
		// должны возвращать 404, а не HTML-документ приложения.
		if path.Ext(name) != "" || strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		data, err = fs.ReadFile(h.assets, "index.html")
		if err != nil {
			http.Error(w, "Frontend не собран", http.StatusNotFound)
			return
		}
		name = "index.html"
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

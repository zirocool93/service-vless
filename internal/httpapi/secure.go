package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/zirocool93/service-vless/internal/auth"
	"github.com/zirocool93/service-vless/internal/events"
	"github.com/zirocool93/service-vless/internal/provider"
	"github.com/zirocool93/service-vless/web"
)

type Options struct {
	Auth    *auth.Service
	Service http.Handler
	Events  *events.Hub
	DevHTTP bool
	Status  func() any
}
type principal struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}
type sessionResponse struct {
	Authenticated bool       `json:"authenticated"`
	User          *principal `json:"user,omitempty"`
	CSRFToken     string     `json:"csrfToken,omitempty"`
}

func NewWithOptions(o Options) http.Handler {
	if o.Events == nil {
		o.Events = events.New()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeAny(w, 200, response{Status: "ok", Message: "Сервис работает"})
	})
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaErr != nil || mediaType != "application/json" {
			writeAny(w, http.StatusUnsupportedMediaType, response{Message: "Требуется Content-Type application/json"})
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeAny(w, http.StatusForbidden, response{Message: "Недопустимый источник запроса"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
			writeAny(w, 403, response{Message: "Недопустимый источник запроса"})
			return
		}
		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if err := decodeStrict(r.Body, &request); err != nil {
			writeAny(w, 400, response{Message: "Некорректные данные входа"})
			return
		}
		if request.Username == "" || request.Password == "" || len(request.Username) > 128 || len(request.Password) > 1024 {
			writeAny(w, 400, response{Message: "Некорректная длина учётных данных"})
			return
		}
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			remote = r.RemoteAddr
		}
		token, csrf, err := o.Auth.Login(r.Context(), request.Username, request.Password, remote)
		if err != nil {
			writeAny(w, 401, response{Message: "Неверные учётные данные или слишком много попыток входа"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: token, Path: "/api/v1", MaxAge: int(auth.SessionLifetime.Seconds()), Expires: time.Now().Add(auth.SessionLifetime), HttpOnly: true, Secure: !o.DevHTTP, SameSite: http.SameSiteStrictMode})
		o.Events.Publish("auth.login", "Выполнен вход пользователя "+request.Username)
		writeAny(w, 200, sessionResponse{Authenticated: true, User: &principal{Username: request.Username, Role: "admin"}, CSRFToken: csrf})
	})
	mux.HandleFunc("GET /api/v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		username, csrf, ok := current(o.Auth, r)
		if !ok {
			writeAny(w, 200, sessionResponse{Authenticated: false})
			return
		}
		writeAny(w, 200, sessionResponse{Authenticated: true, User: &principal{Username: username, Role: "admin"}, CSRFToken: csrf})
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie(auth.CookieName)
		if cookie != nil {
			_ = o.Auth.Logout(r.Context(), cookie.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/api/v1", MaxAge: -1, HttpOnly: true, Secure: !o.DevHTTP, SameSite: http.SameSiteStrictMode})
		o.Events.Publish("auth.logout", "Сеанс завершён")
		writeAny(w, 200, response{Message: "Выход выполнен"})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		if o.Status != nil {
			writeAny(w, 200, o.Status())
			return
		}
		writeAny(w, 200, response{State: provider.StateDisconnected, Message: "Провайдер не подключен"})
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		o.Events.ServeAuthorized(w, r, func() bool { _, _, ok := current(o.Auth, r); return ok })
	})
	if o.Service != nil {
		mux.Handle("/api/v1/connections", o.Service)
		mux.Handle("/api/v1/connections/", o.Service)
		mux.Handle("/api/v1/subscriptions", o.Service)
		mux.Handle("/api/v1/subscriptions/", o.Service)
		mux.Handle("/api/v1/tunnel/", o.Service)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeAny(w, 404, response{Message: "API-маршрут не найден"})
	})
	mux.Handle("/", spaHandler{assets: web.Dist})
	return securityHeaders(authentication(o.Auth, mux))
}
func current(a *auth.Service, r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return "", "", false
	}
	user, csrf, err := a.Authenticate(r.Context(), cookie.Value)
	return user, csrf, err == nil
}
func authentication(a *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/session" {
			next.ServeHTTP(w, r)
			return
		}
		_, csrf, ok := current(a, r)
		if !ok {
			writeAny(w, 401, response{Message: "Требуется вход"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !auth.ValidCSRF(r.Header.Get("X-CSRF-Token"), csrf) {
			writeAny(w, 403, response{Message: "Неверный CSRF-токен"})
			return
		}
		// Непрозрачные формы и cross-origin запросы не принимаются для мутаций.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
				writeAny(w, 403, response{Message: "Недопустимый источник запроса"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func sameOrigin(origin string, r *http.Request) bool {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return origin == scheme+"://"+r.Host
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func writeAny(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func decodeStrict(r io.Reader, value any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("после JSON есть лишние данные")
		}
		return err
	}
	return nil
}

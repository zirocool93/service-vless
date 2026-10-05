package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/zirocool93/service-vless/internal/proxy/subscription"
	"github.com/zirocool93/service-vless/internal/proxy/xray"
	"github.com/zirocool93/service-vless/internal/vpn/amneziawg"
	"io"
	"net/http"
	"strings"
)

func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serve) }
func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case p == "/api/v1/connections" && r.Method == "GET":
		nodes, e := s.store.ListNodes(r.Context())
		respond(w, nodes, e, 200)
	case p == "/api/v1/connections" && r.Method == "POST":
		var v struct {
			URI       string `json:"uri"`
			AWGConfig string `json:"awg_config"`
		}
		if decode(w, r, &v) != nil {
			return
		}
		var n Node
		var e error
		if v.URI != "" && v.AWGConfig == "" {
			var parsed xray.Node
			parsed, e = xray.ParseURI(v.URI)
			if e == nil {
				n.Name = parsed.Name
				n.Kind = "vless"
				n.RawConfig = v.URI
			}
		} else if v.AWGConfig != "" && v.URI == "" {
			_, e = amneziawg.Parse(v.AWGConfig)
			if e == nil {
				n.Name = "AmneziaWG"
				n.Kind = "awg"
				n.RawConfig = v.AWGConfig
			}
		} else {
			e = errors.New("Укажите VLESS URI или AWG config")
		}
		if e == nil {
			n.ID, e = randomID()
			n.Enabled = true
		}
		if e == nil {
			e = s.store.UpsertNode(r.Context(), n)
		}
		if e == nil {
			s.publish("connection.imported", "Импортирован узел "+n.Name)
		}
		respond(w, n, e, 201)
	case p == "/api/v1/connections/status" && r.Method == "GET":
		respond(w, s.Detail(), nil, 200)
	case p == "/api/v1/connections/disconnect" && r.Method == "POST":
		e := s.Disconnect(r.Context())
		respond(w, s.Detail(), e, 200)
	case strings.HasPrefix(p, "/api/v1/connections/"):
		s.connectionAction(w, r, strings.TrimPrefix(p, "/api/v1/connections/"))
	case p == "/api/v1/subscriptions" && r.Method == "GET":
		subs, e := s.store.ListSubscriptions(r.Context())
		respond(w, subs, e, 200)
	case p == "/api/v1/subscriptions" && r.Method == "POST":
		var v struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			UpdateInterval string `json:"update_interval"`
			Enabled        bool   `json:"enabled"`
		}
		if decode(w, r, &v) != nil {
			return
		}
		if strings.TrimSpace(v.Name) == "" || len(v.Name) > 256 || !validInterval(v.UpdateInterval) {
			respond(w, nil, errors.New("Некорректное имя или интервал подписки"), 400)
			return
		}
		nodes, e := subscription.Fetch(r.Context(), v.URL)
		if e != nil {
			respond(w, nil, e, 400)
			return
		}
		id, e := randomID()
		if e != nil {
			respond(w, nil, e, 500)
			return
		}
		sub := Subscription{ID: id, Name: v.Name, URL: v.URL, Enabled: v.Enabled, UpdateInterval: v.UpdateInterval}
		if e = s.store.UpdateSubscription(r.Context(), sub); e == nil {
			_, e = s.syncNodes(r.Context(), sub, nodes)
		}
		if e == nil {
			s.publish("subscription.imported", "Добавлена подписка "+sub.Name)
		}
		respond(w, sub, e, 201)
	case strings.HasPrefix(p, "/api/v1/subscriptions/") && strings.HasSuffix(p, "/refresh") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/subscriptions/"), "/refresh")
		n, e := s.RefreshSubscription(r.Context(), id)
		if e == nil {
			s.publish("subscription.refreshed", "Подписка обновлена")
		}
		respond(w, map[string]int{"count": n}, e, 200)
	default:
		http.NotFound(w, r)
	}
}
func (s *Service) connectionAction(w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(p, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 2 && r.Method == "POST" {
		switch parts[1] {
		case "connect":
			e := s.Connect(r.Context(), id)
			respond(w, s.Detail(), e, 200)
			return
		case "test":
			v, e := s.Test(r.Context(), id)
			respond(w, v, e, 200)
			return
		}
	}
	if len(parts) == 1 && r.Method == "PATCH" {
		var v struct {
			Favorite *bool `json:"favorite"`
			Enabled  *bool `json:"enabled"`
		}
		if decode(w, r, &v) != nil {
			return
		}
		n, e := s.store.GetNode(r.Context(), id)
		if e == nil {
			if v.Favorite != nil {
				n.Favorite = *v.Favorite
			}
			if v.Enabled != nil {
				n.Enabled = *v.Enabled
			}
			e = s.store.UpsertNode(r.Context(), n)
		}
		respond(w, n, e, 200)
		return
	}
	http.NotFound(w, r)
}
func validInterval(v string) bool { return v == "disabled" || v == "6h" || v == "12h" || v == "24h" }
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		respond(w, nil, errors.New("Некорректные данные запроса"), 400)
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		respond(w, nil, errors.New("Лишние данные запроса"), 400)
		return errors.New("лишние данные")
	}
	return nil
}
func respond(w http.ResponseWriter, v any, e error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e != nil {
		if code < 400 {
			code = 400
		}
		if errors.Is(e, ErrAWGUnavailable) {
			code = 501
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
		return
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func randomID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func stableID(subscriptionID, uri string) string {
	sum := sha256.Sum256([]byte(subscriptionID + "\x00" + uri))
	return hex.EncodeToString(sum[:16])
}

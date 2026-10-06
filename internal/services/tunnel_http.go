package services

import (
	"errors"
	"github.com/zirocool93/service-vless/internal/auth"
	"github.com/zirocool93/service-vless/internal/proxy/xray"
	"github.com/zirocool93/service-vless/internal/tunnel"
	"net"
	"net/http"
)

func (s *Service) tunnelAction(w http.ResponseWriter, r *http.Request, path string) {
	if s.tunnel == nil {
		if r.Method == http.MethodGet && path == "/api/v1/tunnel/status" {
			respond(w, tunnel.Status{State: "disabled", Message: "Full Tunnel не настроен"}, nil, 200)
		} else if r.Method == http.MethodPost && (path == "/api/v1/tunnel/prepare" || path == "/api/v1/tunnel/apply" || path == "/api/v1/tunnel/confirm" || path == "/api/v1/tunnel/disable") {
			respond(w, nil, errors.New("Full Tunnel не настроен"), http.StatusServiceUnavailable)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.Method == "GET" && path == "/api/v1/tunnel/status" {
		respond(w, s.tunnel.Status(), nil, 200)
		return
	}
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		NodeID string `json:"node_id"`
		ID     string `json:"transaction_id"`
		Hash   string `json:"hash"`
		Token  string `json:"token"`
	}
	if decode(w, r, &request) != nil {
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		respond(w, nil, errors.New("Адрес управления не определён"), 400)
		return
	}
	ip := net.ParseIP(host)
	if ip == nil {
		respond(w, nil, errors.New("Адрес управления не определён"), 400)
		return
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		respond(w, nil, errors.New("Сессия управления отсутствует"), 401)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	switch path {
	case "/api/v1/tunnel/prepare":
		status := s.Detail()
		if status.State != "connected" || status.ActiveNodeID != request.NodeID {
			respond(w, nil, errors.New("Сначала подключите выбранный VLESS-узел в разделе подключений"), 409)
			return
		}
		node, e := s.store.GetNode(r.Context(), request.NodeID)
		if e != nil {
			respond(w, nil, e, 400)
			return
		}
		if node.Kind != "vless" || !node.Enabled {
			respond(w, nil, errors.New("Для Full Tunnel требуется включённый VLESS-узел"), 400)
			return
		}
		parsed, e := xray.ParseURI(node.RawConfig)
		if e != nil {
			respond(w, nil, e, 400)
			return
		}
		plan, e := s.tunnel.Prepare(r.Context(), parsed, node.ID, ip.String(), cookie.Value)
		respond(w, plan, e, 200)
	case "/api/v1/tunnel/apply":
		status, e := s.tunnel.Apply(r.Context(), request.ID, request.Hash, request.Token, ip.String(), cookie.Value)
		s.publish("tunnel.state", status.Message)
		respond(w, status, e, 202)
	case "/api/v1/tunnel/confirm":
		status, e := s.tunnel.Confirm(request.ID, request.Hash, request.Token, ip.String(), cookie.Value)
		s.publish("tunnel.state", status.Message)
		respond(w, status, e, 200)
	case "/api/v1/tunnel/disable":
		e := s.tunnel.Disable()
		status := s.tunnel.Status()
		s.publish("tunnel.state", status.Message)
		respond(w, status, e, 200)
	default:
		http.NotFound(w, r)
	}
}

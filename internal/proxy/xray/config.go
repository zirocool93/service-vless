package xray

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Config builds an Xray client config without shell interpolation or route changes.
func Config(n Node, socksPort, httpPort int) ([]byte, error) {
	if n.UUID == "" || n.Host == "" || n.Port < 1 {
		return nil, errors.New("Узел VLESS не проверен")
	}
	if socksPort < 1 || socksPort > 65535 || httpPort < 1 || httpPort > 65535 || socksPort == httpPort {
		return nil, errors.New("Некорректные порты локального proxy")
	}
	user := map[string]any{"id": n.UUID, "encryption": "none"}
	if n.Flow != "" {
		user["flow"] = n.Flow
	}
	stream := map[string]any{"network": n.Transport, "security": n.Security}
	switch n.Security {
	case "reality":
		reality := map[string]any{"serverName": n.SNI, "fingerprint": n.Fingerprint, "password": n.PublicKey, "shortId": n.ShortID}
		if n.SpiderX != "" {
			reality["spiderX"] = n.SpiderX
		}
		stream["realitySettings"] = reality
	case "tls":
		tls := map[string]any{"serverName": n.SNI}
		if n.Fingerprint != "" {
			tls["fingerprint"] = n.Fingerprint
		}
		stream["tlsSettings"] = tls
	}
	if n.Transport == "xhttp" {
		h := map[string]any{"path": n.XHTTPPath}
		if n.XHTTPHost != "" {
			h["host"] = n.XHTTPHost
		}
		if n.XHTTPMode != "" {
			h["mode"] = n.XHTTPMode
		}
		if n.XPaddingBytes != "" {
			h["extra"] = map[string]any{"xPaddingBytes": n.XPaddingBytes}
		}
		stream["xhttpSettings"] = h
	}
	cfg := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"tag": "socks-in", "listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}, map[string]any{"tag": "http-in", "listen": "127.0.0.1", "port": httpPort, "protocol": "http"}}, "outbounds": []any{map[string]any{"tag": "proxy", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": n.Host, "port": n.Port, "users": []any{user}}}}, "streamSettings": stream}}}
	b, e := json.MarshalIndent(cfg, "", "  ")
	if e != nil {
		return nil, fmt.Errorf("Сборка конфигурации Xray: %w", e)
	}
	return b, nil
}

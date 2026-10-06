package xray

import (
	"encoding/json"
	"errors"
	"net"
)

// WithBypass отмечает исходящие сокеты production Xray, предотвращая петлю.
func WithBypass(config []byte) ([]byte, error) {
	var c map[string]any
	if err := json.Unmarshal(config, &c); err != nil {
		return nil, err
	}
	for _, raw := range c["outbounds"].([]any) {
		o := raw.(map[string]any)
		s, _ := o["streamSettings"].(map[string]any)
		if s == nil {
			s = map[string]any{}
			o["streamSettings"] = s
		}
		s["sockopt"] = map[string]any{"mark": 512}
	}
	return json.MarshalIndent(c, "", "  ")
}

// FullTunnel фиксирует IPv4 endpoint; SNI и Host сохраняются из исходного узла.
func FullTunnel(n Node, endpoint string) ([]byte, error) {
	ip := net.ParseIP(endpoint)
	if ip == nil || ip.To4() == nil {
		return nil, errors.New("Для Full Tunnel нужен IPv4 VPN-сервера")
	}
	n.Host = ip.To4().String()
	b, err := Config(n, 1080, 8080)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c["inbounds"] = []any{
		map[string]any{"tag": "uvg-transparent", "listen": "127.0.0.1", "port": 12345, "protocol": "dokodemo-door", "settings": map[string]any{"network": "tcp,udp", "followRedirect": true}, "streamSettings": map[string]any{"sockopt": map[string]any{"tproxy": "tproxy"}}},
		// followRedirect сохраняет исходный адрес назначения: Xray использует его
		// как source для синтетического UDP-ответа перехваченного DNS-запроса.
		map[string]any{"tag": "uvg-dns", "listen": "127.0.0.1", "port": 12346, "protocol": "dokodemo-door", "settings": map[string]any{"network": "tcp,udp", "followRedirect": true}, "streamSettings": map[string]any{"sockopt": map[string]any{"tproxy": "tproxy"}}},
	}
	outbounds, ok := c["outbounds"].([]any)
	if !ok {
		return nil, errors.New("Некорректные исходящие соединения Xray")
	}
	// Freedom переписывает адрес назначения DNS, а затем делегирует соединение
	// VLESS outbound. Так DNS не получает прямой выход в сеть.
	outbounds = append(outbounds, map[string]any{
		"tag": "uvg-dns-rewrite", "protocol": "freedom",
		"settings":       map[string]any{"redirect": "1.1.1.1:53"},
		"proxySettings":  map[string]any{"tag": "proxy"},
		"streamSettings": map[string]any{"sockopt": map[string]any{"mark": 512}},
	})
	c["outbounds"] = outbounds
	c["routing"] = map[string]any{"domainStrategy": "AsIs", "rules": []any{
		map[string]any{"type": "field", "inboundTag": []string{"uvg-transparent"}, "outboundTag": "proxy"},
		map[string]any{"type": "field", "inboundTag": []string{"uvg-dns"}, "outboundTag": "uvg-dns-rewrite"},
	}}
	b, err = json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return WithBypass(b)
}

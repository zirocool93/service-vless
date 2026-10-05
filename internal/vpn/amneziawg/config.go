package amneziawg

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type Config struct {
	Interface map[string]string
	Peer      map[string]string
}

// Parse validates an AWG client profile. It never executes wg-quick hooks or changes routes.
func Parse(raw string) (Config, error) {
	if len(raw) > 64<<10 {
		return Config{}, errors.New("Конфигурация AWG слишком велика")
	}
	c := Config{Interface: map[string]string{}, Peer: map[string]string{}}
	section := ""
	count := 0
	allowedI := set("PrivateKey Address DNS MTU ListenPort Jc Jmin Jmax S1 S2 S3 S4 H1 H2 H3 H4 I1 I2 I3 I4 I5 HeaderProtectionKey ContentPaddingAddition RekeyAfterTime RekeyTimeout RejectAfterTime KeepaliveTimeout MaxHandshakeAttempts RandomTrailers DisableCookies")
	allowedP := set("PublicKey PresharedKey AllowedIPs Endpoint PersistentKeepalive")
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		count++
		if count > 512 {
			return Config{}, errors.New("Слишком много строк AWG")
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			switch line {
			case "[Interface]":
				if section != "" {
					return Config{}, errors.New("Повторный раздел AWG")
				}
				section = "interface"
			case "[Peer]":
				if section != "interface" {
					return Config{}, errors.New("Некорректный раздел Peer")
				}
				section = "peer"
			default:
				return Config{}, errors.New("Неизвестный раздел AWG")
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return Config{}, errors.New("Некорректная строка AWG")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == "" {
			return Config{}, fmt.Errorf("Пустой параметр AWG %s", k)
		}
		m := c.Interface
		allowed := allowedI
		if section == "peer" {
			m = c.Peer
			allowed = allowedP
		}
		if !allowed[k] {
			return Config{}, fmt.Errorf("Параметр AWG %s не поддерживается", k)
		}
		if _, exists := m[k]; exists {
			return Config{}, fmt.Errorf("Повторный параметр AWG %s", k)
		}
		m[k] = v
	}
	if !key(c.Interface["PrivateKey"]) || !key(c.Peer["PublicKey"]) {
		return Config{}, errors.New("Некорректный ключ AWG")
	}
	if v := c.Peer["PresharedKey"]; v != "" && !key(v) {
		return Config{}, errors.New("Некорректный preshared key AWG")
	}
	if v := c.Interface["HeaderProtectionKey"]; v != "" && !key(v) {
		return Config{}, errors.New("Некорректный header protection key AWG")
	}
	for _, name := range []string{"Address", "AllowedIPs"} {
		v := c.Interface[name]
		if name == "AllowedIPs" {
			v = c.Peer[name]
		}
		if v == "" {
			return Config{}, fmt.Errorf("Отсутствует %s", name)
		}
		for _, part := range strings.Split(v, ",") {
			if _, e := netip.ParsePrefix(strings.TrimSpace(part)); e != nil {
				return Config{}, fmt.Errorf("Некорректная сеть %s", name)
			}
		}
	}
	endpoint := c.Peer["Endpoint"]
	host, port, e := net.SplitHostPort(endpoint)
	if e != nil || host == "" || port == "" {
		return Config{}, errors.New("Некорректный Endpoint AWG")
	}
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 {
		return Config{}, errors.New("Некорректный порт AWG")
	}
	for _, name := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout", "RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts", "RandomTrailers", "MTU", "ListenPort", "PersistentKeepalive"} {
		v := c.Interface[name]
		if name == "PersistentKeepalive" {
			v = c.Peer[name]
		}
		if v == "" {
			continue
		}
		n, e := strconv.ParseUint(v, 10, 32)
		if e != nil || n > 2147483647 {
			return Config{}, fmt.Errorf("Некорректное число AWG %s", name)
		}
		if name == "ListenPort" && n > 65535 {
			return Config{}, errors.New("Некорректный ListenPort AWG")
		}
	}
	if c.Interface["Jmin"] != "" && c.Interface["Jmax"] != "" {
		a, _ := strconv.Atoi(c.Interface["Jmin"])
		b, _ := strconv.Atoi(c.Interface["Jmax"])
		if a > b {
			return Config{}, errors.New("Jmin больше Jmax")
		}
	}
	for _, name := range []string{"I1", "I2", "I3", "I4", "I5"} {
		if len(c.Interface[name]) > 1024 {
			return Config{}, errors.New("Строка AWG I слишком длинная")
		}
	}
	return c, nil
}
func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Fields(s) {
		m[v] = true
	}
	return m
}
func key(s string) bool { b, e := base64.StdEncoding.DecodeString(s); return e == nil && len(b) == 32 }

package xray

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Node is the validated, secret-bearing VLESS client configuration.
type Node struct {
	Name, Host, UUID, Transport, Security, SNI, Fingerprint, PublicKey, ShortID, Flow, XHTTPHost, XHTTPPath, XHTTPMode, XPaddingBytes, SpiderX, RawURI string
	Port                                                                                                                                               int
}

// CanonicalIdentity возвращает идентичность соединения без отображаемого имени
// и исходного URI. Она намеренно включает все параметры, меняющие транспорт.
func (n Node) CanonicalIdentity() string {
	parts := []string{
		strings.ToLower(n.Host), strconv.Itoa(n.Port), strings.ToLower(n.UUID),
		n.Transport, n.Security, n.Flow, strings.ToLower(n.SNI), n.Fingerprint,
		n.PublicKey, strings.ToLower(n.ShortID), strings.ToLower(n.XHTTPHost),
		n.XHTTPPath, n.XHTTPMode, n.XPaddingBytes, n.SpiderX,
	}
	b, _ := json.Marshal(parts)
	return string(b)
}

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var keyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var fingerprints = map[string]bool{"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true, "edge": true, "360": true, "qq": true, "random": true, "randomized": true}

func ParseURI(raw string) (Node, error) {
	if len(raw) > 8192 {
		return Node{}, errors.New("Ссылка VLESS слишком длинная")
	}
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u == nil || u.Scheme != "vless" || u.Opaque != "" || u.User == nil {
		return Node{}, errors.New("Некорректная ссылка VLESS")
	}
	if _, ok := u.User.Password(); ok {
		return Node{}, errors.New("Пароль в ссылке VLESS не поддерживается")
	}
	if u.Path != "" && u.Path != "/" {
		return Node{}, errors.New("Путь в адресе VLESS не поддерживается")
	}
	uuid := u.User.Username()
	if !uuidRE.MatchString(uuid) {
		return Node{}, errors.New("Некорректный UUID VLESS")
	}
	host := u.Hostname()
	port, e := strconv.Atoi(u.Port())
	if host == "" || e != nil || port < 1 || port > 65535 || strings.ContainsAny(host, " \t\r\n") {
		return Node{}, errors.New("Некорректный адрес или порт VLESS")
	}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 253 || strings.Contains(host, ":") || strings.HasPrefix(host, "-") {
			return Node{}, errors.New("Некорректное имя сервера VLESS")
		}
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return Node{}, errors.New("Некорректная кодировка параметров VLESS")
	}
	allowed := map[string]bool{"type": true, "security": true, "sni": true, "fp": true, "pbk": true, "sid": true, "flow": true, "host": true, "path": true, "mode": true, "encryption": true, "spx": true, "extra": true, "x_padding_bytes": true, "support-x25519mlkem768": true}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return Node{}, fmt.Errorf("Параметр VLESS %q не поддерживается", k)
		}
	}
	if q.Get("encryption") != "" && q.Get("encryption") != "none" {
		return Node{}, errors.New("Поддерживается только шифрование VLESS none")
	}
	n := Node{Name: u.Fragment, Host: host, Port: port, UUID: uuid, Transport: q.Get("type"), Security: q.Get("security"), SNI: q.Get("sni"), Fingerprint: q.Get("fp"), PublicKey: q.Get("pbk"), ShortID: q.Get("sid"), Flow: q.Get("flow"), XHTTPHost: q.Get("host"), XHTTPPath: q.Get("path"), XHTTPMode: q.Get("mode"), XPaddingBytes: q.Get("x_padding_bytes"), SpiderX: q.Get("spx"), RawURI: strings.TrimSpace(raw)}
	if flag := q.Get("support-x25519mlkem768"); flag != "" && flag != "true" && flag != "false" {
		return Node{}, errors.New("Некорректный флаг совместимости VLESS")
	}
	if extra := q.Get("extra"); extra != "" {
		if len(extra) > 2048 {
			return Node{}, errors.New("Параметры XHTTP слишком велики")
		}
		var v map[string]json.RawMessage
		if e := json.Unmarshal([]byte(extra), &v); e != nil {
			return Node{}, errors.New("Некорректный extra XHTTP")
		}
		for k, item := range v {
			switch k {
			case "mode":
				var mode string
				if json.Unmarshal(item, &mode) != nil {
					return Node{}, errors.New("Некорректный режим XHTTP")
				}
				if n.XHTTPMode != "" && n.XHTTPMode != mode {
					return Node{}, errors.New("Конфликт режима XHTTP")
				}
				n.XHTTPMode = mode
			case "xPaddingBytes":
				var padding string
				if json.Unmarshal(item, &padding) != nil {
					return Node{}, errors.New("Некорректный padding XHTTP")
				}
				if n.XPaddingBytes != "" && n.XPaddingBytes != padding {
					return Node{}, errors.New("Конфликт padding XHTTP")
				}
				n.XPaddingBytes = padding
			default:
				return Node{}, fmt.Errorf("Параметр XHTTP extra %q не поддерживается", k)
			}
		}
	}
	if n.Name == "" {
		n.Name = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if len(n.Name) > 256 {
		return Node{}, errors.New("Имя узла слишком длинное")
	}
	if n.Transport == "" || n.Transport == "tcp" {
		n.Transport = "raw"
	}
	if n.Security == "" {
		n.Security = "none"
	}
	if n.Transport != "raw" && n.Transport != "xhttp" {
		return Node{}, errors.New("Транспорт VLESS не поддерживается")
	}
	if n.Security != "none" && n.Security != "tls" && n.Security != "reality" {
		return Node{}, errors.New("Защита VLESS не поддерживается")
	}
	if n.Transport == "xhttp" && n.Security == "none" {
		return Node{}, errors.New("XHTTP требует TLS или REALITY")
	}
	if n.Transport == "raw" && n.Security == "tls" {
		return Node{}, errors.New("RAW/TLS вне поддерживаемых вариантов")
	}
	if n.Security == "reality" {
		if n.Fingerprint == "" {
			n.Fingerprint = "chrome"
		}
		if n.SNI == "" || !keyRE.MatchString(n.PublicKey) || !fingerprints[n.Fingerprint] {
			return Node{}, errors.New("REALITY требует SNI, fingerprint и корректный public key")
		}
		if len(n.ShortID) > 16 || len(n.ShortID)%2 != 0 {
			return Node{}, errors.New("Некорректный short ID")
		}
		if _, e := hex.DecodeString(n.ShortID); e != nil {
			return Node{}, errors.New("Некорректный short ID")
		}
		if _, e := base64.RawURLEncoding.DecodeString(n.PublicKey); e != nil {
			return Node{}, errors.New("Некорректный ключ REALITY")
		}
	} else if n.PublicKey != "" || n.ShortID != "" {
		return Node{}, errors.New("Параметры REALITY недопустимы без REALITY")
	}
	if n.Security == "tls" && n.SNI == "" {
		return Node{}, errors.New("TLS требует SNI")
	}
	if n.Flow != "" && (n.Flow != "xtls-rprx-vision" || n.Transport != "raw" || n.Security != "reality") {
		return Node{}, errors.New("Flow поддерживается только для RAW/REALITY Vision")
	}
	if n.Transport != "xhttp" && (n.XHTTPHost != "" || n.XHTTPPath != "" || n.XHTTPMode != "") {
		return Node{}, errors.New("Параметры XHTTP недопустимы для RAW")
	}
	if n.Transport == "xhttp" {
		if n.XHTTPPath == "" {
			n.XHTTPPath = "/"
		}
		if !strings.HasPrefix(n.XHTTPPath, "/") || strings.ContainsAny(n.XHTTPPath, "\r\n") {
			return Node{}, errors.New("Некорректный путь XHTTP")
		}
		if n.XHTTPMode != "" && n.XHTTPMode != "auto" && n.XHTTPMode != "packet-up" && n.XHTTPMode != "stream-up" && n.XHTTPMode != "stream-one" {
			return Node{}, errors.New("Режим XHTTP не поддерживается")
		}
		if n.XPaddingBytes != "" && !validRange(n.XPaddingBytes) {
			return Node{}, errors.New("Некорректный padding XHTTP")
		}
	} else if n.XPaddingBytes != "" || q.Get("extra") != "" {
		return Node{}, errors.New("Параметры XHTTP недопустимы для RAW")
	}
	if len(n.SpiderX) > 1024 || strings.ContainsAny(n.SpiderX, "\r\n") {
		return Node{}, errors.New("Некорректный spiderX")
	}
	return n, nil
}
func validRange(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		n, e := strconv.Atoi(part)
		if e != nil || n < 0 || n > 65535 {
			return false
		}
	}
	if len(parts) == 2 {
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		return a <= b
	}
	return true
}

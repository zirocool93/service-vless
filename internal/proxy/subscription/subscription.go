package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/xray"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxBody = 2 << 20

func Parse(body []byte) ([]xray.Node, error) {
	if len(body) > maxBody {
		return nil, errors.New("Подписка слишком велика")
	}
	s := strings.TrimSpace(string(body))
	if !strings.Contains(s, "vless://") {
		compact := strings.Join(strings.Fields(s), "")
		var decoded []byte
		var err error
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err = enc.DecodeString(compact)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, errors.New("Подписка не содержит VLESS URI")
		}
		s = string(decoded)
	}
	if len(s) > maxBody {
		return nil, errors.New("Подписка слишком велика")
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 10000 {
		return nil, errors.New("В подписке слишком много строк")
	}
	out := make([]xray.Node, 0, len(lines))
	seen := map[string]bool{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, e := xray.ParseURI(line)
		if e != nil {
			return nil, fmt.Errorf("Строка %d: %w", i+1, e)
		}
		id := n.CanonicalIdentity()
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("Подписка пуста")
	}
	return out, nil
}

// Fetch pins each resolved address before dialing, including redirect targets.
func Fetch(ctx context.Context, raw string) ([]xray.Node, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Подписка требует публичный HTTPS URL")
	}
	resolver := net.DefaultResolver
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 5 * time.Second, DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		ips, e := resolver.LookupNetIP(c, "ip", host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !public(ip) {
				return nil, errors.New("Подписка указывает на локальный или частный адрес")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("Адрес подписки не найден")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(c, network, net.JoinHostPort(ips[0].String(), port))
	}}
	client := &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("Недопустимое перенаправление подписки")
		}
		return nil
	}}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, errors.New("Некорректный URL подписки")
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Не удалось загрузить подписку")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Сервер подписки вернул HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if e != nil {
		return nil, errors.New("Не удалось прочитать подписку")
	}
	return Parse(b)
}

func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is4() {
		a := ip.As4()
		if a[0] == 0 || a[0] == 100 && a[1] >= 64 && a[1] <= 127 || a[0] == 192 && a[1] == 0 || a[0] == 198 && a[1] >= 18 && a[1] <= 19 || a[0] >= 224 {
			return false
		}
	}
	return true
}

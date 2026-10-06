package xray

import (
	"encoding/json"
	"testing"
)

func TestFullTunnelPinnedEndpointAndDNS(t *testing.T) {
	n := Node{Host: "vpn.example", Port: 443, UUID: "11111111-1111-1111-1111-111111111111", Transport: "raw", Security: "tls", SNI: "vpn.example"}
	b, err := FullTunnel(n, "203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Inbounds []struct {
			Tag      string
			Protocol string
			Listen   string
			Port     int
			Settings struct {
				Address        string
				Port           int
				FollowRedirect bool
				Network        string
			}
		}
		Outbounds []struct {
			Tag            string
			Protocol       string
			Settings       map[string]any
			ProxySettings  struct{ Tag string }
			StreamSettings struct {
				TLSSettings struct{ ServerName string }
				Sockopt     struct{ Mark int }
			}
		}
		Routing struct {
			Rules []struct {
				InboundTag  []string
				OutboundTag string
			}
		}
	}
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Inbounds) != 2 || c.Inbounds[1].Tag != "uvg-dns" || c.Inbounds[1].Protocol != "dokodemo-door" || c.Inbounds[1].Settings.Network != "tcp,udp" || !c.Inbounds[1].Settings.FollowRedirect {
		t.Fatal("DNS inbound не сохраняет исходное назначение для UDP-ответа")
	}
	o := c.Outbounds[0]
	proxySettings := o.Settings["vnext"].([]any)[0].(map[string]any)
	if proxySettings["address"] != "203.0.113.8" || o.StreamSettings.TLSSettings.ServerName != "vpn.example" || o.StreamSettings.Sockopt.Mark != 512 {
		t.Fatal("Endpoint/SNI/mark изменён неправильно")
	}
	if len(c.Outbounds) != 2 {
		t.Fatalf("Ожидались VLESS и DNS rewrite outbound, получено %d", len(c.Outbounds))
	}
	dns := c.Outbounds[1]
	if dns.Tag != "uvg-dns-rewrite" || dns.Protocol != "freedom" || dns.Settings["redirect"] != "1.1.1.1:53" || dns.ProxySettings.Tag != "proxy" || dns.StreamSettings.Sockopt.Mark != 512 {
		t.Fatalf("DNS rewrite не перенаправляется в VLESS с bypass mark: %+v", dns)
	}
	if len(c.Routing.Rules) != 2 || c.Routing.Rules[0].OutboundTag != "proxy" || c.Routing.Rules[1].OutboundTag != "uvg-dns-rewrite" || len(c.Routing.Rules[1].InboundTag) != 1 || c.Routing.Rules[1].InboundTag[0] != "uvg-dns" {
		t.Fatalf("Маршруты full tunnel/DNS заданы неверно: %+v", c.Routing.Rules)
	}
	if _, err = FullTunnel(n, "2001:db8::1"); err == nil {
		t.Fatal("Непроверенный IPv6 endpoint разрешён")
	}
}

func TestWithBypassMarksEveryOutbound(t *testing.T) {
	input := []byte(`{"outbounds":[{"tag":"proxy","protocol":"vless"},{"tag":"uvg-dns-rewrite","protocol":"freedom"},{"tag":"aux","protocol":"freedom"}]}`)
	output, err := WithBypass(input)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Outbounds []struct {
			StreamSettings struct {
				Sockopt struct{ Mark int }
			}
		}
	}
	if err = json.Unmarshal(output, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Outbounds) != 3 {
		t.Fatalf("Ожидались три outbound, получено %d", len(config.Outbounds))
	}
	for i, outbound := range config.Outbounds {
		if outbound.StreamSettings.Sockopt.Mark != 512 {
			t.Errorf("Outbound #%d не помечен для обхода: mark=%d", i, outbound.StreamSettings.Sockopt.Mark)
		}
	}
}

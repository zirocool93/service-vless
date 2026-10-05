package subscription

import (
	"github.com/zirocool93/service-vless/internal/proxy/xray"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublicRejectsMappedSharedAddress(t *testing.T) {
	if public(netip.MustParseAddr("::ffff:100.64.0.1")) {
		t.Fatal("IPv4-mapped CGNAT адрес признан публичным")
	}
	if public(netip.MustParseAddr("100.127.255.254")) {
		t.Fatal("CGNAT адрес признан публичным")
	}
	if !public(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("публичный адрес отклонён")
	}
}

func TestRealSubscription(t *testing.T) {
	path := os.Getenv("SUBSCRIPTION_TEST_FILE")
	if path == "" {
		t.Skip("SUBSCRIPTION_TEST_FILE не задан")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	nodes, e := Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	if len(nodes) != 3 {
		t.Fatalf("ожидалось 3 узла, получено %d", len(nodes))
	}
	bin := os.Getenv("XRAY_TEST_BINARY")
	if bin == "" {
		t.Skip("XRAY_TEST_BINARY не задан")
	}
	for _, n := range nodes {
		cfg, e := xray.Config(n, 1080, 8080)
		if e != nil {
			t.Fatal(e)
		}
		p := filepath.Join(t.TempDir(), "config.json")
		if e = os.WriteFile(p, cfg, 0600); e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(bin, "run", "-test", "-config", p)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if e = cmd.Run(); e != nil {
			t.Fatalf("Xray отверг %s/%s: %v", n.Transport, n.Security, e)
		}
	}
}

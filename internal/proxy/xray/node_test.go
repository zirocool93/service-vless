package xray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sample = "vless://11111111-1111-4111-8111-111111111111@example.com:443?type=raw&security=reality&sni=example.com&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=aabbccdd&flow=xtls-rprx-vision#Test"

func TestParseAndConfig(t *testing.T) {
	n, e := ParseURI(sample)
	if e != nil {
		t.Fatal(e)
	}
	if n.Transport != "raw" || n.Flow != "xtls-rprx-vision" {
		t.Fatalf("unexpected node: %+v", n)
	}
	b, e := Config(n, 1080, 8080)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) == 0 {
		t.Fatal("empty config")
	}
}
func TestRejectUnsupported(t *testing.T) {
	for _, u := range []string{sample[:len(sample)-5] + "&foo=bar#Test", "vless://bad@example.com:443", "vless://11111111-1111-4111-8111-111111111111@example.com:443?type=grpc", "vless://11111111-1111-4111-8111-111111111111@example.com:443?security=reality&pbk=abc"} {
		if _, e := ParseURI(u); e == nil {
			t.Fatalf("accepted %s", u)
		}
	}
}
func TestCanonicalIdentityIgnoresPresentationFields(t *testing.T) {
	a, err := ParseURI(sample)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Name = "Другое отображаемое имя"
	b.RawURI = "не участвует в идентичности"
	if a.CanonicalIdentity() != b.CanonicalIdentity() {
		t.Fatal("имя или исходный URI изменили каноническую идентичность")
	}
}

func TestCanonicalIdentityDistinguishesTransportParameters(t *testing.T) {
	base := Node{Host: "example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Transport: "xhttp", Security: "reality", SNI: "example.com", XHTTPPath: "/one"}
	changed := base
	changed.XHTTPPath = "/two"
	if base.CanonicalIdentity() == changed.CanonicalIdentity() {
		t.Fatal("разные XHTTP path склеены")
	}
	changed = base
	changed.Transport = "raw"
	if base.CanonicalIdentity() == changed.CanonicalIdentity() {
		t.Fatal("разные транспорты склеены")
	}
	changed = base
	changed.Flow = "xtls-rprx-vision"
	if base.CanonicalIdentity() == changed.CanonicalIdentity() {
		t.Fatal("разные flow склеены")
	}
}
func TestRealityFingerprintDefaultAndAllowlist(t *testing.T) {
	without := strings.Replace(sample, "&fp=chrome", "", 1)
	n, err := ParseURI(without)
	if err != nil {
		t.Fatal(err)
	}
	if n.Fingerprint != "chrome" {
		t.Fatalf("ожидался fingerprint chrome, получено %q", n.Fingerprint)
	}
	bad := strings.Replace(sample, "fp=chrome", "fp=unknown-client", 1)
	if _, err := ParseURI(bad); err == nil {
		t.Fatal("неизвестный fingerprint принят")
	}
}
func TestRealXrayConfig(t *testing.T) {
	bin := os.Getenv("XRAY_TEST_BINARY")
	if bin == "" {
		t.Skip("XRAY_TEST_BINARY не задан")
	}
	n, e := ParseURI(sample)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Config(n, 1080, 8080)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(bin, "run", "-test", "-config", path).CombinedOutput()
	if e != nil {
		t.Fatalf("xray rejected config: %v %s", e, out)
	}
}

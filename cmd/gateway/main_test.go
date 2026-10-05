package main

import (
	"crypto/tls"
	"os"
	"runtime"
	"testing"
)

func TestEnsureCertificate(t *testing.T) {
	certPath, keyPath, err := ensureCertificate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("сертификат не загружается: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{certPath, keyPath} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0077 != 0 {
				t.Fatalf("небезопасные права %s: %o", path, info.Mode().Perm())
			}
		}
	}
}

func TestDevHTTPOnlyLoopback(t *testing.T) {
	if !isLoopbackListen("127.0.0.1:8443") || !isLoopbackListen("[::1]:8443") || isLoopbackListen(":8443") || isLoopbackListen("0.0.0.0:8443") {
		t.Fatal("неверная классификация адреса dev HTTP")
	}
}

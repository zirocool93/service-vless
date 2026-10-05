package config

import "testing"

func TestValidateAcceptsExpectedBindForms(t *testing.T) {
	for _, bind := range []string{"127.0.0.1:8443", "[::1]:443", ":8080", "localhost:9000"} {
		t.Run(bind, func(t *testing.T) {
			if err := (Config{Listen: bind, MaxConcurrent: 1}).Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestValidateRejectsInvalidBindAndConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name, bind  string
		concurrency int
	}{
		{"missing-port", "127.0.0.1", 1}, {"zero-port", "127.0.0.1:0", 1}, {"bad-port", "localhost:abc", 1}, {"bad-host", "bad host:80", 1}, {"zero-concurrency", "127.0.0.1:80", 0}, {"too-many", "127.0.0.1:80", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (Config{Listen: tc.bind, MaxConcurrent: tc.concurrency}).Validate(); err == nil {
				t.Fatal("ожидалась ошибка валидации")
			}
		})
	}
}

func TestValidateRejectsNegativeTimeout(t *testing.T) {
	cfg := (Config{Listen: "127.0.0.1:8443", MaxConcurrent: 1}).WithDefaults()
	cfg.ShutdownTimeout = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("отрицательный таймаут должен быть отклонён")
	}
}

package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config содержит параметры запуска и предел одновременных проверок узлов.
type Config struct {
	Listen            string
	MaxConcurrent     int
	DataDir           string
	SecretsDir        string
	XrayBin           string
	SocksPort         int
	HTTPPort          int
	DevHTTP           bool
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

func (c Config) Validate() error {
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 5 {
		return fmt.Errorf("max-concurrent должен быть от 1 до 5")
	}
	if (c.SocksPort != 0 || c.HTTPPort != 0) && (c.SocksPort < 1 || c.SocksPort > 65535 || c.HTTPPort < 1 || c.HTTPPort > 65535 || c.SocksPort == c.HTTPPort) {
		return fmt.Errorf("порты proxy должны быть различными числами от 1 до 65535")
	}
	if c.ReadHeaderTimeout < 0 || c.ShutdownTimeout < 0 {
		return fmt.Errorf("таймауты не могут быть отрицательными")
	}
	host, portText, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen должен иметь формат host:port: %w", err)
	}
	if strings.TrimSpace(host) != host || strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("некорректный host в listen")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("порт listen должен быть числом от 1 до 65535")
	}
	if host != "" && net.ParseIP(host) == nil && !validHostname(host) {
		return fmt.Errorf("некорректный host в listen")
	}
	return nil
}

func validHostname(host string) bool {
	if len(host) > 253 || host == "." {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// WithDefaults задаёт безопасные таймауты HTTP-сервера.
func (c Config) WithDefaults() Config {
	if c.Listen == "" {
		c.Listen = ":8443"
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 5
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/ubuntu-vpn-gateway"
	}
	if c.SecretsDir == "" {
		c.SecretsDir = "/etc/ubuntu-vpn-gateway/secrets"
	}
	if c.XrayBin == "" {
		c.XrayBin = "/usr/local/bin/xray"
	}
	if c.SocksPort == 0 {
		c.SocksPort = 1080
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 8080
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = 5 * time.Second
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 10 * time.Second
	}
	return c
}

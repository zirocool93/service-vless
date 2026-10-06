package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zirocool93/service-vless/internal/auth"
	"github.com/zirocool93/service-vless/internal/config"
	"github.com/zirocool93/service-vless/internal/database"
	"github.com/zirocool93/service-vless/internal/events"
	"github.com/zirocool93/service-vless/internal/httpapi"
	"github.com/zirocool93/service-vless/internal/services"
	"github.com/zirocool93/service-vless/internal/tunnel"
)

// version задаётся при сборке релиза через -ldflags.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("ошибка: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(os.Stdout, version)
		return nil
	}
	if len(args) == 0 || (args[0] != "init" && args[0] != "serve") {
		return errors.New("использование: gateway <init|serve|version> [параметры]")
	}
	cfg, err := parseConfig(args[0], args[1:])
	if err != nil {
		return err
	}
	db, err := database.OpenWithSecrets(cfg.DataDir, cfg.SecretsDir)
	if err != nil {
		return fmt.Errorf("открытие базы: %w", err)
	}
	defer db.Close()
	authService := auth.New(db)
	if args[0] == "init" {
		password, err := authService.Bootstrap(context.Background())
		if err != nil {
			return fmt.Errorf("инициализация администратора: %w", err)
		}
		if password == "" {
			return errors.New("администратор уже инициализирован; новый пароль не создан")
		}
		fmt.Fprintf(os.Stdout, "Имя пользователя: admin\nПароль: %s\n", password)
		return nil
	}
	if count, err := db.UserCount(context.Background()); err != nil {
		return err
	} else if count == 0 {
		return errors.New("администратор не создан; сначала выполните gateway init")
	}
	if cfg.DevHTTP && !isLoopbackListen(cfg.Listen) {
		return errors.New("--dev-http разрешён только для loopback-адреса")
	}
	eventHub := events.New()
	service := services.New(db, cfg.XrayBin, cfg.SocksPort, cfg.HTTPPort, cfg.MaxConcurrent)
	service.SetTunnel(tunnel.New(filepath.Join(cfg.DataDir, "tunnel"), cfg.XrayBin))
	service.SetEventSink(eventHub.Publish)
	handler := httpapi.NewWithOptions(httpapi.Options{Auth: authService, Service: service.Handler(), Events: eventHub, DevHTTP: cfg.DevHTTP, Status: func() any { return service.Detail() }})
	defer func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Disconnect(disconnectCtx)
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go service.StartScheduler(ctx)
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = service.Disconnect(disconnectCtx)
		disconnectCancel()
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Web-интерфейс слушает %s", cfg.Listen)
	if cfg.DevHTTP {
		err = server.ListenAndServe()
	} else {
		cert, key, e := ensureCertificate(cfg.SecretsDir)
		if e != nil {
			return e
		}
		err = server.ListenAndServeTLS(cert, key)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web-сервер: %w", err)
	}
	return nil
}

func parseConfig(command string, args []string) (config.Config, error) {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	cfg := config.Config{}
	f.StringVar(&cfg.Listen, "listen", "", "адрес web-интерфейса")
	f.StringVar(&cfg.DataDir, "data-dir", "", "каталог данных")
	f.StringVar(&cfg.SecretsDir, "secrets-dir", "", "каталог секретов")
	f.StringVar(&cfg.XrayBin, "xray-bin", "", "абсолютный путь к Xray")
	f.IntVar(&cfg.SocksPort, "socks-port", 0, "локальный порт SOCKS5")
	f.IntVar(&cfg.HTTPPort, "http-port", 0, "локальный порт HTTP proxy")
	f.IntVar(&cfg.MaxConcurrent, "max-concurrent", 0, "максимум одновременных проверок")
	f.BoolVar(&cfg.DevHTTP, "dev-http", false, "локальный HTTP без TLS для разработки")
	if err := f.Parse(args); err != nil {
		return cfg, err
	}
	if f.NArg() != 0 {
		return cfg, errors.New("позиционные аргументы не поддерживаются")
	}
	cfg = cfg.WithDefaults()
	return cfg, cfg.Validate()
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ensureCertificate(dir string) (string, string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", "", err
	}
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if certErr == nil && keyErr == nil {
		_ = os.Chmod(certPath, 0600)
		_ = os.Chmod(keyPath, 0600)
		return certPath, keyPath, nil
	}
	if !errors.Is(certErr, os.ErrNotExist) || !errors.Is(keyErr, os.ErrNotExist) {
		return "", "", errors.New("TLS-сертификат и ключ должны существовать вместе; восстановите пару или удалите оставшийся файл")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	ipAddresses := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if addresses, e := net.InterfaceAddrs(); e == nil {
		for _, address := range addresses {
			if ipNet, ok := address.(*net.IPNet); ok && ipNet.IP != nil {
				ipAddresses = append(ipAddresses, ipNet.IP)
			}
		}
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Ubuntu VPN Gateway"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, DNSNames: []string{"localhost"}, IPAddresses: ipAddresses}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err = writeExclusive(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return "", "", err
	}
	if err = writeExclusive(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		_ = os.Remove(certPath)
		return "", "", err
	}
	return certPath, keyPath, nil
}
func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

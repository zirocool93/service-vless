package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/config"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/httpapi"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("ошибка: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("использование: gateway serve [--listen host:port] [--max-concurrent N]")
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8443", "адрес HTTP-сервера")
	maxConcurrent := flags.Int("max-concurrent", 5, "максимум одновременных проверок")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("команда serve не принимает позиционные аргументы")
	}
	cfg := (config.Config{Listen: *listen, MaxConcurrent: *maxConcurrent}).WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	log.Printf("ВНИМАНИЕ: development HTTP без TLS и аутентификации. Production HTTPS и auth будут добавлены в Phase1.")
	server := &http.Server{Addr: cfg.Listen, Handler: httpapi.New(), ReadHeaderTimeout: cfg.ReadHeaderTimeout}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Сервер слушает %s", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP-сервер: %w", err)
	}
	return nil
}

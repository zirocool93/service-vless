package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/zirocool93/service-vless/internal/tunnel"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Использование: uvg-watchdog <monitor|rollback|recover> [ID] [--root DIR]")
		os.Exit(2)
	}
	mode := os.Args[1]
	args := os.Args[2:]
	id := ""
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		id = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet(mode, flag.ExitOnError)
	root := f.String("root", tunnel.DefaultRoot, "Каталог транзакций")
	_ = f.Parse(args)
	var e error
	switch mode {
	case "monitor":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		e = tunnel.Monitor(ctx, *root, id)
	case "rollback":
		e = tunnel.Rollback(*root, id, "Откат независимым helper")
	case "recover":
		e = tunnel.Recover(*root)
	case "namespace-apply":
		e = tunnel.NamespaceApply(*root, id)
	default:
		e = fmt.Errorf("Неизвестная команда")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

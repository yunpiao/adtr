package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "github.com/yunpiao/adtr/internal/runtime"
	"github.com/yunpiao/adtr/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "api", "api, worker or migrate")
	probe := flag.String("probe", "", "check a local health URL and exit")
	flag.Parse()
	if *probe != "" {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(*probe)
		if err != nil {
			return fmt.Errorf("probe failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("probe not ready")
		}
		return nil
	}
	cfg, err := app.LoadConfig(*mode, os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *mode == "migrate" {
		migrationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return store.Migrate(migrationCtx, cfg.DatabaseURL)
	}
	check := func(ctx context.Context) error { return store.Ready(ctx, cfg.DatabaseURL) }
	server := &http.Server{Addr: cfg.ListenAddr, Handler: app.Handler(*mode, check), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Fprintf(os.Stdout, "%s infrastructure starting\n", *mode)
	return app.Serve(ctx, server)
}

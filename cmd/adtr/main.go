package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yunpiao/adtr/internal/auth"
	app "github.com/yunpiao/adtr/internal/runtime"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "api", "api, worker, migrate or bootstrap")
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
		return store.Migrate(migrationCtx, cfg.Database)
	}
	var taskEngine *tasks.Engine
	if *mode == "api" || *mode == "worker" {
		taskEngine, err = tasks.New(cfg.Database, tasks.ProductionRegistry(), auth.NewTaskAuthorizer(), store.SchemaVersion)
		if err != nil {
			return fmt.Errorf("task engine configuration failed")
		}
	}
	check := func(ctx context.Context) error { return store.Ready(ctx, cfg.Database) }
	handler := app.Handler(*mode, check)
	if *mode == "api" || *mode == "bootstrap" {
		encodedKey := os.Getenv("ADTR_AUTH_KEY")
		if encodedKey != "" || *mode == "bootstrap" {
			key, keyErr := base64.StdEncoding.DecodeString(encodedKey)
			if keyErr != nil {
				return fmt.Errorf("ADTR_AUTH_KEY must be base64")
			}
			authentication, authErr := auth.New(cfg.Database, key, os.Getenv("ADTR_ORIGIN"), os.Getenv("ADTR_DEVELOPMENT") == "true")
			if authErr != nil {
				return authErr
			}
			if *mode == "bootstrap" {
				return authentication.Bootstrap(ctx, os.Getenv("ADTR_BOOTSTRAP_USERNAME"), os.Getenv("ADTR_BOOTSTRAP_PASSWORD"))
			}
			mux := http.NewServeMux()
			mux.Handle("/api/auth/", authentication)
			mux.Handle("/api/access/", http.HandlerFunc(authentication.ServeAccessHTTP))
			mux.Handle("/api/resources/", http.HandlerFunc(authentication.ServeResources))
			taskHandler := authentication.TasksHandler(taskEngine)
			mux.Handle("/api/tasks", taskHandler)
			mux.Handle("/api/tasks/", taskHandler)
			mux.Handle("/livez", handler)
			mux.Handle("/readyz", handler)
			if dir := os.Getenv("ADTR_WEB_DIR"); dir != "" {
				root, rootErr := filepath.Abs(dir)
				if rootErr != nil {
					return fmt.Errorf("invalid web directory")
				}
				files := http.FileServer(http.Dir(root))
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/api/") {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("X-Content-Type-Options", "nosniff")
					w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
					files.ServeHTTP(w, r)
				})
			} else {
				mux.Handle("/", handler)
			}
			handler = mux
		}
	}
	server := &http.Server{Addr: cfg.ListenAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Fprintf(os.Stdout, "%s service starting\n", *mode)
	if *mode == "worker" {
		var identity [16]byte
		if _, err = rand.Read(identity[:]); err != nil {
			return fmt.Errorf("worker identity unavailable")
		}
		worker, workerErr := tasks.NewWorker(taskEngine, tasks.WorkerConfig{Owner: hex.EncodeToString(identity[:]), Concurrency: 4, PollInterval: 250 * time.Millisecond, OnError: func(error) { fmt.Fprintln(os.Stderr, "task worker cycle failed") }})
		if workerErr != nil {
			return workerErr
		}
		result := make(chan error, 1)
		go func() { result <- worker.Run(ctx); stop() }()
		serverErr := app.Serve(ctx, server)
		stop()
		workerErr = <-result
		if serverErr != nil {
			return serverErr
		}
		return workerErr
	}
	return app.Serve(ctx, server)
}

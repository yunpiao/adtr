package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/operationallogs"
	app "github.com/yunpiao/adtr/internal/runtime"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (runErr error) {
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
	var operationAccountStore *operationaccounts.Store
	var domainStore *domains.Store
	var taskEngine *tasks.Engine
	var scheduleEngine *schedules.Engine
	var archiveEngine *taskarchive.Engine
	var healthStore *systemhealth.Store
	var journal *operationallogs.Recorder
	if *mode == "api" || *mode == "worker" {
		domainRuntime, domainErr := domainconfig.Load(os.Getenv)
		if domainErr != nil {
			return fmt.Errorf("domain security configuration failed")
		}
		domainStore = domains.New(domainRuntime)
		operationAccountStore = operationaccounts.New(domainRuntime)
		healthStore, err = systemhealth.NewStore(cfg.Database, store.SchemaVersion)
		if err != nil {
			return fmt.Errorf("system health configuration failed")
		}
		taskEngine, err = tasks.New(cfg.Database, tasks.ProductionRegistry(audit.Kind(), domainStore.Kind(), domainStore.AccountKind(), domainStore.DirectoryKind(), operationallogs.Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
		if err != nil {
			return fmt.Errorf("task engine configuration failed")
		}
	}
	if *mode == "api" || *mode == "worker" {
		scheduleEngine, err = schedules.New(cfg.Database, taskEngine, auth.NewScheduleAuthorizer())
		if err != nil {
			return fmt.Errorf("scheduler configuration failed")
		}
		archiveEngine, err = taskarchive.New(auth.NewArchiveAuthorizer())
		if err != nil {
			return fmt.Errorf("archive configuration failed")
		}
	}
	if *mode == "api" || *mode == "worker" {
		journalStore, journalErr := operationallogs.NewStore(cfg.Database, store.SchemaVersion)
		if journalErr != nil {
			return fmt.Errorf("operational journal configuration failed")
		}
		module := operationallogs.API
		if *mode == "worker" {
			module = operationallogs.Worker
		}
		journal, journalErr = operationallogs.NewRecorder(journalStore, module)
		if journalErr != nil {
			return fmt.Errorf("operational recorder unavailable")
		}
		journalCtx, journalCancel := context.WithCancel(context.Background())
		if journalErr = journal.Start(journalCtx); journalErr != nil {
			journalCancel()
			return fmt.Errorf("operational recorder could not start")
		}
		journal.Emit(operationallogs.ServiceStartRequested, operationallogs.Attempted, operationallogs.NoReason)
		defer func() {
			if runErr == nil {
				journal.Emit(operationallogs.ServiceStopped, operationallogs.Completed, operationallogs.NoReason)
			} else {
				reason := operationallogs.ServeFailed
				if module == operationallogs.Worker {
					reason = operationallogs.WorkerFailed
				}
				journal.Emit(operationallogs.ServiceFailed, operationallogs.Failed, reason)
			}
			drain, stopDrain := context.WithTimeout(context.Background(), 5*time.Second)
			closeErr := journal.Close(drain)
			stopDrain()
			journalCancel()
			if closeErr != nil {
				fmt.Fprintln(os.Stderr, "operational journal delivery incomplete")
				runErr = errors.Join(runErr, fmt.Errorf("operational journal delivery incomplete"))
			}
		}()
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
			profileHandler := http.HandlerFunc(authentication.ServeProfileHTTP)
			mux.Handle("/api/profile", profileHandler)
			mux.Handle("/api/profile/", profileHandler)
			systemHandler := authentication.SystemHandler(healthStore)
			mux.Handle("/api/system/", systemHandler)
			auditHandler := authentication.AuditHandler(taskEngine)
			mux.Handle("/api/audit", auditHandler)
			mux.Handle("/api/audit/", auditHandler)
			mux.Handle("/api/access/", http.HandlerFunc(authentication.ServeAccessHTTP))
			mux.Handle("/api/resources/", http.HandlerFunc(authentication.ServeResources))
			credentialUseHandler := authentication.CredentialUseHandler(credentialuse.New())
			mux.Handle("/api/credential-use", credentialUseHandler)
			mux.Handle("/api/credential-use/", credentialUseHandler)
			directoryUseHandler := authentication.DirectoryCredentialUseHandler(credentialuse.New())
			mux.Handle("/api/directory-credential-use", directoryUseHandler)
			mux.Handle("/api/directory-credential-use/", directoryUseHandler)
			operationAccountHandler := authentication.OperationAccountsHandler(operationAccountStore)
			mux.Handle("/api/operation-accounts", operationAccountHandler)
			mux.Handle("/api/operation-accounts/", operationAccountHandler)
			selectionHandler := authentication.DomainSelectionHandler(domainStore)
			mux.Handle("/api/domain-selection", selectionHandler)
			mux.Handle("/api/domain-selection/", selectionHandler)
			domainHandler := authentication.DomainsHandler(domainStore, taskEngine)
			mux.Handle("/api/domains", domainHandler)
			mux.Handle("/api/domains/", domainHandler)
			directoryHandler := authentication.DirectoryHandler(domainStore, taskEngine)
			mux.Handle("/api/directory", directoryHandler)
			mux.Handle("/api/directory/", directoryHandler)
			operationalHandler := authentication.OperationalLogsHandler(taskEngine)
			mux.Handle("/api/system/logs", operationalHandler)
			mux.Handle("/api/system/logs/", operationalHandler)
			taskHandler := authentication.TasksHandler(taskEngine)
			mux.Handle("/api/tasks", taskHandler)
			mux.Handle("/api/tasks/", taskHandler)
			scheduleHandler := authentication.SchedulesHandler(scheduleEngine)
			mux.Handle("/api/tasks/schedules", scheduleHandler)
			mux.Handle("/api/tasks/schedules/", scheduleHandler)
			archiveHandler := authentication.ArchiveHandler(archiveEngine)
			for _, path := range []string{"/api/tasks/archive-candidates", "/api/tasks/archive", "/api/tasks/restore"} {
				mux.Handle(path, archiveHandler)
			}
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
		workerOwner := hex.EncodeToString(identity[:])
		activityRecorder := newWorkerActivityRecorder(func(c context.Context, a tasks.WorkerActivity) error {
			recordOperationalWorkerActivity(journal, a)
			return healthStore.RecordWorkerCycle(c, a.WorkerID, systemhealth.WorkerCycle(a.Cycle), systemhealth.WorkerCycleStatus(a.Status), a.Code)
		})
		worker, workerErr := tasks.NewWorker(taskEngine, tasks.WorkerConfig{Owner: workerOwner, Concurrency: 4, PollInterval: 250 * time.Millisecond, OnActivity: activityRecorder, OnError: func(error) { fmt.Fprintln(os.Stderr, "task worker cycle failed") }})
		if workerErr != nil {
			return workerErr
		}
		schedulerDone := make(chan struct{})
		go func() { runScheduleLoop(ctx, scheduleEngine, workerOwner, activityRecorder); close(schedulerDone) }()
		maintenanceDone := make(chan struct{})
		reconcileUses := func(c context.Context) error {
			return reconcileCredentialReservations(c, func(pass context.Context) error {
				_, err := domainStore.ReconcileReservedAccountUses(pass, cfg.Database, 32)
				return err
			}, func(pass context.Context) error {
				_, err := domainStore.ReconcileReservedDirectoryUses(pass, cfg.Database, 32)
				return err
			})
		}
		go func() {
			runAccountUseMaintenance(ctx, reconcileUses, func(error) { fmt.Fprintln(os.Stderr, "credential reservation reconciliation failed") })
			close(maintenanceDone)
		}()
		result := make(chan error, 1)
		go func() { result <- worker.Run(ctx); stop() }()
		serverErr := app.Serve(ctx, server)
		stop()
		workerErr = <-result
		<-schedulerDone
		<-maintenanceDone
		// Worker.Run has joined every executor and completed its acknowledgement
		// drain. A final separate pass can now release terminal unopened uses.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := reconcileUses(cleanupCtx)
		cleanupCancel()
		workerErr = errors.Join(workerErr, cleanupErr)
		if serverErr != nil {
			return serverErr
		}
		return workerErr
	}
	if *mode == "api" {
		sampler, samplerErr := systemhealth.NewSampler(systemhealth.Config{})
		if samplerErr != nil {
			return samplerErr
		}
		collector, collectorErr := systemhealth.NewCollector(healthStore, sampler)
		if collectorErr != nil {
			return collectorErr
		}
		done := make(chan error, 1)
		go func() { done <- collector.Run(ctx) }()
		serveErr := app.Serve(ctx, server)
		stop()
		<-done
		return serveErr
	}
	return app.Serve(ctx, server)
}

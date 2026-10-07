package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Check func(context.Context) error

// Handler exposes infrastructure status only. There are no business routes yet.
func Handler(service string, check Check) http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, state string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"service": service, "status": state})
	}
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusOK, "alive")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if check == nil || check(ctx) != nil {
			write(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		write(w, http.StatusOK, "ready")
	})
	return mux
}

func Serve(ctx context.Context, server *http.Server) error {
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		err := <-result
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

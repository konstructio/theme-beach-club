// Command theme-beach-club serves the Beach Club theme theme: a
// static frontend (embedded) plus a JSON proxy under /api/gc/ that reads live
// observability data from the groundcover API. There is no sample fallback —
// no data beats fake data; unconfigured installs answer honestly with 503.
//
// Assets are embedded with go:embed because cloud-native buildpacks strip
// source files from the final image — a bare http.Dir would 404 in production.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// staticFS holds the frontend assets.
//
//go:embed static
var staticFS embed.FS


// shutdownTimeout bounds how long in-flight requests may drain on SIGTERM/SIGINT
// before the server is forced closed.
const shutdownTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := LoadConfig()


	srv := newServer(cfg, logger)

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.Error("failed to open embedded static assets", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	srv.routes(mux)
	mux.Handle("/", http.FileServer(http.FS(static)))

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withMiddleware(mux, logger),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Run the server until a termination signal arrives.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("beach-club theme server starting",
			"port", cfg.Port,
			"cluster", cfg.Cluster,
			"mode", modeLabel(cfg.Live()),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received; draining in-flight requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

// modeLabel renders the boot-log mode string without exposing the key.
func modeLabel(live bool) string {
	if live {
		return "live"
	}
	return "not configured"
}

// withMiddleware wraps the mux with panic recovery and request logging. The
// theme carries no credentials from the browser, so there is no auth layer
// here; the API key lives only in this server's environment.
func withMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered",
					"method", r.Method, "path", r.URL.Path, "panic", rec)
				if !sw.wrote {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"code":"internal_error","message":"internal server error"}`))
				}
			}
			// Log API calls; skip static asset noise.
			if len(r.URL.Path) >= 8 && r.URL.Path[:8] == "/api/gc/" {
				logger.Info("request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", sw.status,
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}
		}()

		next.ServeHTTP(sw, r)
	})
}

// statusWriter captures the response status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

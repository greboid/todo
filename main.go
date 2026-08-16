// Command todo serves a single-binary todo app: a JSON API backed by embedded
// sqlite and a Svelte UI embedded via go:embed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/greboid/todo/internal/api"
	"github.com/greboid/todo/internal/db"
	"github.com/greboid/todo/internal/ui"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	apiKey := flag.String("api-key", "", "optional API key guarding /api; requests must send it as an X-API-Key header or a Bearer token (empty disables authentication)")
	flag.Parse()

	addr := envOr("TODO_ADDR", ":8080")
	driver := envOr("TODO_DB_DRIVER", "sqlite")
	dsn := envOr("TODO_DB", "todo.db")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, driver, dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = store.Close() }()

	mux := http.NewServeMux()
	handler := api.New(store, *apiKey)
	mux.Handle("/api/", handler.Routes())
	mux.Handle("/", spaHandler(ui.FS(), handler.MintSession))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve blocks the main goroutine; shutdown runs in a goroutine keyed to
	// ctx. context.WithoutCancel detaches from ctx so Shutdown uses a fresh
	// deadline instead of the already-cancelled signal context.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("todo listening", "addr", addr, "driver", driver, "db", dsn, "api-key", *apiKey != "")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// spaHandler serves static assets from root. Unknown GET paths fall back to
// index.html so client-side routing works. Serving the document mints a
// browser session via mintSession (when an API key is configured), so the
// SPA can call the guarded API without ever knowing the key.
func spaHandler(root fs.FS, mintSession func(http.ResponseWriter)) http.Handler {
	files := http.FileServerFS(root)
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		panic(fmt.Sprintf("ui: read index.html: %v", err))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			files.ServeHTTP(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if _, err := fs.Stat(root, name); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		// Not a real file: serve index.html for client-side routing.
		if mintSession != nil {
			mintSession(w)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

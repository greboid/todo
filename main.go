// Command todo serves a single-binary todo app: a JSON API backed by embedded
// sqlite and a Svelte UI embedded via go:embed.
package main

import (
	"context"
	"errors"
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
	addr := envOr("TODO_ADDR", ":8080")
	dbPath := envOr("TODO_DB", "todo.db")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = store.Close() }()

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(store).Routes())
	mux.Handle("/", spaHandler(ui.FS()))

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

	slog.Info("todo listening", "addr", addr, "db", dbPath)
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
// index.html so client-side routing works.
func spaHandler(root fs.FS) http.Handler {
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

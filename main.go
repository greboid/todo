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

	"github.com/csmith/envflag/v2"
	"github.com/csmith/slogflags"

	"github.com/greboid/todo/internal/api"
	"github.com/greboid/todo/internal/db"
	"github.com/greboid/todo/internal/schedule"
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
	addr := flag.String("addr", ":8080", "address the HTTP server listens on")
	driver := flag.String("db-driver", "sqlite", "database backend: sqlite (also sqlite3) or postgres (also pg/postgresql)")
	dsn := flag.String("db", "todo.db", "for SQLite, the database file path; for Postgres, a libpq-style connection string")
	defaultDue := flag.String("default-due", "", "default due/repeating schedule applied to new todos without their own, in the quick-add date grammar (e.g. \"tomorrow\", \"every monday\", \"in 3 days\"); empty means no due date")
	envflag.Parse(envflag.WithPrefix("TODO_"))
	_ = slogflags.Logger(slogflags.WithSetDefault(true))

	// Fail fast on an unparsable default rather than rejecting every create
	// at runtime; parsing again per creation keeps relative dates fresh.
	if *defaultDue != "" {
		if _, err := schedule.Parse(*defaultDue, time.Now().UTC()); err != nil {
			return fmt.Errorf("parse -default-due: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, *driver, *dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = store.Close() }()

	mux := http.NewServeMux()
	handler := api.New(store, *apiKey).WithDefaultDue(*defaultDue)
	// /api reads are never cached by the browser: the service worker keeps
	// its own "last good sync" copy, and an intervening HTTP-cache entry
	// would shadow it. The worker, manifest, and document are revalidated
	// every time (no-cache) so updates land instead of sticking in caches.
	mux.Handle("/api/", noStore(handler.Routes()))
	mux.HandleFunc("GET /sw.js", serveArtifact(ui.FS(), "sw.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /manifest.webmanifest", serveArtifact(ui.FS(), "manifest.webmanifest", "application/manifest+json; charset=utf-8"))
	mux.Handle("/", spaHandler(ui.FS(), handler.MintSession))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve blocks the main goroutine; shutdown runs in a goroutine keyed to
	// ctx. context.WithoutCancel detaches from ctx so Shutdown uses a fresh
	// deadline instead of the already-cancelled signal context.
	go func() {
		<-ctx.Done()
		// End open SSE streams first: Shutdown waits for active requests,
		// and the event streams are the only long-lived ones — without this
		// every shutdown idles out its full grace period on idle tabs.
		handler.Close()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("todo listening", "addr", *addr, "driver", *driver, "db", *dsn, "api-key", *apiKey != "")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

// noStore pins Cache-Control: no-store on API responses so the browser never
// serves a stale HTTP-cache entry where the service worker expects to see the
// network's answer (and cache the last good one itself).
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// serveArtifact serves a build artifact that must revalidate on every fetch:
// the service worker and manifest only update when the browser actually sees
// the new bytes, so explicit content types and no opaque caching.
func serveArtifact(root fs.FS, name, contentType string) http.HandlerFunc {
	body, err := fs.ReadFile(root, name)
	if err != nil {
		panic(fmt.Sprintf("ui: read %s: %v", name, err))
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	}
}

// spaHandler serves static assets from root. Unknown GET paths fall back to
// index.html so client-side routing works. Serving the document mints a
// browser session via mintSession (when an API key is configured), so the
// SPA can call the guarded API without ever knowing the key.
func spaHandler(root fs.FS, mintSession func(http.ResponseWriter, *http.Request)) http.Handler {
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
			mintSession(w, r)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

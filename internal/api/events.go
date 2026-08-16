package api

// Live sync: server-sent events that poke open tabs to re-fetch.
//
// Every successful mutation (from any client — the SPA, a phone, a curl
// against the API) broadcasts a "sync" event on GET /api/events. The client
// treats it as a hint to run its normal load(); it never carries data, so
// the server stays the single source of truth and offline/PWA semantics are
// untouched.
//
// Design notes:
//   - SSE, not WebSocket: the channel is one-directional and the Go side
//     stays stdlib-only. The EventSource reconnect loop is free.
//   - The stream holds no database connection, so SQLite's single-writer
//     pool is unaffected by idle tabs.
//   - Pokes are signals, not messages: each subscriber channel is buffered
//     with capacity 1 and broadcasts are non-blocking, so back-to-back
//     mutations coalesce and a slow client can never block a handler.
//   - The poke is deliberately dumb — no board scoping, no payload. Clients
//     re-fetch the (already server-filtered) view they are looking at.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// eventHeartbeat is how often the stream sends a liveness ping. It keeps
// intermediaries from buffering or idling out the connection, and clients
// use it to detect silently-dead connections (a stream that is OPEN but
// has not delivered bytes in several heartbeat periods is torn down and
// reconnected). It is a named `ping` event, not a comment: comment lines
// are invisible to EventSource listeners, so they cannot drive a client
// watchdog. A var (not const) so tests can shrink it.
var eventHeartbeat = 20 * time.Second

// eventRetryMs is the reconnect delay advertised to browsers.
const eventRetryMs = 3000

// eventBus tracks connected SSE clients and fans out mutation pokes.
type eventBus struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan struct{}]struct{})}
}

// subscribe registers a new client and returns its (buffered, capacity 1)
// signal channel.
func (b *eventBus) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// unsubscribe removes a client. Safe to call more than once.
func (b *eventBus) unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// close shuts the bus down: every subscriber's channel is closed, which
// streamEvents observes and treats as end-of-stream, so in-flight event
// requests finish and a graceful server shutdown is not held up by idle
// SSE clients. Idempotent, and safe against concurrent subscribe/broadcast
// (all three share the mutex, and closed channels are removed from the map
// before anyone can send on them).
func (b *eventBus) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
	}
}

// broadcast pokes every client. Non-blocking: a client that has not drained
// its previous poke keeps it (pokes coalesce), and nothing written here can
// stall a request handler.
func (b *eventBus) broadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// streamEvents serves GET /api/events as an SSE stream: a "sync" event after
// every successful mutation and a "ping" heartbeat between them. The stream
// ends when the client disconnects (request context) or a write fails.
func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Disable proxy buffering (nginx and friends) so events are flushed, not held.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", eventRetryMs)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch := h.bus.subscribe()
	defer h.bus.unsubscribe(ch)

	ticker := time.NewTicker(eventHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-ch:
			// A closed channel is the bus shutting down (server stopping):
			// end the stream so Shutdown is not held waiting on it.
			if !ok {
				return
			}
			if _, err := fmt.Fprint(w, "event: sync\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// notifyMutations wraps next so that any successful mutating request
// (POST/PUT/PATCH/DELETE) pokes all connected clients afterwards. The
// schedule parse/extract POSTs are excluded: they never touch stored data
// and fire on every keystroke in the due-date field.
func (h *Handler) notifyMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/schedule/") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			h.bus.broadcast()
		}
	})
}

// statusRecorder captures the status code written by the wrapped handler so
// notifyMutations can poke only on success. Flush (needed by the SSE stream)
// passes through.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

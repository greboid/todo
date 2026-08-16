package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventBusBroadcastCoalesces(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()

	// Back-to-back broadcasts with no drain in between coalesce into one
	// pending poke instead of queueing or blocking.
	bus.broadcast()
	bus.broadcast()
	select {
	case <-ch:
	default:
		t.Fatal("coalesced broadcasts must deliver a poke")
	}
	select {
	case <-ch:
		t.Fatal("buffer must hold exactly one pending poke")
	default:
	}

	// Unsubscribed clients stop receiving; double unsubscribe is safe.
	bus.unsubscribe(ch)
	bus.unsubscribe(ch)
	bus.broadcast()
	select {
	case <-ch:
		t.Fatal("unsubscribed client must not receive pokes")
	default:
	}
}

func TestNotifyMutations(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		status   int
		wantPoke bool
	}{
		{"create", http.MethodPost, "/api/todos", http.StatusCreated, true},
		{"update", http.MethodPatch, "/api/todos/1", http.StatusOK, true},
		{"delete", http.MethodDelete, "/api/todos/1", http.StatusNoContent, true},
		{"move", http.MethodPost, "/api/todos/1/move", http.StatusOK, true},
		{"get does not poke", http.MethodGet, "/api/todos", http.StatusOK, false},
		{"failed mutation does not poke", http.MethodPost, "/api/todos", http.StatusConflict, false},
		{"schedule parse does not poke", http.MethodPost, "/api/schedule/parse", http.StatusOK, false},
		{"schedule extract does not poke", http.MethodPost, "/api/schedule/extract", http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(nil, "")
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})
			ch := h.bus.subscribe()
			h.notifyMutations(inner).ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(tt.method, tt.path, nil))
			select {
			case <-ch:
				if !tt.wantPoke {
					t.Error("got poke, want none")
				}
			default:
				if tt.wantPoke {
					t.Error("missing poke")
				}
			}
		})
	}
}

func TestStreamEvents(t *testing.T) {
	// Shrink the heartbeat so the test observes a ping without waiting 20s.
	// It must be a named event (not a comment): the client's staleness
	// watchdog listens for it, and comment lines never dispatch in
	// EventSource.
	oldHeartbeat := eventHeartbeat
	eventHeartbeat = 10 * time.Millisecond
	t.Cleanup(func() { eventHeartbeat = oldHeartbeat })

	h := New(nil, "")
	srv := httptest.NewServer(http.HandlerFunc(h.streamEvents))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}

	// Lines arrive one SSE event at a time; read until a line matches.
	lines := bufio.NewScanner(resp.Body)
	await := func(want string) {
		t.Helper()
		for lines.Scan() {
			if lines.Text() == want {
				return
			}
		}
		t.Fatalf("stream ended before %q", want)
	}
	await("retry: 3000")
	await(": connected")

	// A broadcast must surface as a sync event on the open stream.
	h.bus.broadcast()
	await("event: sync")
	await("data: {}")

	// The heartbeat must arrive as an observable named event.
	await("event: ping")

	// Cancelling the request ends the handler; the client slot is freed.
	cancel()
	h.bus.broadcast()
	waitFor(t, time.Second, func() bool {
		h.bus.mu.Lock()
		defer h.bus.mu.Unlock()
		return len(h.bus.subs) == 0
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

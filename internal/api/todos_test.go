package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greboid/todo/internal/db"
	"github.com/greboid/todo/internal/models"
)

// openTestHandler opens a throwaway SQLite-backed API handler. A fresh
// database always has the seeded board with id 1.
func openTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := db.New(context.Background(), "sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return New(store, "").Routes()
}

func TestCreateTodoCreatedAt(t *testing.T) {
	h := openTestHandler(t)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/todos", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w := post(`{"boardId":1,"title":"bad","createdAt":"not-a-timestamp"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed createdAt: status = %d, want %d (body %s)", w.Code, http.StatusBadRequest, w.Body)
	}

	w = post(`{"boardId":1,"title":"explicit","createdAt":"2026-01-02T03:04:05+02:00"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("explicit createdAt: status = %d, want %d (body %s)", w.Code, http.StatusCreated, w.Body)
	}
	var got models.Todo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := "2026-01-02T01:04:05Z"; got.CreatedAt != want {
		t.Errorf("explicit createdAt = %q, want canonical UTC %q", got.CreatedAt, want)
	}

	w = post(`{"boardId":1,"title":"default"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("default createdAt: status = %d, want %d (body %s)", w.Code, http.StatusCreated, w.Body)
	}
	got = models.Todo{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CreatedAt == "" {
		t.Error("default createdAt is empty, want stamped now")
	}
	if got.CompletedAt != "" {
		t.Errorf("new todo completedAt = %q, want empty", got.CompletedAt)
	}
}

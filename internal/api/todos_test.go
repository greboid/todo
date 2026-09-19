package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
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

func TestCreateTodoDefaultDue(t *testing.T) {
	store, err := db.New(context.Background(), "sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h := New(store, "").WithDefaultDue("every monday").Routes()

	post := func(body string) (*httptest.ResponseRecorder, models.Todo) {
		req := httptest.NewRequest(http.MethodPost, "/api/todos", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var todo models.Todo
		if w.Code == http.StatusCreated {
			if err := json.Unmarshal(w.Body.Bytes(), &todo); err != nil {
				t.Fatalf("decode response: %v", err)
			}
		}
		return w, todo
	}

	// No schedule on the request: the default applies.
	w, got := post(`{"boardId":1,"title":"defaulted"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create without schedule: status = %d, want %d (body %s)", w.Code, http.StatusCreated, w.Body)
	}
	if got.DueDate == "" {
		t.Error("dueDate is empty, want stamped from -default-due")
	}
	if got.Recurrence == nil || got.Recurrence.Frequency != "weekly" || len(got.Recurrence.Weekdays) != 1 {
		t.Errorf("recurrence = %+v, want weekly on one weekday", got.Recurrence)
	}

	// An explicit due date wins over the default.
	w, got = post(`{"boardId":1,"title":"explicit","dueDate":"2026-03-04"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create with explicit dueDate: status = %d, want %d (body %s)", w.Code, http.StatusCreated, w.Body)
	}
	if got.DueDate != "2026-03-04" {
		t.Errorf("dueDate = %q, want 2026-03-04 (the explicit value)", got.DueDate)
	}
	if got.Recurrence != nil {
		t.Errorf("recurrence = %+v, want none", got.Recurrence)
	}

	// An explicit noSchedule opt-out skips the default.
	w, got = post(`{"boardId":1,"title":"opted out","noSchedule":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create with noSchedule: status = %d, want %d (body %s)", w.Code, http.StatusCreated, w.Body)
	}
	if got.DueDate != "" {
		t.Errorf("dueDate = %q, want empty despite -default-due", got.DueDate)
	}
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

func TestListTodosSiblingOrder(t *testing.T) {
	h := openTestHandler(t)
	create := func(title, date string, parent *int64, position int) models.Todo {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"boardId": 1, "title": title, "dueDate": date,
			"parentId": parent, "position": position,
		})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/todos", strings.NewReader(string(body))))
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
		var todo models.Todo
		if err := json.Unmarshal(w.Body.Bytes(), &todo); err != nil {
			t.Fatal(err)
		}
		return todo
	}
	// Creation order deliberately differs from manual position order; equal
	// dates exercise stable ties in that manual order.
	a := create("a", "2026-08-20", nil, 0)
	b := create("b", "2026-08-10", nil, 0)
	c := create("c", "2026-08-10", nil, 1)
	x := create("x", "2026-08-20", &a.ID, 0)
	y := create("y", "2026-08-10", &a.ID, 0)
	z := create("z", "2026-08-10", &a.ID, 1)
	grandchild := create("grandchild", "2026-08-01", &x.ID, 4)
	// Insertion shifted the first-created siblings to the end.
	a.Position, x.Position = 2, 2
	for _, tc := range []struct {
		name, query     string
		roots, children []int64
		sorted          bool
	}{
		{"default", "", []int64{b.ID, c.ID, a.ID}, []int64{y.ID, z.ID, x.ID}, false},
		{"filter without sort", "has:date", []int64{b.ID, c.ID, a.ID}, []int64{y.ID, z.ID, x.ID}, false},
		{"explicit sort", "sort:!date", []int64{a.ID, b.ID, c.ID}, []int64{x.ID, y.ID, z.ID}, true},
		{"default after sort", "", []int64{b.ID, c.ID, a.ID}, []int64{y.ID, z.ID, x.ID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/todos?boardId=1&filter="+url.QueryEscape(tc.query), nil))
			if w.Code != http.StatusOK {
				t.Fatalf("list: %d %s", w.Code, w.Body)
			}
			var todos []models.Todo
			if err := json.Unmarshal(w.Body.Bytes(), &todos); err != nil {
				t.Fatal(err)
			}
			groups := map[int64][]int64{}
			original := map[int64]models.Todo{}
			for _, todo := range []models.Todo{a, b, c, x, y, z, grandchild} {
				original[todo.ID] = todo
			}
			if len(todos) != len(original) {
				t.Fatalf("got %d todos, want %d", len(todos), len(original))
			}
			for _, todo := range todos {
				before, ok := original[todo.ID]
				if !ok || !reflect.DeepEqual(todo.ParentID, before.ParentID) {
					t.Fatalf("unexpected hierarchy: %+v", todo)
				}
				parent := int64(0)
				if todo.ParentID != nil {
					parent = *todo.ParentID
				}
				position := before.Position
				if tc.sorted {
					position = len(groups[parent])
				}
				if todo.Position != position {
					t.Errorf("todo %d position = %d, want %d", todo.ID, todo.Position, position)
				}
				groups[parent] = append(groups[parent], todo.ID)
			}
			for parent, want := range map[int64][]int64{0: tc.roots, a.ID: tc.children, x.ID: {grandchild.ID}} {
				if got := groups[parent]; !reflect.DeepEqual(got, want) {
					t.Errorf("parent %d slice order = %v, want %v", parent, got, want)
				}
			}
		})
	}
}

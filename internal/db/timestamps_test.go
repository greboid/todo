package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/greboid/todo/internal/models"
)

// mustBoard creates a fresh board and returns its id.
func mustBoard(t *testing.T, d *DB) int64 {
	t.Helper()
	b, err := d.CreateBoard(context.Background(), models.CreateBoard{Name: "Test"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	return b.ID
}

// mustTimestamp parses an RFC3339 timestamp, failing the test on a bad value.
func mustTimestamp(t *testing.T, what, v string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatalf("%s: %q is not RFC3339: %v", what, v, err)
	}
	return ts
}

// forceCompletedAt overwrites a todo's completed_at directly, bypassing the
// stamping logic, so tests can pin a known value.
func forceCompletedAt(t *testing.T, d *DB, id int64, v string) {
	t.Helper()
	if _, err := d.db.NewUpdate().TableExpr("todos").
		Set("completed_at = ?", toNullString(&v)).
		Where("id = ?", id).Exec(context.Background()); err != nil {
		t.Fatalf("force completed_at: %v", err)
	}
}

func TestCreateStampsCreatedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	bid := mustBoard(t, d)

	before := time.Now().UTC().Add(-time.Minute)
	got, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "default"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	after := time.Now().UTC().Add(time.Minute)
	ts := mustTimestamp(t, "default createdAt", got.CreatedAt)
	if ts.Before(before) || ts.After(after) {
		t.Errorf("default createdAt = %v, want within [%v, %v]", ts, before, after)
	}

	explicit := "2020-06-01T12:00:00Z"
	got, err = d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "explicit", CreatedAt: &explicit})
	if err != nil {
		t.Fatalf("create with explicit createdAt: %v", err)
	}
	if got.CreatedAt != explicit {
		t.Errorf("explicit createdAt = %q, want %q", got.CreatedAt, explicit)
	}
	if got.CompletedAt != "" {
		t.Errorf("new todo completedAt = %q, want empty", got.CompletedAt)
	}
}

func TestCompletedAtStamping(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	bid := mustBoard(t, d)

	root, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	child, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "child", ParentID: &root.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	wantCreatedAt := map[int64]string{root.ID: root.CreatedAt, child.ID: child.CreatedAt}
	check := func(id int64, wantCompletedAt, what string) {
		t.Helper()
		got, err := d.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", what, err)
		}
		if got.CompletedAt != wantCompletedAt {
			t.Errorf("%s: completedAt = %q, want %q", what, got.CompletedAt, wantCompletedAt)
		}
		if got.CreatedAt != wantCreatedAt[id] {
			t.Errorf("%s: createdAt changed: got %q, want %q", what, got.CreatedAt, wantCreatedAt[id])
		}
	}

	// Completing cascades the timestamp to descendants alongside the flag.
	if _, err := d.SetCompleted(ctx, root.ID, true); err != nil {
		t.Fatalf("complete root: %v", err)
	}
	for _, id := range []int64{root.ID, child.ID} {
		got, err := d.Get(ctx, id)
		if err != nil {
			t.Fatalf("get after complete: %v", err)
		}
		mustTimestamp(t, "completedAt", got.CompletedAt)
	}

	// Re-completing keeps the first completion time: the cascade must not
	// re-stamp descendants that were already completed.
	old := "2020-01-01T00:00:00Z"
	forceCompletedAt(t, d, root.ID, old)
	forceCompletedAt(t, d, child.ID, old)
	if _, err := d.SetCompleted(ctx, root.ID, true); err != nil {
		t.Fatalf("re-complete root: %v", err)
	}
	check(root.ID, old, "root re-completed")
	check(child.ID, old, "child re-completed")

	// Un-completing clears the timestamp on the whole subtree.
	if _, err := d.SetCompleted(ctx, root.ID, false); err != nil {
		t.Fatalf("un-complete root: %v", err)
	}
	check(root.ID, "", "root un-completed")
	check(child.ID, "", "child un-completed")

	// The PATCH completed path stamps and clears the same way.
	yes, no := true, false
	if _, err := d.Update(ctx, child.ID, models.UpdateTodo{Completed: &yes}); err != nil {
		t.Fatalf("patch child completed: %v", err)
	}
	got, err := d.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get patched child: %v", err)
	}
	mustTimestamp(t, "patched completedAt", got.CompletedAt)
	forceCompletedAt(t, d, child.ID, old)
	if _, err := d.Update(ctx, child.ID, models.UpdateTodo{Completed: &yes}); err != nil {
		t.Fatalf("re-patch child completed: %v", err)
	}
	check(child.ID, old, "child re-patched")
	if _, err := d.Update(ctx, child.ID, models.UpdateTodo{Completed: &no}); err != nil {
		t.Fatalf("patch child un-completed: %v", err)
	}
	check(child.ID, "", "child patched un-completed")
}

func TestRecurringCloneTimestamps(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	bid := mustBoard(t, d)

	due := "2030-01-01"
	src, err := d.Create(ctx, models.CreateTodo{
		BoardID:    &bid,
		Title:      "recur",
		DueDate:    &due,
		Recurrence: &models.Recurrence{Frequency: "daily", Interval: 1},
	})
	if err != nil {
		t.Fatalf("create recurring todo: %v", err)
	}
	if _, err := d.SetCompleted(ctx, src.ID, true); err != nil {
		t.Fatalf("complete recurring todo: %v", err)
	}

	all, err := d.ListAll(ctx, bid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var clone *models.Todo
	for i := range all {
		if all[i].Title == "recur" && !all[i].Completed {
			clone = &all[i]
		}
	}
	if clone == nil {
		t.Fatal("no incomplete clone spawned")
	}
	mustTimestamp(t, "clone createdAt", clone.CreatedAt)
	if clone.CompletedAt != "" {
		t.Errorf("clone completedAt = %q, want empty", clone.CompletedAt)
	}
	got, err := d.Get(ctx, src.ID)
	if err != nil {
		t.Fatalf("get completed original: %v", err)
	}
	mustTimestamp(t, "original completedAt", got.CompletedAt)
}

// TestTimestampBackfillMigration simulates rows predating the timestamp
// columns (NULL timestamps) and reopens the database: migrate must backfill
// createdAt everywhere and completedAt on completed rows only.
func TestTimestampBackfillMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	d, err := New(ctx, "sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	bid := mustBoard(t, d)
	openTodo, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "open"})
	if err != nil {
		t.Fatalf("create open: %v", err)
	}
	doneTodo, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: "done"})
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	if _, err := d.db.NewUpdate().TableExpr("todos").
		Set("created_at = ?", nil).
		Set("completed = ?", 1).
		Set("completed_at = ?", nil).
		Where("id = ?", doneTodo.ID).Exec(ctx); err != nil {
		t.Fatalf("age done row: %v", err)
	}
	if _, err := d.db.NewUpdate().TableExpr("todos").
		Set("created_at = ?", nil).
		Where("id = ?", openTodo.ID).Exec(ctx); err != nil {
		t.Fatalf("age open row: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	d, err = New(ctx, "sqlite", path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	got, err := d.Get(ctx, openTodo.ID)
	if err != nil {
		t.Fatalf("get open: %v", err)
	}
	mustTimestamp(t, "backfilled open createdAt", got.CreatedAt)
	if got.CompletedAt != "" {
		t.Errorf("open todo completedAt = %q, want empty (backfill only touches completed rows)", got.CompletedAt)
	}
	got, err = d.Get(ctx, doneTodo.ID)
	if err != nil {
		t.Fatalf("get done: %v", err)
	}
	mustTimestamp(t, "backfilled done createdAt", got.CreatedAt)
	mustTimestamp(t, "backfilled done completedAt", got.CompletedAt)
}

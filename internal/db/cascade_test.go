package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/greboid/todo/internal/models"
)

// openTestDB opens a throwaway SQLite database in a temp directory, matching
// the production driver setup.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := New(context.Background(), "sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestSetCompletedCascade covers the completion cascade the client's offline
// queue depends on: replaying a parent completion must leave the server
// already holding the child's outcome, so the child's own queued completion
// is an agreeing no-op instead of a conflict (see web/src/lib/offline.test.js).
func TestSetCompletedCascade(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	board, err := d.CreateBoard(ctx, models.CreateBoard{Name: "Test"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	bid := board.ID
	mustCreate := func(title string, parent *int64) models.Todo {
		t.Helper()
		todo, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: title, ParentID: parent})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return todo
	}

	root := mustCreate("root", nil)
	child := mustCreate("child", &root.ID)
	grand := mustCreate("grand", &child.ID)
	other := mustCreate("other", nil)         // a different root todo…
	otherChild := mustCreate("oc", &other.ID) // …whose subtree must stay untouched

	wantCompleted := func(id int64, want bool, what string) {
		t.Helper()
		got, err := d.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", what, err)
		}
		if got.Completed != want {
			t.Errorf("%s: completed = %v, want %v", what, got.Completed, want)
		}
	}

	if _, err := d.SetCompleted(ctx, root.ID, true); err != nil {
		t.Fatalf("complete root: %v", err)
	}
	wantCompleted(root.ID, true, "root")
	wantCompleted(child.ID, true, "child")
	wantCompleted(grand.ID, true, "grandchild")
	wantCompleted(other.ID, false, "unrelated root")
	wantCompleted(otherChild.ID, false, "unrelated child")

	// Un-completing cascades to the subtree exactly the same way.
	if _, err := d.SetCompleted(ctx, root.ID, false); err != nil {
		t.Fatalf("un-complete root: %v", err)
	}
	wantCompleted(root.ID, false, "root after un-complete")
	wantCompleted(child.ID, false, "child after un-complete")
	wantCompleted(grand.ID, false, "grandchild after un-complete")
	wantCompleted(other.ID, false, "unrelated root after un-complete")

	if _, err := d.SetCompleted(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("complete missing todo: err = %v, want ErrNotFound", err)
	}
}

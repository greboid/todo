package db

import (
	"context"
	"errors"
	"testing"

	"github.com/greboid/todo/internal/models"
)

// TestMoveToBoard covers the board-move half of Move: the todo is uprooted to
// the target board's root todos, the whole subtree follows it (subtasks always
// live on their parent's board), and the old board's siblings close the gap.
func TestMoveToBoard(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	b1, err := d.CreateBoard(ctx, models.CreateBoard{Name: "One"})
	if err != nil {
		t.Fatalf("create board 1: %v", err)
	}
	b2, err := d.CreateBoard(ctx, models.CreateBoard{Name: "Two"})
	if err != nil {
		t.Fatalf("create board 2: %v", err)
	}
	mustCreate := func(board int64, title string, parent *int64) models.Todo {
		t.Helper()
		bid := board
		todo, err := d.Create(ctx, models.CreateTodo{BoardID: &bid, Title: title, ParentID: parent})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return todo
	}

	root := mustCreate(b1.ID, "root", nil)
	other := mustCreate(b1.ID, "other", nil) // sibling behind root on board 1
	child := mustCreate(b1.ID, "child", &root.ID)
	grand := mustCreate(b1.ID, "grand", &child.ID)
	anchor := mustCreate(b2.ID, "anchor", nil) // existing root on board 2

	wantBoard := func(id, board int64, what string) models.Todo {
		t.Helper()
		got, err := d.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", what, err)
		}
		if got.BoardID != board {
			t.Errorf("%s: boardId = %d, want %d", what, got.BoardID, board)
		}
		return got
	}

	// Root-to-root across boards: appended after board 2's existing roots.
	moved, err := d.Move(ctx, root.ID, models.MoveTodo{
		OptionalParent: models.OptionalParent{Set: true}, // parentId null
		BoardID:        &b2.ID,
	})
	if err != nil {
		t.Fatalf("move root to board 2: %v", err)
	}
	if moved.ParentID != nil {
		t.Errorf("moved root: parentId = %v, want nil", *moved.ParentID)
	}
	if moved.Position != anchor.Position+1 {
		t.Errorf("moved root: position = %d, want %d (appended after board 2 roots)", moved.Position, anchor.Position+1)
	}
	wantBoard(root.ID, b2.ID, "moved root")
	wantBoard(child.ID, b2.ID, "child following the move")
	wantBoard(grand.ID, b2.ID, "grandchild following the move")
	if got := wantBoard(other.ID, b1.ID, "old-board sibling"); got.Position != 0 {
		t.Errorf("old-board sibling: position = %d, want 0 (gap closed)", got.Position)
	}
	if c := wantBoard(child.ID, b2.ID, "child parent"); c.ParentID == nil || *c.ParentID != root.ID {
		t.Errorf("child: parentId = %v, want %d (nesting kept)", c.ParentID, root.ID)
	}

	// Moving a subtask to another board uproots it to that board's roots;
	// its own subtree still follows.
	uprooted, err := d.Move(ctx, child.ID, models.MoveTodo{BoardID: &b1.ID})
	if err != nil {
		t.Fatalf("move child to board 1: %v", err)
	}
	if uprooted.ParentID != nil {
		t.Errorf("uprooted child: parentId = %v, want nil", *uprooted.ParentID)
	}
	if uprooted.Position != 1 {
		t.Errorf("uprooted child: position = %d, want 1 (after board 1's remaining root)", uprooted.Position)
	}
	wantBoard(child.ID, b1.ID, "uprooted child")
	wantBoard(grand.ID, b1.ID, "grandchild following the uproot")

	// A board that does not exist is the 404-shaped sentinel.
	missing := int64(9999)
	if _, err := d.Move(ctx, root.ID, models.MoveTodo{BoardID: &missing}); !errors.Is(err, ErrBoardNotFound) {
		t.Errorf("move to missing board: err = %v, want ErrBoardNotFound", err)
	}
}

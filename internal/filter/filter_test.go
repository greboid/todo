package filter

import (
	"reflect"
	"strings"
	"testing"
)

const today = "2026-08-10"

// sampleBoard builds a small board covering every filter dimension. Item 2 is a
// child of item 1, so any filter matching item 2 also surfaces item 1 as
// ancestor context (the tree must stay connected):
//
//	root (completed, no date, no label)        id 1
//	└─ open-labeled (label:work, no date)       id 2   child of 1
//	recurring (recurrence, due today)          id 3
//	overdue-urgent (label:urgent, due -5d)     id 4
//	plain (no date, no label)                  id 5
//	far-future (label:work, due +30d)          id 6
func sampleBoard() []Item {
	root := int64(1)
	return []Item{
		{ID: 1, Title: "Done task", Completed: true},
		{ID: 2, ParentID: &root, Title: "Open with label", Labels: []string{"work"}},
		{ID: 3, Title: "Recurring open", HasRecurrence: true, DueDate: today},
		{ID: 4, Title: "Overdue open", Labels: []string{"urgent"}, DueDate: "2026-08-05"},
		{ID: 5, Title: "No date no label"},
		{ID: 6, Title: "Far future", Labels: []string{"work"}, DueDate: "2026-09-09"},
	}
}

// ids extracts the visible IDs in order (nil for none).
func ids(items []Item) []int64 {
	if len(items) == 0 {
		return nil
	}
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestApply(t *testing.T) {
	board := sampleBoard()
	// Note: item 2 is the only nested todo; whenever it matches, its parent
	// (item 1) appears as ancestor context — hence item 1 in those expectations.
	tests := []struct {
		name   string
		query  string
		want   []int64
		wantEx bool // expects a parse error
	}{
		// has: existence filters
		{"has complete", "has:complete", []int64{1}, false},
		{"not complete (default)", "!has:complete", []int64{1, 2, 3, 4, 5, 6}, false},
		{"has label", "has:label", []int64{1, 2, 4, 6}, false},
		{"not label", "!has:label", []int64{1, 3, 5}, false},
		{"has recur", "has:recur", []int64{3}, false},
		{"has date", "has:date", []int64{3, 4, 6}, false},
		{"not date", "!has:date", []int64{1, 2, 5}, false},

		// label: positive (OR) and negation (each excludes)
		{"label work", "label:work", []int64{1, 2, 6}, false},
		{"not label work", "!label:work", []int64{1, 3, 4, 5}, false},

		// date presets. "week" includes no-date todos (legacy semantics).
		{"date week", "date:week", []int64{1, 2, 3, 4, 5}, false},
		{"not date week", "!date:week", []int64{6}, false},
		{"date overdue", "date:overdue", []int64{4}, false},
		{"date today", "date:today", []int64{3}, false},
		{"date tomorrow", "date:tomorrow", nil, false},
		{"date none", "date:none", []int64{1, 2, 5}, false},
		{"date exact", "date:2026-08-05", []int64{4}, false},
		{"date range", "date:2026-08-01..2026-08-31", []int64{3, 4}, false},

		// free-text search (title + description, case-insensitive)
		{"text match", "future", []int64{6}, false},
		{"text no match", "nonexistent", nil, false},

		// compound (AND) — incomplete AND labeled
		{"compound", "!has:complete has:label", []int64{1, 2, 4, 6}, false},

		// parse errors are surfaced, not dropped
		{"bad date", "date:foo", nil, true},
		{"bad has", "has:banana", nil, true},

		// empty query returns everything
		{"empty", "", []int64{1, 2, 3, 4, 5, 6}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Parse(tc.query)
			if tc.wantEx {
				if err == nil {
					t.Fatalf("Parse(%q): expected error, got nil", tc.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.query, err)
			}
			if got := ids(Apply(board, q, today)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Apply(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// Ancestor context in isolation: "label:work date:none" matches only item 2
// (item 6 has a due date and is excluded), so the visible set is exactly item 2
// plus its parent (item 1) as context.
func TestApplyAncestorContext(t *testing.T) {
	q, err := Parse("label:work date:none")
	if err != nil {
		t.Fatal(err)
	}
	got := ids(Apply(sampleBoard(), q, today))
	want := []int64{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Negating "has:complete" while searching for text only a completed todo has
// yields nothing — proving AND composition and negation compose.
func TestApplyCompletedHiddenByDefault(t *testing.T) {
	q, err := Parse("!has:complete Done")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(Apply(sampleBoard(), q, today)); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"date:foo", "invalid date filter"},
		{"has:xyz", "invalid has filter"},
	} {
		_, err := Parse(tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want error containing %q", tc.query, err, tc.want)
		}
	}
}

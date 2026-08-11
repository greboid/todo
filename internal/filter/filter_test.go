package filter

import (
	"reflect"
	"sort"
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
//	high-priority (priority:high)              id 7
//	low-priority (priority:low)                id 8
func sampleBoard() []Item {
	root := int64(1)
	return []Item{
		{ID: 1, Title: "Done task", Completed: true},
		{ID: 2, ParentID: &root, Title: "Open with label", Labels: []string{"work"}},
		{ID: 3, Title: "Recurring open", HasRecurrence: true, DueDate: today},
		{ID: 4, Title: "Overdue open", Labels: []string{"urgent"}, DueDate: "2026-08-05"},
		{ID: 5, Title: "No date no label"},
		{ID: 6, Title: "Far future", Labels: []string{"work"}, DueDate: "2026-09-09"},
		{ID: 7, Title: "High priority task", Priority: "high"},
		{ID: 8, Title: "Low priority task", Priority: "low"},
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
		{"not complete (default)", "!has:complete", []int64{1, 2, 3, 4, 5, 6, 7, 8}, false},
		{"has label", "has:label", []int64{1, 2, 4, 6}, false},
		{"not label", "!has:label", []int64{1, 3, 5, 7, 8}, false},
		{"has recur", "has:recur", []int64{3}, false},
		{"has date", "has:date", []int64{3, 4, 6}, false},
		{"not date", "!has:date", []int64{1, 2, 5, 7, 8}, false},

		// label: positive (OR) and negation (each excludes)
		{"label work", "label:work", []int64{1, 2, 6}, false},
		{"not label work", "!label:work", []int64{1, 3, 4, 5, 7, 8}, false},

		// priority: positive (OR) and negation. Items without a priority are
		// excluded by a positive priority filter.
		{"priority high", "priority:high", []int64{7}, false},
		{"priority low", "priority:low", []int64{8}, false},
		{"priority OR", "priority:high priority:low", []int64{7, 8}, false},
		{"not priority high", "!priority:high", []int64{1, 2, 3, 4, 5, 6, 8}, false},
		{"has priority", "has:priority", []int64{7, 8}, false},
		{"not has priority", "!has:priority", []int64{1, 2, 3, 4, 5, 6}, false},

		// date presets. "week" includes no-date todos (legacy semantics).
		{"date week", "date:week", []int64{1, 2, 3, 4, 5, 7, 8}, false},
		{"not date week", "!date:week", []int64{6}, false},
		{"date overdue", "date:overdue", []int64{4}, false},
		{"date today", "date:today", []int64{3}, false},
		{"date tomorrow", "date:tomorrow", nil, false},
		{"date none", "date:none", []int64{1, 2, 5, 7, 8}, false},
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
		// multiple date filters are now an error rather than silently overwriting
		{"two dates", "date:today date:tomorrow", nil, true},

		// date OR / AND expressions inside a single date: qualifier
		{"date or", "date:\"overdue or today\"", []int64{3, 4}, false},
		{"date and", "date:\"today and week\"", []int64{3}, false},
		// "overdue and week" is empty because the overdue item (08-05) predates
		// this-week scope; combined with "today" via OR it surfaces both.
		{"date or and", "date:\"today and week or overdue\"", []int64{3, 4}, false},
		// OR of two mutually-exclusive presets hits both days.
		{"date today or tomorrow", "date:\"today or tomorrow\"", []int64{3}, false},

		// empty query returns everything
		{"empty", "", []int64{1, 2, 3, 4, 5, 6, 7, 8}, false},
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
		{"sort:banana", "invalid sort field"},
	} {
		_, err := Parse(tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want error containing %q", tc.query, err, tc.want)
		}
	}
}

// rootOrder returns the root items' IDs in ascending Position order after a
// sort. Since Sort reorders via Position (not flat slice order), this is the
// correct way to observe the rendered order.
func rootOrder(board []Item, q Query) []int64 {
	out := Sort(Apply(board, q, today), q)
	roots := make([]Item, 0, len(out))
	for _, it := range out {
		if it.ParentID == nil {
			roots = append(roots, it)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].Position < roots[j].Position })
	r := make([]int64, len(roots))
	for i, it := range roots {
		r[i] = it.ID
	}
	return r
}

// TestSortOrder verifies each sort field orders roots ascending, with empty
// values (no priority/label/date) landing last.
func TestSortOrder(t *testing.T) {
	board := sampleBoard()

	q, err := Parse("sort:priority")
	if err != nil {
		t.Fatal(err)
	}
	// Priorities present: high(7), low(8); empties sort last: 1,3,4,5,6.
	if got := rootOrder(board, q); !reflect.DeepEqual(got, []int64{7, 8, 1, 3, 4, 5, 6}) {
		t.Errorf("sort:priority = %v", got)
	}

	q, err = Parse("sort:date")
	if err != nil {
		t.Fatal(err)
	}
	// Dated roots ascending: 4(08-05),3(08-10),6(09-09); undated last: 1,5,7,8.
	if got := rootOrder(board, q); !reflect.DeepEqual(got, []int64{4, 3, 6, 1, 5, 7, 8}) {
		t.Errorf("sort:date = %v", got)
	}

	q, err = Parse("sort:label")
	if err != nil {
		t.Fatal(err)
	}
	// Labeled roots ascending by first label: urgent(4), work(6); unlabeled
	// last: 1,3,5,7,8.
	if got := rootOrder(board, q); !reflect.DeepEqual(got, []int64{4, 6, 1, 3, 5, 7, 8}) {
		t.Errorf("sort:label = %v", got)
	}
}

// TestSortDesc reverses the comparison via the leading "!".
func TestSortDesc(t *testing.T) {
	q, err := Parse("sort:!priority")
	if err != nil {
		t.Fatal(err)
	}
	// Descending: empties (compared as greatest, then sign-flipped to least)
	// come first, then low(8), high(7).
	got := rootOrder(sampleBoard(), q)
	if !reflect.DeepEqual(got, []int64{1, 3, 4, 5, 6, 8, 7}) {
		t.Errorf("sort:!priority = %v", got)
	}
}

// TestSortMultipleKeys applies sort keys in order: primary date, secondary
// priority as a tiebreaker among the undated roots.
func TestSortMultipleKeys(t *testing.T) {
	q, err := Parse("sort:date sort:priority")
	if err != nil {
		t.Fatal(err)
	}
	// Dated ascending: 4,3,6. Undated group (1,5,7,8) broken by priority:
	// high(7),low(8) before the truly empty (1,5).
	got := rootOrder(sampleBoard(), q)
	if !reflect.DeepEqual(got, []int64{4, 3, 6, 7, 8, 1, 5}) {
		t.Errorf("sort:date sort:priority = %v", got)
	}
}

// TestSortReassignsPosition confirms Sort rewrites Position to the new 0-based
// sibling index so downstream consumers see the new order.
func TestSortReassignsPosition(t *testing.T) {
	q, err := Parse("sort:priority")
	if err != nil {
		t.Fatal(err)
	}
	out := Sort(Apply(sampleBoard(), q, today), q)
	byID := make(map[int64]int, len(out))
	for _, it := range out {
		byID[it.ID] = it.Position
	}
	// Roots: high(7)->0, low(8)->1, then empties 1,3,4,5,6 -> 2..6. Child 2
	// is the only item in its sibling group so its position is 0.
	want := map[int64]int{7: 0, 8: 1, 1: 2, 3: 3, 4: 4, 5: 5, 6: 6, 2: 0}
	if !reflect.DeepEqual(byID, want) {
		t.Errorf("positions = %v, want %v", byID, want)
	}
}

// TestSortNoKeys is a no-op: with no sort tokens the slice comes back unchanged.
func TestSortNoKeys(t *testing.T) {
	q, err := Parse("label:work")
	if err != nil {
		t.Fatal(err)
	}
	in := Apply(sampleBoard(), q, today)
	out := Sort(in, q)
	if !reflect.DeepEqual(in, out) {
		t.Errorf("Sort with no keys should be a no-op")
	}
}

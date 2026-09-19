package db

import (
	"context"
	"reflect"
	"testing"
)

// TestListPrioritiesSeededOrder pins the direction of the priority list: the
// API returns predefined priorities highest precedence first (position 0 =
// highest, per models.Priority), so a fresh database lists high, medium, low.
func TestListPrioritiesSeededOrder(t *testing.T) {
	d := openTestDB(t)
	got, err := d.ListPriorities(context.Background())
	if err != nil {
		t.Fatalf("list priorities: %v", err)
	}
	var names []string
	var positions []int
	for _, p := range got {
		names = append(names, p.Name)
		if p.Position == nil {
			t.Fatalf("priority %q has no position; seeded defaults are predefined", p.Name)
		}
		positions = append(positions, *p.Position)
	}
	if want := []string{"high", "medium", "low"}; !reflect.DeepEqual(names, want) {
		t.Errorf("seeded priorities = %v, want %v", names, want)
	}
	if want := []int{0, 1, 2}; !reflect.DeepEqual(positions, want) {
		t.Errorf("seeded positions = %v, want %v", positions, want)
	}
}

// TestReorderPrioritiesRoundTrip covers the modal's save-order path: the
// client sends names in display order (highest precedence first) and the list
// endpoint must echo that same order back.
func TestReorderPrioritiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	// The modal sends every predefined name in display order (highest
	// precedence first); here the user dragged low to the top.
	if err := d.ReorderPriorities(ctx, []string{"low", "medium", "high"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	got, err := d.ListPriorities(ctx)
	if err != nil {
		t.Fatalf("list priorities: %v", err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if want := []string{"low", "medium", "high"}; !reflect.DeepEqual(names, want) {
		t.Errorf("priorities after reorder = %v, want %v", names, want)
	}
}

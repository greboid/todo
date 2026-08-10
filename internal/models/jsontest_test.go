package models_test

import (
	"encoding/json"
	"testing"

	"github.com/greboid/todo/internal/models"
)

func ptr[T any](v T) *T { return &v }

func TestUpdateTodoUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want models.UpdateTodo
	}{
		{
			name: "scalars and labels",
			raw:  `{"title":"edited","description":"d","labels":["a","b"],"completed":true,"position":2}`,
			want: models.UpdateTodo{
				Title:       ptr("edited"),
				Description: ptr("d"),
				Completed:   ptr(true),
				Position:    ptr(2),
				Labels:      ptr([]string{"a", "b"}),
			},
		},
		{
			name: "parentId value sets OptionalParent",
			raw:  `{"title":"x","parentId":5}`,
			want: models.UpdateTodo{
				Title:          ptr("x"),
				OptionalParent: models.OptionalParent{Set: true, ID: ptr(int64(5))},
			},
		},
		{
			name: "parentId null means move to root",
			raw:  `{"parentId":null}`,
			want: models.UpdateTodo{
				OptionalParent: models.OptionalParent{Set: true},
			},
		},
		{
			name: "absent parentId leaves parent unchanged",
			raw:  `{"title":"y"}`,
			want: models.UpdateTodo{
				Title:          ptr("y"),
				OptionalParent: models.OptionalParent{Set: false},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got models.UpdateTodo
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatal(err)
			}
			if !equalPtrs(got.Title, tc.want.Title) {
				t.Errorf("title: got %v want %v", got.Title, tc.want.Title)
			}
			if !equalPtrs(got.Description, tc.want.Description) {
				t.Errorf("description: got %v want %v", got.Description, tc.want.Description)
			}
			if !equalPtrs(got.Completed, tc.want.Completed) {
				t.Errorf("completed: got %v want %v", got.Completed, tc.want.Completed)
			}
			if !equalPtrs(got.Position, tc.want.Position) {
				t.Errorf("position: got %v want %v", got.Position, tc.want.Position)
			}
			if got.Set != tc.want.Set {
				t.Errorf("Set: got %v want %v", got.Set, tc.want.Set)
			}
			if !equalPtrs(got.ID, tc.want.ID) {
				t.Errorf("ID: got %v want %v", got.ID, tc.want.ID)
			}
		})
	}
}

func TestMoveTodoUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want models.MoveTodo
	}{
		{name: "position only", raw: `{"position":3}`, want: models.MoveTodo{Position: ptr(3)}},
		{name: "parent value", raw: `{"parentId":2,"position":0}`,
			want: models.MoveTodo{OptionalParent: models.OptionalParent{Set: true, ID: ptr(int64(2))}, Position: ptr(0)}},
		{name: "parent null", raw: `{"parentId":null,"position":1}`,
			want: models.MoveTodo{OptionalParent: models.OptionalParent{Set: true}, Position: ptr(1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got models.MoveTodo
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatal(err)
			}
			if !equalPtrs(got.Position, tc.want.Position) {
				t.Errorf("position: got %v want %v", got.Position, tc.want.Position)
			}
			if got.Set != tc.want.Set {
				t.Errorf("Set: got %v want %v", got.Set, tc.want.Set)
			}
			if !equalPtrs(got.ID, tc.want.ID) {
				t.Errorf("ID: got %v want %v", got.ID, tc.want.ID)
			}
		})
	}
}

func equalPtrs[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Package models defines the core domain types for the todo app.
package models

import (
	"encoding/json"
	"time"
)

// Board is a top-level grouping of todos. Each todo belongs to exactly one
// board; the UI shows one board at a time and lets the user switch between
// them via the header bar.
type Board struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// CreateBoard is the payload for creating a new board.
type CreateBoard struct {
	Name     string `json:"name"`
	Position *int   `json:"position,omitempty"`
}

// UpdateBoard is the payload for renaming or reordering a board.
type UpdateBoard struct {
	Name     *string `json:"name,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// CreateLabel is the payload for adding a label to the predefined set.
type CreateLabel struct {
	Name string `json:"name"`
}

// Label is a label with its colour. Colour is a hex string (e.g. "#ef4444")
// or empty when no user-defined colour is set (the client picks from a
// palette deterministically by name).
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// LabelColor pairs a label name with its colour for inline rendering on todos.
type LabelColor struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// UpdateLabel is the payload for changing a label's colour. Colour is a hex
// string; an empty string clears the user-defined colour so the label reverts
// to the auto-assigned palette colour.
type UpdateLabel struct {
	Color *string `json:"color"`
}

// Priority is a priority level with its colour. Colour is a hex string (e.g.
// "#ef4444") or empty when no user-defined colour is set (the client picks
// from a palette deterministically by name). Unlike labels, a todo carries at
// most one priority.
type Priority struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// CreatePriority is the payload for adding a priority to the predefined set.
type CreatePriority struct {
	Name string `json:"name"`
}

// UpdatePriority is the payload for changing a priority's colour. Colour is a
// hex string; an empty string clears the user-defined colour.
type UpdatePriority struct {
	Color *string `json:"color"`
}

// CompleteTodo is the payload for the /complete endpoint.
type CompleteTodo struct {
	Completed bool `json:"completed"`
}

// Recurrence is a Todoist-style repeating schedule. A todo is recurring iff
// its Recurrence is non-nil. Completing a recurring todo spawns a new
// incomplete instance whose due date is advanced by these rules.
//
// The fields cover the date-level Todoist grammar (times are out of scope:
// the app is date-only). All targets are optional; what is set depends on the
// frequency:
//   - daily:    Interval only.
//   - weekly:   Interval; Weekdays restricts to specific weekdays.
//   - monthly:  Interval plus one of MonthDays, LastDay, or NthWeekday. With
//     none set, the base day-of-month is kept.
//   - yearly:   Interval only.
type Recurrence struct {
	Frequency      string      `json:"frequency"`                // "daily"|"weekly"|"monthly"|"yearly"
	Interval       int         `json:"interval"`                 // >= 1 (every N)
	Weekdays       []int       `json:"weekdays,omitempty"`       // weekly: target weekdays, 0=Sun..6=Sat
	MonthDays      []int       `json:"monthDays,omitempty"`      // monthly: target days-of-month 1..31 (e.g. "every 2, 15, 27")
	LastDay        bool        `json:"lastDay,omitempty"`        // monthly: target the last day of the month
	NthWeekday     *NthWeekday `json:"nthWeekday,omitempty"`     // monthly: ordinal weekday, e.g. "every 2nd tuesday"
	EndDate        string      `json:"endDate,omitempty"`        // "YYYY-MM-DD"; recurrence stops after this date
	FromCompletion bool        `json:"fromCompletion,omitempty"` // every! : advance from completion date, not due date
}

// NthWeekday pins a recurrence to an ordinal weekday of the month, e.g. the
// 2nd Tuesday (N=2, Weekday=2) or the last Friday (N=-1, Weekday=5).
type NthWeekday struct {
	N       int `json:"n"`       // 1..5, or -1 for last
	Weekday int `json:"weekday"` // 0=Sun..6=Sat
}

// Valid reports whether the recurrence rule is self-consistent: a known
// frequency, a positive interval, weekday targets in [0,6], month-day targets
// in [1,31], a sane ordinal weekday, and an ISO end date when set. Used by both
// API input validation and the db advance path.
func (r Recurrence) Valid() bool {
	switch r.Frequency {
	case "daily", "weekly", "monthly", "yearly":
	default:
		return false
	}
	if r.Interval < 1 {
		return false
	}
	for _, w := range r.Weekdays {
		if w < 0 || w > 6 {
			return false
		}
	}
	for _, d := range r.MonthDays {
		if d < 1 || d > 31 {
			return false
		}
	}
	if r.NthWeekday != nil {
		n := r.NthWeekday.N
		if (n < 1 || n > 5) && n != -1 {
			return false
		}
		if r.NthWeekday.Weekday < 0 || r.NthWeekday.Weekday > 6 {
			return false
		}
	}
	if r.EndDate != "" {
		if _, err := time.Parse("2006-01-02", r.EndDate); err != nil {
			return false
		}
	}
	return true
}

// Todo represents a single todo item.
type Todo struct {
	ID              int64        `json:"id"`
	BoardID         int64        `json:"boardId"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	Completed       bool         `json:"completed"`
	ParentID        *int64       `json:"parentId,omitempty"`
	Position        int          `json:"position"`
	Labels          []string     `json:"labels,omitempty"`
	LabelColors     []LabelColor `json:"labelColors,omitempty"`
	Priority        string       `json:"priority,omitempty"`        // priority name, "" = none
	PriorityColor   string       `json:"priorityColor,omitempty"`   // hex colour for the priority, "" = auto
	DueDate         string       `json:"dueDate,omitempty"`         // "YYYY-MM-DD", "" = none
	Recurrence      *Recurrence  `json:"recurrence,omitempty"`      // nil = non-recurring
	ScheduleText    string       `json:"scheduleText,omitempty"`    // canonical free-text seed for the edit field (computed, not stored)
	RecurrenceLabel string       `json:"recurrenceLabel,omitempty"` // short badge label, e.g. "2 weeks on Mon, Wed" (computed, not stored)
}

// CreateTodo is the payload for creating a new todo.
type CreateTodo struct {
	BoardID     *int64      `json:"boardId,omitempty"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	ParentID    *int64      `json:"parentId,omitempty"`
	Position    *int        `json:"position,omitempty"`
	Labels      []string    `json:"labels,omitempty"`
	Priority    string      `json:"priority,omitempty"`
	DueDate     *string     `json:"dueDate,omitempty"`
	Recurrence  *Recurrence `json:"recurrence,omitempty"`
}

// OptionalParent captures the three-way distinction encoding/json cannot
// express with a single tagged pointer field when decoding parentId:
//   - absent  -> Set=false (leave parent unchanged)
//   - null    -> Set=true, ID=nil (move to root)
//   - <number> -> Set=true, ID=&value (move under that parent)
//
// Both UpdateTodo and MoveTodo embed it so the wire contract is defined once.
type OptionalParent struct {
	ID  *int64
	Set bool
}

// UnmarshalJSON decodes parentId preserving the absent/null/value distinction.
func (p *OptionalParent) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, present := raw["parentId"]
	if !present {
		return nil // omitted: leave parent unchanged.
	}
	p.Set = true
	if string(v) == "null" {
		return nil // explicit null: move to root.
	}
	var id int64
	if err := json.Unmarshal(v, &id); err != nil {
		return err
	}
	p.ID = &id
	return nil
}

// UpdateTodo is the payload for updating an existing todo.
// All fields are optional; omitted fields are left unchanged.
// Pointer-valued JSON fields let us distinguish "unset" from "zero".
// Parent embeds OptionalParent for the absent/null/value tri-state on parentId.
type UpdateTodo struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Completed   *bool   `json:"completed,omitempty"`
	OptionalParent
	Position      *int        `json:"position,omitempty"`
	Labels        *[]string   `json:"labels,omitempty"`
	Priority      *string     `json:"-"` // value to set; nil+PrioritySet = clear
	PrioritySet   bool        `json:"-"`
	DueDate       *string     `json:"-"` // value to set; nil+DueDateSet = clear
	DueDateSet    bool        `json:"-"`
	Recurrence    *Recurrence `json:"-"` // value to set; nil+RecurrenceSet = clear
	RecurrenceSet bool        `json:"-"`
}

// UnmarshalJSON decodes scalar fields normally and routes parentId through
// OptionalParent so the absent/null/value tri-state is preserved. This is
// required because embedding OptionalParent would otherwise promote its
// UnmarshalJSON and silently drop every other field.
func (u *UpdateTodo) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if v, ok := raw["title"]; ok && string(v) != "null" {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		u.Title = &s
	}
	if v, ok := raw["description"]; ok && string(v) != "null" {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		u.Description = &s
	}
	if v, ok := raw["completed"]; ok && string(v) != "null" {
		var c bool
		if err := json.Unmarshal(v, &c); err != nil {
			return err
		}
		u.Completed = &c
	}
	if v, ok := raw["position"]; ok && string(v) != "null" {
		var p int
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		u.Position = &p
	}
	if v, ok := raw["labels"]; ok && string(v) != "null" {
		var l []string
		if err := json.Unmarshal(v, &l); err != nil {
			return err
		}
		u.Labels = &l
	}
	if v, ok := raw["priority"]; ok {
		u.PrioritySet = true
		if string(v) != "null" {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			u.Priority = &s
		}
	}
	if v, ok := raw["dueDate"]; ok {
		u.DueDateSet = true
		if string(v) != "null" {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			u.DueDate = &s
		}
	}
	if v, ok := raw["recurrence"]; ok {
		u.RecurrenceSet = true
		if string(v) != "null" {
			var rc Recurrence
			if err := json.Unmarshal(v, &rc); err != nil {
				return err
			}
			u.Recurrence = &rc
		}
	}
	return u.OptionalParent.UnmarshalJSON(b)
}

// MoveTodo moves a todo to a new parent and/or position.
// If Position is nil, the item is appended to the end of its sibling group.
// Parent embeds OptionalParent for the absent/null/value tri-state on parentId.
type MoveTodo struct {
	OptionalParent
	Position *int `json:"position,omitempty"`
}

// UnmarshalJSON decodes position and routes parentId through OptionalParent.
// Without this, the promoted UnmarshalJSON would drop the position field.
func (m *MoveTodo) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if v, ok := raw["position"]; ok && string(v) != "null" {
		var p int
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		m.Position = &p
	}
	return m.OptionalParent.UnmarshalJSON(b)
}

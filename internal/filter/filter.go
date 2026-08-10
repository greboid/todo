// Package filter is the single source of truth for the todo list's filter query
// language. It parses free-text qualifiers (label:, date:, has:, and bare
// search terms) into a [Query] and evaluates it against a board's todos,
// preserving tree context by keeping every ancestor of a match visible.
//
// The package has no dependency on the models or db packages: callers map their
// persisted todos into the small [Item] projection. The reference date ("today")
// is supplied by the caller so date presets (week, today, tomorrow) resolve
// deterministically and are unit-testable without touching the system clock.
package filter

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Item is the projection of a todo needed to evaluate a filter.
type Item struct {
	ID            int64
	ParentID      *int64
	Title         string
	Description   string
	Completed     bool
	Labels        []string
	Priority      string // priority name, "" = none
	DueDate       string // "YYYY-MM-DD" or "" (none)
	HasRecurrence bool
}

// Query is a parsed filter expression. All criteria are ANDed; a todo matches
// only when it satisfies every active criterion. Its fields are unexported so
// the grammar stays in one place; use [Parse] and [Query.Match].
type Query struct {
	text          string
	labels        []string // positive label match (OR semantics)
	notLabels     []string // each excludes (AND semantics)
	priorities    []string // positive priority match (OR semantics)
	notPriorities []string // each excludes (AND semantics)
	date          *dateSpec
	dateNeg       bool
	has           map[string]bool // keys: complete, label, recur, date, priority
}

// Empty reports whether the query has no criteria (matches everything).
func (q Query) Empty() bool {
	return q.text == "" && len(q.labels) == 0 && len(q.notLabels) == 0 &&
		len(q.priorities) == 0 && len(q.notPriorities) == 0 &&
		q.date == nil && len(q.has) == 0
}

type dateSpec struct {
	mode     string // week, overdue, nodate, today, tomorrow, custom
	from, to string // custom range bounds (inclusive)
}

// qualifierRe splits the query into [!]key:value tokens and bare text. Groups:
//
//	1: optional leading !
//	2: key (\w+)
//	3: quoted value
//	4: unquoted value
//	5: bare text token
var qualifierRe = regexp.MustCompile(`(!?)(\w+):(?:"([^"]*)"|(\S+))|("[^"]*"|\S+)`)

var (
	rangeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})$`)
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Parse tokenises the query text into a [Query]. label and date accept a leading
// "!" for negation; has accepts one of complete/label/recur/date. Unrecognized
// date or has values return an error so the caller can surface it rather than
// silently dropping the token.
func Parse(input string) (Query, error) {
	q := Query{has: map[string]bool{}}
	input = strings.TrimSpace(input)
	if input == "" {
		return q, nil
	}
	var parts []string
	for _, m := range qualifierRe.FindAllStringSubmatch(input, -1) {
		if m[2] != "" {
			neg := m[1] == "!"
			key := strings.ToLower(m[2])
			val := m[3]
			if val == "" {
				val = m[4]
			}
			switch key {
			case "label", "l":
				if neg {
					q.notLabels = append(q.notLabels, val)
				} else {
					q.labels = append(q.labels, val)
				}
			case "priority", "p":
				if neg {
					q.notPriorities = append(q.notPriorities, val)
				} else {
					q.priorities = append(q.priorities, val)
				}
			case "date", "d":
				d, ok := parseDate(val)
				if !ok {
					return Query{}, fmt.Errorf("invalid date filter %q: try week, overdue, none, today, tomorrow, YYYY-MM-DD, or YYYY-MM-DD..YYYY-MM-DD", val)
				}
				q.date = &d
				q.dateNeg = neg
			case "has":
				switch h := strings.ToLower(val); h {
				case "complete", "label", "recur", "date", "priority":
					q.has[h] = !neg
				default:
					return Query{}, fmt.Errorf("invalid has filter %q: use complete, label, priority, recur, or date", val)
				}
			}
			continue
		}
		parts = append(parts, strings.Trim(m[5], `"`))
	}
	q.text = strings.Join(parts, " ")
	return q, nil
}

// parseDate maps a date qualifier to a predicate descriptor. Returns ok=false
// for unrecognized values so Parse can report them.
func parseDate(v string) (dateSpec, bool) {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "week", "this-week", "next-week":
		return dateSpec{mode: "week"}, true
	case "overdue", "past":
		return dateSpec{mode: "overdue"}, true
	case "none", "nodate", "no-date":
		return dateSpec{mode: "nodate"}, true
	case "today":
		return dateSpec{mode: "today"}, true
	case "tomorrow":
		return dateSpec{mode: "tomorrow"}, true
	default:
		if rng := rangeRe.FindStringSubmatch(s); rng != nil {
			return dateSpec{mode: "custom", from: rng[1], to: rng[2]}, true
		}
		if dateRe.MatchString(s) {
			return dateSpec{mode: "custom", from: s, to: s}, true
		}
		return dateSpec{}, false
	}
}

// addDays returns the ISO date n days after iso, computed from calendar
// components to avoid timezone drift.
func addDays(iso string, n int) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// testDate reports whether the item satisfies the date predicate. ISO dates
// compare correctly as lexicographic strings. "week" passes todos with no due
// date (matching the original client semantics) as well as those within range.
func testDate(it Item, d dateSpec, today, weekEnd string) bool {
	switch d.mode {
	case "week":
		return it.DueDate == "" || it.DueDate <= weekEnd
	case "overdue":
		return it.DueDate != "" && it.DueDate < today
	case "nodate":
		return it.DueDate == ""
	case "today":
		return it.DueDate == today
	case "tomorrow":
		return it.DueDate == addDays(today, 1)
	case "custom":
		if it.DueDate == "" {
			return false
		}
		if d.from != "" && it.DueDate < d.from {
			return false
		}
		if d.to != "" && it.DueDate > d.to {
			return false
		}
		return true
	}
	return true
}

// Match reports whether the item satisfies every active criterion. today is the
// reference ISO date (YYYY-MM-DD); weekEnd is today+7. Label matching is
// case-sensitive, matching stored label names exactly.
func (q Query) Match(it Item, today, weekEnd string) bool {
	if q.text != "" {
		hay := strings.ToLower(it.Title + " " + it.Description)
		if !strings.Contains(hay, strings.ToLower(q.text)) {
			return false
		}
	}
	if len(q.labels) > 0 && !anyLabel(it.Labels, q.labels) {
		return false
	}
	if len(q.notLabels) > 0 && anyLabel(it.Labels, q.notLabels) {
		return false
	}
	if len(q.priorities) > 0 && !containsString(q.priorities, it.Priority) {
		return false
	}
	if len(q.notPriorities) > 0 && containsString(q.notPriorities, it.Priority) {
		return false
	}
	if q.date != nil {
		ok := testDate(it, *q.date, today, weekEnd)
		if (q.dateNeg && ok) || (!q.dateNeg && !ok) {
			return false
		}
	}
	if want, ok := q.has["complete"]; ok && it.Completed != want {
		return false
	}
	if want, ok := q.has["label"]; ok && hasAnyLabel(it) != want {
		return false
	}
	if want, ok := q.has["priority"]; ok && hasPriority(it) != want {
		return false
	}
	if want, ok := q.has["recur"]; ok && it.HasRecurrence != want {
		return false
	}
	if want, ok := q.has["date"]; ok && (it.DueDate != "") != want {
		return false
	}
	return true
}

func anyLabel(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, l := range have {
		set[l] = true
	}
	for _, w := range want {
		if set[w] {
			return true
		}
	}
	return false
}

func hasAnyLabel(it Item) bool { return len(it.Labels) > 0 }

func hasPriority(it Item) bool { return it.Priority != "" }

// containsString reports whether s equals any element of list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Apply evaluates q against a board's todos and returns the subset that should
// be visible: every matching item plus the ancestors that keep the tree
// connected, in the original input order. An empty query returns all items
// unchanged. today is the reference ISO date.
func Apply(items []Item, q Query, today string) []Item {
	if q.Empty() {
		return items
	}
	weekEnd := addDays(today, 7)
	byID := make(map[int64]Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	visible := make(map[int64]bool, len(items))
	for _, it := range items {
		if !q.Match(it, today, weekEnd) {
			continue
		}
		// Mark the match and walk up its ancestor chain so the subtree stays
		// connected. Stop once we reach an already-visible ancestor.
		cur := it
		for {
			if visible[cur.ID] {
				break
			}
			visible[cur.ID] = true
			if cur.ParentID == nil {
				break
			}
			parent, ok := byID[*cur.ParentID]
			if !ok {
				break
			}
			cur = parent
		}
	}
	out := make([]Item, 0, len(visible))
	for _, it := range items {
		if visible[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

package db

import (
	"testing"
	"time"

	"github.com/greboid/todo/internal/models"
)

// TestNextDueDate exercises the recurrence advance engine. The "current" date
// is fixed so daily/weekly/monthly arithmetic is deterministic; cases that
// depend on "today" (FromCompletion, missing date) are checked loosely against
// today's date.
func TestNextDueDate(t *testing.T) {
	cases := []struct {
		name    string
		current string
		rc      models.Recurrence
		want    string // expected next ISO date; "" with recurring=false means ended
		wantRec bool
	}{
		// daily
		{"daily", "2026-08-10", models.Recurrence{Frequency: "daily", Interval: 1}, "2026-08-11", true},
		{"every 3 days", "2026-08-10", models.Recurrence{Frequency: "daily", Interval: 3}, "2026-08-13", true},
		// weekly, no weekdays
		{"weekly", "2026-08-10", models.Recurrence{Frequency: "weekly", Interval: 1}, "2026-08-17", true},
		{"every 2 weeks", "2026-08-10", models.Recurrence{Frequency: "weekly", Interval: 2}, "2026-08-24", true},
		{"every 2 weeks on mon,wed", "2026-08-10", models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{1, 3}}, "2026-08-12", true},
		{"every 2 weeks on mon,wed from wed", "2026-08-12", models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{1, 3}}, "2026-08-24", true},
		// monthly, base day kept
		{"monthly", "2026-01-15", models.Recurrence{Frequency: "monthly", Interval: 1}, "2026-02-15", true},
		{"every 3 months", "2026-01-15", models.Recurrence{Frequency: "monthly", Interval: 3}, "2026-04-15", true},
		// month-end clamp: Jan 31 + 1 month -> Feb 28
		{"monthly jan31", "2026-01-31", models.Recurrence{Frequency: "monthly", Interval: 1}, "2026-02-28", true},
		// monthDay (single)
		{"monthly day 15", "2026-01-10", models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{15}}, "2026-01-15", true},
		// monthDay list: every 2, 15, 27; from Jan 10 -> Jan 15
		{"monthly 2,15,27 from 10", "2026-01-10", models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{2, 15, 27}}, "2026-01-15", true},
		{"monthly 2,15,27 from 27", "2026-01-27", models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{2, 15, 27}}, "2026-02-02", true},
		// last day
		{"monthly last day from 15", "2026-01-15", models.Recurrence{Frequency: "monthly", Interval: 1, LastDay: true}, "2026-01-31", true},
		{"monthly last day feb", "2026-01-31", models.Recurrence{Frequency: "monthly", Interval: 1, LastDay: true}, "2026-02-28", true},
		// nth weekday: 2nd Tuesday. Jan 2026 Tuesdays: 6,13,20,27 -> 2nd = 13.
		{"2nd tuesday jan", "2026-01-01", models.Recurrence{Frequency: "monthly", Interval: 1, NthWeekday: &models.NthWeekday{N: 2, Weekday: 2}}, "2026-01-13", true},
		// last friday of month. Jan 2026 Fridays: 2,9,16,23,30 -> last = 30.
		{"last friday jan", "2026-01-15", models.Recurrence{Frequency: "monthly", Interval: 1, NthWeekday: &models.NthWeekday{N: -1, Weekday: 5}}, "2026-01-30", true},
		// yearly + Feb29 clamp (2024 is leap)
		{"yearly feb29", "2024-02-29", models.Recurrence{Frequency: "yearly", Interval: 1}, "2025-02-28", true},
		// end date: next within window
		{"daily in window", "2026-08-10", models.Recurrence{Frequency: "daily", Interval: 1, EndDate: "2026-08-12"}, "2026-08-11", true},
		// end date: next past end -> no recurrence
		{"daily past end", "2026-08-12", models.Recurrence{Frequency: "daily", Interval: 1, EndDate: "2026-08-12"}, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rec, err := nextDueDate(c.current, c.rc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rec != c.wantRec {
				t.Fatalf("recurring = %v, want %v", rec, c.wantRec)
			}
			if c.wantRec && got != c.want {
				t.Fatalf("next = %q, want %q", got, c.want)
			}
		})
	}
}

// TestNextDueDateFromCompletion checks every! advances from today, not the
// stored (possibly stale) due date.
func TestNextDueDateFromCompletion(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	got, rec, err := nextDueDate("2020-01-01", models.Recurrence{Frequency: "daily", Interval: 1, FromCompletion: true})
	if err != nil || !rec {
		t.Fatalf("unexpected: got=%q rec=%v err=%v", got, rec, err)
	}
	want := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	if got != want {
		t.Fatalf("from-completion next = %q, want %q (today=%s)", got, want, today)
	}
}

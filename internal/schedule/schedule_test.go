package schedule

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/greboid/todo/internal/models"
)

// now is a fixed Sunday so relative date parsing is deterministic.
var now = date(2026, time.August, 9)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func rc(r models.Recurrence) *models.Recurrence { return &r }

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Schedule
		wantErr string // substring; "" means no error expected
	}{
		// empty clears both
		{"empty", "", Schedule{}, ""},
		{"whitespace", "   ", Schedule{}, ""},

		// plain dates
		{"iso", "2026-08-15", Schedule{DueDate: "2026-08-15"}, ""},
		{"slash dmy", "15/8", Schedule{DueDate: "2026-08-15"}, ""},
		{"slash dmy2", "15/8/26", Schedule{DueDate: "2026-08-15"}, ""},
		{"today", "today", Schedule{DueDate: "2026-08-09"}, ""},
		{"tod", "tod", Schedule{DueDate: "2026-08-09"}, ""},
		{"tomorrow", "tomorrow", Schedule{DueDate: "2026-08-10"}, ""},
		{"yesterday", "yesterday", Schedule{DueDate: "2026-08-08"}, ""},
		{"month day", "aug 15", Schedule{DueDate: "2026-08-15"}, ""},
		{"day month", "15 aug", Schedule{DueDate: "2026-08-15"}, ""},
		{"full date", "dec 25 2026", Schedule{DueDate: "2026-12-25"}, ""},
		{"ordinal", "15th", Schedule{DueDate: "2026-08-15"}, ""},
		{"next monday", "next monday", Schedule{DueDate: "2026-08-10"}, ""},
		{"bare monday", "monday", Schedule{DueDate: "2026-08-10"}, ""},
		{"this monday", "this monday", Schedule{DueDate: "2026-08-10"}, ""},
		{"this wednesday", "this wednesday", Schedule{DueDate: "2026-08-12"}, ""},
		{"in 2 weeks", "in 2 weeks", Schedule{DueDate: "2026-08-23"}, ""},
		{"plus days", "5", Schedule{DueDate: "2026-08-14"}, ""},
		{"plus days word", "+3 days", Schedule{DueDate: "2026-08-12"}, ""},
		{"end of month", "end of month", Schedule{DueDate: "2026-08-31"}, ""},
		{"end of aug", "end of aug", Schedule{DueDate: "2026-08-31"}, ""},
		{"this weekend", "this weekend", Schedule{DueDate: "2026-08-15"}, ""},

		// recurrence, bare-keyword shorthand
		{"daily bare", "daily", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1})}, ""},
		{"weekly bare", "weekly", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 1})}, ""},

		// recurrence, explicit
		{"every day", "every day", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1})}, ""},
		{"every 3 days", "every 3 days", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 3})}, ""},
		{"every week", "every week", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 1})}, ""},
		{
			"every 2 weeks on mon wed", "every 2 weeks on mon, wed",
			Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{1, 3}})},
			"",
		},
		{
			"every week on mon wed fri", "every week on mon, wed, fri",
			Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 3, 5}})},
			"",
		},
		{"every weekday", "every weekday", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 2, 3, 4, 5}})}, ""},
		{"weekends", "every weekend", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 1, Weekdays: []int{0, 6}})}, ""},
		{"every other friday", "every other friday", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{5}})}, ""},
		{"every quarter", "every quarter", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 3})}, ""},
		{"every fortnight", "every fortnight", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "weekly", Interval: 2})}, ""},
		{"every month", "every month", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1})}, ""},
		{"every month on the 15th", "every month on the 15th", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{15}})}, ""},
		{"every month on 2 15 27", "every month on 2, 15, 27", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{2, 15, 27}})}, ""},
		{"every month last day", "every month on the last day", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, LastDay: true})}, ""},
		{"every 2nd tuesday", "every 2nd tuesday", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, NthWeekday: &models.NthWeekday{N: 2, Weekday: 2}})}, ""},
		{"last friday", "every last friday", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, NthWeekday: &models.NthWeekday{N: -1, Weekday: 5}})}, ""},
		{"every year", "every year", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "yearly", Interval: 1})}, ""},
		{"every bang", "every! day", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1, FromCompletion: true})}, ""},

		// clauses
		{"starting", "every day starting sep 1", Schedule{DueDate: "2026-09-01", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1})}, ""},
		{"from", "every day from aug 15", Schedule{DueDate: "2026-08-15", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1})}, ""},
		{"ending", "every day ending dec 31", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1, EndDate: "2026-12-31"})}, ""},
		{"until", "every day until dec 31", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1, EndDate: "2026-12-31"})}, ""},
		{"for duration", "every day for 2 weeks", Schedule{DueDate: "2026-08-09", Recurrence: rc(models.Recurrence{Frequency: "daily", Interval: 1, EndDate: "2026-08-23"})}, ""},
		{
			"start and end", "every month on the 15th starting sep 1 ending dec 31",
			Schedule{DueDate: "2026-09-01", Recurrence: rc(models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{15}, EndDate: "2026-12-31"})},
			"",
		},

		// errors
		{"every alone", "every", Schedule{}, `missing repeat pattern`},
		{"every banana", "every banana", Schedule{}, "unrecognized repeat"},
		{"every hour", "every hour", Schedule{}, "times are not supported"},
		{"at 3pm", "at 3pm", Schedule{}, "couldn't read a date"},
		{"garbage", "notadate", Schedule{}, "couldn't read a date"},
		{"day out of range", "every month on the 99th", Schedule{}, "day of month must be 1-31"},
		{"bad qualifier", "every day on monday", Schedule{}, "does not apply to daily"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in, now)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q): want error containing %q, got nil\nschedule=%+v", tc.in, tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Parse(%q): error %q does not contain %q", tc.in, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.in, err)
			}
			if !scheduleEqual(got, tc.want) {
				t.Fatalf("Parse(%q):\n got  %+v\n want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNextWeekday locks in "next <weekday>" as next week's weekday under
// Monday–Sunday weeks: the coming Wednesday until/including Sunday, then a week
// later (coming+7) on Monday/Tuesday when this week's Wednesday is still ahead.
func TestNextWeekday(t *testing.T) {
	// 2026-08-09 is a Sunday; Wednesdays fall on 08-12 and 08-19.
	cases := []struct {
		today string // YYYY-MM-DD
		want  string // expected "next wednesday"
	}{
		{"2026-08-09", "2026-08-12"}, // Sun: coming Wednesday
		{"2026-08-10", "2026-08-19"}, // Mon: this week's Wed is ahead -> +7
		{"2026-08-11", "2026-08-19"}, // Tue: this week's Wed is ahead -> +7
		{"2026-08-12", "2026-08-19"}, // Wed: coming is next week's
		{"2026-08-13", "2026-08-19"}, // Thu
		{"2026-08-14", "2026-08-19"}, // Fri
		{"2026-08-15", "2026-08-19"}, // Sat
		{"2026-08-16", "2026-08-19"}, // Sun: coming Wednesday is next week's
	}
	for _, tc := range cases {
		ref, err := time.Parse("2006-01-02", tc.today)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ParseDate("next wednesday", ref)
		if !ok {
			t.Fatalf("ParseDate(next wednesday, %s): no match", tc.today)
		}
		if got != tc.want {
			t.Errorf("next wednesday from %s: got %s, want %s", tc.today, got, tc.want)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		in   models.Recurrence
		want string
	}{
		{models.Recurrence{Frequency: "daily", Interval: 1}, "day"},
		{models.Recurrence{Frequency: "daily", Interval: 3}, "3 days"},
		{models.Recurrence{Frequency: "weekly", Interval: 1}, "week"},
		{models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{1, 3}}, "2 weeks on Mon, Wed"},
		{models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{15}}, "month on the 15th"},
		{models.Recurrence{Frequency: "monthly", Interval: 1, MonthDays: []int{2, 15, 27}}, "month on the 2nd, 15th, 27th"},
		{models.Recurrence{Frequency: "monthly", Interval: 1, LastDay: true}, "month on the last day"},
		{models.Recurrence{Frequency: "monthly", Interval: 1, NthWeekday: &models.NthWeekday{N: 2, Weekday: 2}}, "second Tuesday"},
		{models.Recurrence{Frequency: "monthly", Interval: 2, NthWeekday: &models.NthWeekday{N: -1, Weekday: 5}}, "2 months on the last Friday"},
		{models.Recurrence{Frequency: "yearly", Interval: 1}, "year"},
	}
	for _, tc := range cases {
		if got := Format(tc.in); got != tc.want {
			t.Errorf("Format(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatSchedule(t *testing.T) {
	cases := []struct {
		dueDate string
		rc      *models.Recurrence
		want    string
	}{
		{"", nil, ""},
		{"2026-08-15", nil, "2026-08-15"},
		// due == today omits the "starting" clause
		{"2026-08-09", rc(models.Recurrence{Frequency: "daily", Interval: 1}), "every day"},
		{"2026-09-01", rc(models.Recurrence{Frequency: "daily", Interval: 1}), "every day starting 2026-09-01"},
		{"2026-08-09", rc(models.Recurrence{Frequency: "weekly", Interval: 2, Weekdays: []int{1, 3}, EndDate: "2026-12-31"}), "every 2 weeks on Mon, Wed ending 2026-12-31"},
		{"2026-08-09", rc(models.Recurrence{Frequency: "daily", Interval: 1, FromCompletion: true}), "every! day"},
	}
	for _, tc := range cases {
		if got := FormatSchedule(tc.dueDate, tc.rc, now); got != tc.want {
			t.Errorf("FormatSchedule(%q, %+v) = %q, want %q", tc.dueDate, tc.rc, got, tc.want)
		}
	}
}

// TestRoundTrip ensures FormatSchedule output re-parses to the same schedule,
// so the edit-field seed is stable across edits.
func TestRoundTrip(t *testing.T) {
	inputs := []string{
		"every day",
		"every 2 weeks on mon, wed",
		"every month on the 15th starting sep 1 ending dec 31",
		"every 2nd tuesday",
		"every! week",
		"every month on the last day",
		"2026-12-25",
		"every weekday",
	}
	for _, in := range inputs {
		orig, err := Parse(in, now)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		text := FormatSchedule(orig.DueDate, orig.Recurrence, now)
		again, err := Parse(text, now)
		if err != nil {
			t.Fatalf("re-Parse(%q from %q): %v", text, in, err)
		}
		if !scheduleEqual(orig, again) {
			t.Errorf("round trip mismatch for %q:\n orig %+v\n again %+v", in, orig, again)
		}
	}
}

func scheduleEqual(a, b Schedule) bool {
	if a.DueDate != b.DueDate {
		return false
	}
	if (a.Recurrence == nil) != (b.Recurrence == nil) {
		return false
	}
	if a.Recurrence != nil && !reflect.DeepEqual(*a.Recurrence, *b.Recurrence) {
		return false
	}
	return true
}

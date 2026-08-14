package schedule

import (
	"testing"
	"time"
)

// extractNow anchors relative dates. 2026-08-10 is a Monday, so "tomorrow" is
// 2026-08-11 and the next Friday is 2026-08-14.
var extractNow = time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)

func TestExtract(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		title     string
		labels    []string
		due       string // expected DueDate; "" skips the due-date assertion
		hasRec    bool
		freq      string
		interval  int
		weekdays  []int
		monthDays []int
	}{
		{"title only", "buy milk", "buy milk", nil, "", false, "", 0, nil, nil},
		{"trailing relative date", "buy milk tomorrow", "buy milk", nil, "2026-08-11", false, "", 0, nil, nil},
		{"trailing iso date", "dentist 2026-08-15", "dentist", nil, "2026-08-15", false, "", 0, nil, nil},
		{"trailing month day", "dentist aug 15", "dentist", nil, "2026-08-15", false, "", 0, nil, nil},
		{"weekly recurrence", "submit report every friday", "submit report", nil, "", true, "weekly", 1, []int{5}, nil},
		{"repeat recurrence", "water plants repeat weekly", "water plants", nil, "", true, "weekly", 1, nil, nil},
		{"repeat every recurrence", "call mom repeat every week", "call mom", nil, "", true, "weekly", 1, nil, nil},
		{"monthly recurrence", "pay rent every month on the 1st", "pay rent", nil, "", true, "monthly", 1, nil, []int{1}},
		{"weekly weekday list", "standup every week on mon, wed", "standup", nil, "", true, "weekly", 1, []int{1, 3}, nil},
		{"label stripped", "buy milk #errands tomorrow", "buy milk", []string{"errands"}, "2026-08-11", false, "", 0, nil, nil},
		{"label with recurrence", "pay rent #bills every month on the 1st", "pay rent", []string{"bills"}, "", true, "monthly", 1, nil, []int{1}},
		{"labels deduped case-insensitive", "report #A #a", "report", []string{"A"}, "", false, "", 0, nil, nil},
		{"lenient reject keeps title", "buy aug 15 milk", "buy aug 15 milk", nil, "", false, "", 0, nil, nil},
		{"month word in title", "review the march report", "review the march report", nil, "", false, "", 0, nil, nil},
		{"bare schedule becomes title", "every day", "every day", nil, "", false, "", 0, nil, nil},
		{"single date token becomes title", "tomorrow", "tomorrow", nil, "", false, "", 0, nil, nil},
		{"fortnight date with title", "test in a fortnight", "test", nil, "2026-08-24", false, "", 0, nil, nil},
		{"fortnight alone becomes title", "in a fortnight", "in a fortnight", nil, "", false, "", 0, nil, nil},
		{"spacing collapsed", "buy   milk   tomorrow", "buy milk", nil, "2026-08-11", false, "", 0, nil, nil},
		{"case preserved in title", "Buy Milk Tomorrow", "Buy Milk", nil, "2026-08-11", false, "", 0, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qa, ok := Extract(c.in, extractNow)
			if !ok {
				t.Fatalf("Extract(%q) ok=false, want true", c.in)
			}
			if qa.Title != c.title {
				t.Errorf("title: got %q want %q", qa.Title, c.title)
			}
			if !equalStrings(qa.Labels, c.labels) {
				t.Errorf("labels: got %v want %v", qa.Labels, c.labels)
			}
			hasSched := qa.Schedule.DueDate != "" || qa.Schedule.Recurrence != nil
			if c.due != "" && qa.Schedule.DueDate != c.due {
				t.Errorf("dueDate: got %q want %q", qa.Schedule.DueDate, c.due)
			}
			if c.hasRec {
				if qa.Schedule.Recurrence == nil {
					t.Fatalf("expected a recurrence, got nil")
				}
				rc := *qa.Schedule.Recurrence
				if rc.Frequency != c.freq {
					t.Errorf("frequency: got %q want %q", rc.Frequency, c.freq)
				}
				if rc.Interval != c.interval {
					t.Errorf("interval: got %d want %d", rc.Interval, c.interval)
				}
				if !equalInts(rc.Weekdays, c.weekdays) {
					t.Errorf("weekdays: got %v want %v", rc.Weekdays, c.weekdays)
				}
				if !equalInts(rc.MonthDays, c.monthDays) {
					t.Errorf("monthDays: got %v want %v", rc.MonthDays, c.monthDays)
				}
			} else if c.due == "" && hasSched {
				t.Errorf("expected no schedule, got dueDate=%q recurrence=%+v", qa.Schedule.DueDate, qa.Schedule.Recurrence)
			}
		})
	}
}

func TestExtractEmpty(t *testing.T) {
	for _, in := range []string{"   ", "#onlylabel", "#a #b"} {
		if _, ok := Extract(in, time.Now()); ok {
			t.Errorf("Extract(%q) ok=true, want false (no title)", in)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

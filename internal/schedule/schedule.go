// Package schedule parses Todoist-style free-text due dates and recurrence
// rules into the structured {dueDate, recurrence} form stored on a todo, and
// formats them back to free text. It is the single source of truth for the
// schedule grammar: the frontend calls the API to parse instead of keeping a
// parallel implementation in JavaScript.
//
// Scope is the date-level grammar. Times ("every hour", "at 3pm") are rejected
// with a clear error rather than guessed, because the app is date-only.
package schedule

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/greboid/todo/internal/models"
)

// Schedule is the parsed result of a free-text due/recurrence field. A zero
// DueDate ("") means no due date; a nil Recurrence means non-recurring.
type Schedule struct {
	DueDate    string
	Recurrence *models.Recurrence
}

// ----------------------------------------------------------------------------
// lookup tables
// ----------------------------------------------------------------------------

var months = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May,
	"jun": time.June, "june": time.June,
	"jul": time.July, "july": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "sept": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

var weekdays = map[string]int{
	"sun": 0, "sunday": 0, "mon": 1, "monday": 1, "tue": 2, "tues": 2, "tuesday": 2,
	"wed": 3, "weds": 3, "wednesday": 3, "thu": 4, "thur": 4, "thurs": 4, "thursday": 4,
	"fri": 5, "friday": 5, "sat": 6, "saturday": 6,
}

var (
	weekdayAbbr = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	weekdayFull = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
)

var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}

var ordinalWords = map[string]int{
	"first": 1, "1st": 1, "second": 2, "2nd": 2, "third": 3, "3rd": 3,
	"fourth": 4, "4th": 4, "fifth": 5, "5th": 5, "last": -1,
}

var ordinalWord = map[int]string{
	1: "first", 2: "second", 3: "third", 4: "fourth", 5: "fifth", -1: "last",
}

func parseNum(tok string) (int, bool) {
	if digitsRe.MatchString(tok) {
		n, _ := strconv.Atoi(tok)
		return n, true
	}
	if n, ok := numberWords[tok]; ok {
		return n, true
	}
	return 0, false
}

func lookupWeekday(tok string) (int, bool) {
	if w, ok := weekdays[tok]; ok {
		return w, true
	}
	if strings.HasSuffix(tok, "s") {
		if w, ok := weekdays[strings.TrimSuffix(tok, "s")]; ok {
			return w, true
		}
	}
	return 0, false
}

func monthOf(tok string) (time.Month, bool) {
	if m, ok := months[tok]; ok {
		return m, true
	}
	return 0, false
}

// ----------------------------------------------------------------------------
// date helpers (date-only; tz-independent given a reference date)
// ----------------------------------------------------------------------------

func iso(t time.Time) string { return t.Format("2006-01-02") }

// dateOf builds a date, returning ok=false if the components do not round-trip
// (e.g. Feb 30). This mirrors JS Number.isNaN(new Date(...).getTime()) for the
// strict validity checks in the ISO and slash forms.
func dateOf(y int, m time.Month, d int) (time.Time, bool) {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || t.Month() != m || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

func nextWeekday(from time.Time, wd int) time.Time {
	delta := (wd - int(from.Weekday()) + 7) % 7
	if delta == 0 {
		delta = 7
	}
	return from.AddDate(0, 0, delta)
}

// nextNamedWeekday resolves "next <weekday>" with Monday-start-week semantics:
// the coming occurrence of wd, advanced a week when that occurrence still falls
// in the same Monday–Sunday week as `from`. So on Sun/Wed–Sat (this week's day
// has arrived or passed) it is the coming weekday, while on Mon/Tue (this
// week's day is still ahead) it is a week later — i.e. "next Wednesday" means
// next week's Wednesday.
func nextNamedWeekday(from time.Time, wd int) time.Time {
	coming := nextWeekday(from, wd)
	if sameMondayWeek(from, coming) {
		return coming.AddDate(0, 0, 7)
	}
	return coming
}

// mondayWeekStart returns the Monday beginning the Monday–Sunday week containing
// d (preserving d's time of day).
func mondayWeekStart(d time.Time) time.Time {
	off := (int(d.Weekday()) + 6) % 7 // Mon=0 .. Sun=6
	return d.AddDate(0, 0, -off)
}

func sameMondayWeek(a, b time.Time) bool {
	return mondayWeekStart(a).Equal(mondayWeekStart(b))
}

func addUnits(d time.Time, unit string, n int) time.Time {
	switch unit {
	case "day":
		return d.AddDate(0, 0, n)
	case "week":
		return d.AddDate(0, 0, 7*n)
	case "month":
		return d.AddDate(0, n, 0)
	case "year":
		return d.AddDate(n, 0, 0)
	}
	return d
}

// endOfWeek returns the upcoming Saturday (end of a Sun-Sat week), or today
// when today is already Saturday.
func endOfWeek(from time.Time) time.Time {
	delta := (6 - int(from.Weekday())) % 7
	return from.AddDate(0, 0, delta)
}

func lastDayOfMonth(y int, m time.Month) int {
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Day()
}

// lastDayOfMonthNamed returns the last day of the named month; if that day has
// already passed this year it rolls forward to next year.
func lastDayOfMonthNamed(ref time.Time, mon time.Month) time.Time {
	y := ref.Year()
	d := time.Date(y, mon, lastDayOfMonth(y, mon), 0, 0, 0, 0, time.UTC)
	if d.Before(ref) {
		d = time.Date(y+1, mon, lastDayOfMonth(y+1, mon), 0, 0, 0, 0, time.UTC)
	}
	return d
}

func singularUnit(tok string) string {
	if strings.HasSuffix(tok, "s") {
		return strings.TrimSuffix(tok, "s")
	}
	return tok
}

// ----------------------------------------------------------------------------
// date parsing
// ----------------------------------------------------------------------------

// ParseDate turns a natural-language date into YYYY-MM-DD. ok is false when the
// input is not a recognized date. Covers the common Todoist date forms.
func ParseDate(raw string, now time.Time) (string, bool) {
	s := normalize(raw)
	if s == "" {
		return "", false
	}

	if m := reISO.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		t, ok := dateOf(y, time.Month(mo), d)
		if !ok {
			return "", false
		}
		return iso(t), true
	}
	if m := reSlash.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		yr := now.Year()
		if m[3] != "" {
			yr, _ = strconv.Atoi(m[3])
			if len(m[3]) == 2 {
				yr += 2000
			}
		}
		t, ok := dateOf(yr, time.Month(mo), d)
		if !ok {
			return "", false
		}
		return iso(t), true
	}

	switch s {
	case "today", "tod":
		return iso(now), true
	case "tomorrow", "tom", "tmr":
		return iso(addUnits(now, "day", 1)), true
	case "yesterday":
		return iso(addUnits(now, "day", -1)), true
	case "end of month", "end of this month", "last day of month", "last day of this month":
		return iso(time.Date(now.Year(), now.Month(), lastDayOfMonth(now.Year(), now.Month()), 0, 0, 0, 0, time.UTC)), true
	case "next week":
		return iso(addUnits(now, "week", 1)), true
	case "next month":
		return iso(time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)), true
	case "next year":
		return iso(time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)), true
	case "this weekend":
		return iso(nextWeekday(now, 6)), true
	}

	if m := reEndOf.FindStringSubmatch(s); m != nil {
		switch m[1] {
		case "week":
			return iso(endOfWeek(now)), true
		case "month":
			return iso(time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)), true
		}
		if mon, ok := monthOf(m[1]); ok {
			return iso(lastDayOfMonthNamed(now, mon)), true
		}
	}

	if m := reInN.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return iso(addUnits(now, singularUnit(m[2]), n)), true
	}
	if m := rePlusDays.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return iso(addUnits(now, "day", n)), true
	}

	if m := reNextWord.FindStringSubmatch(s); m != nil {
		if wd, ok := lookupWeekday(m[1]); ok {
			return iso(nextNamedWeekday(now, wd)), true
		}
		if mon, ok := monthOf(m[1]); ok {
			return iso(time.Date(now.Year()+1, mon, 1, 0, 0, 0, 0, time.UTC)), true
		}
	}
	if m := reThisWord.FindStringSubmatch(s); m != nil {
		// "this <weekday>" = the coming occurrence, like the bare weekday.
		if wd, ok := lookupWeekday(m[1]); ok {
			return iso(nextWeekday(now, wd)), true
		}
	}
	if m := reSingleWord.FindStringSubmatch(s); m != nil {
		if wd, ok := lookupWeekday(m[1]); ok {
			return iso(nextWeekday(now, wd)), true
		}
	}

	if m := reMid.FindStringSubmatch(s); m != nil {
		if mon, ok := monthOf(m[1]); ok {
			return iso(resolveMonthDay(now, mon, 15, 0, false)), true
		}
	}

	if m := reOrdinalDay.FindStringSubmatch(s); m != nil {
		day, _ := strconv.Atoi(m[1])
		d := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.UTC)
		if d.Before(now) {
			d = time.Date(now.Year(), now.Month()+1, day, 0, 0, 0, 0, time.UTC)
		}
		return iso(d), true
	}

	return parseMonthDayPhrase(s, now)
}

func parseMonthDayPhrase(s string, now time.Time) (string, bool) {
	parts := splitTokens(s)
	if len(parts) < 2 {
		return "", false
	}
	mon, monOK := monthOf(parts[0])
	var dayTok string
	const yearIdx = 2
	if !monOK {
		mon, monOK = monthOf(parts[1])
		dayTok = parts[0]
	} else {
		dayTok = parts[1]
	}
	if !monOK {
		return "", false
	}
	dm := reDayNum.FindStringSubmatch(dayTok)
	if dm == nil {
		return "", false
	}
	day, _ := strconv.Atoi(dm[1])
	explicitYear, hasYear := 0, false
	if yearIdx < len(parts) && reYear.MatchString(parts[yearIdx]) {
		explicitYear, _ = strconv.Atoi(parts[yearIdx])
		if len(parts[yearIdx]) == 2 {
			explicitYear += 2000
		}
		hasYear = true
	}
	return iso(resolveMonthDay(now, mon, day, explicitYear, hasYear)), true
}

// resolveMonthDay builds a date for (month, day); when no explicit year is
// given and the date already passed this year it rolls forward a year.
func resolveMonthDay(ref time.Time, mon time.Month, day, explicitYear int, hasYear bool) time.Time {
	y := explicitYear
	if !hasYear {
		y = ref.Year()
	}
	d := time.Date(y, mon, day, 0, 0, 0, 0, time.UTC)
	if !hasYear && d.Before(ref) {
		d = time.Date(y+1, mon, day, 0, 0, 0, 0, time.UTC)
	}
	return d
}

// ----------------------------------------------------------------------------
// recurrence core parsing
// ----------------------------------------------------------------------------

// unitFrequency maps a leading unit phrase to a frequency. "" means not a unit;
// "hour" is returned so the caller can reject times.
func unitFrequency(u string) string {
	switch {
	case reDayUnit.MatchString(u):
		return "daily"
	case reWeekUnit.MatchString(u):
		return "weekly"
	case reMonthUnit.MatchString(u):
		return "monthly"
	case reYearUnit.MatchString(u):
		return "yearly"
	case reHourUnit.MatchString(u):
		return "hour"
	}
	return ""
}

// parseCore turns the recurrence pattern after "every[/!]" (clauses stripped)
// into the frequency/interval/targets of a Recurrence.
func parseCore(core string) (*models.Recurrence, error) {
	core = normalize(core)
	if core == "" {
		return nil, errors.New(`missing repeat pattern after "every"`)
	}

	interval := 1
	rest := core
	if m := reOther.FindStringSubmatch(core); m != nil {
		interval = 2
		rest = m[1]
	} else if m := reLeadNum.FindStringSubmatch(core); m != nil {
		if n, ok := parseNum(m[1]); ok && n >= 1 {
			interval = n
			rest = m[2]
		}
	}
	rest = strings.TrimSpace(rest)

	// Whole-word shorthands that never take a qualifier.
	switch {
	case reWeekdaysOnly.MatchString(rest):
		return &models.Recurrence{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 2, 3, 4, 5}}, nil
	case reWeekendsOnly.MatchString(rest):
		return &models.Recurrence{Frequency: "weekly", Interval: 1, Weekdays: []int{0, 6}}, nil
	case reQuarter.MatchString(rest):
		return &models.Recurrence{Frequency: "monthly", Interval: interval * 3}, nil
	case reFortnight.MatchString(rest):
		return &models.Recurrence{Frequency: "weekly", Interval: interval * 2}, nil
	case reHours.MatchString(rest):
		return nil, errors.New("times are not supported (this app is date-only)")
	}

	// Peel an optional "on <qualifier>" off a leading unit (week on mon, wed /
	// month on the 15th).
	unitPart, qual := rest, ""
	if loc := reOn.FindStringSubmatchIndex(rest); loc != nil {
		unitPart = strings.TrimSpace(rest[:loc[0]])
		qual = strings.TrimSpace(rest[loc[2]:loc[3]])
	}

	freq := unitFrequency(unitPart)
	if freq == "hour" {
		return nil, errors.New("times are not supported (this app is date-only)")
	}

	if freq != "" {
		if qual == "" {
			return &models.Recurrence{Frequency: freq, Interval: interval}, nil
		}
		switch freq {
		case "weekly":
			wds, ok := extractWeekdays(qual)
			if !ok {
				return nil, errors.New(`expected weekdays after "on" (e.g. on mon, wed)`)
			}
			return &models.Recurrence{Frequency: "weekly", Interval: interval, Weekdays: wds}, nil
		case "monthly":
			q := strings.TrimSpace(reThePrefix.ReplaceAllString(qual, ""))
			if reLastDayQual.MatchString(q) {
				return &models.Recurrence{Frequency: "monthly", Interval: interval, LastDay: true}, nil
			}
			if nw, ok := extractNthWeekday(q); ok {
				return &models.Recurrence{Frequency: "monthly", Interval: interval, NthWeekday: nw}, nil
			}
			if md, errMsg, ok := extractMonthDays(q); ok {
				if errMsg != "" {
					return nil, errors.New(errMsg)
				}
				return &models.Recurrence{Frequency: "monthly", Interval: interval, MonthDays: md}, nil
			}
			return nil, errors.New(`expected a day (e.g. on the 15th) or weekday after "on"`)
		default:
			return nil, fmt.Errorf("a %q qualifier does not apply to %s", qual, freq)
		}
	}

	// No leading unit: classify the bare pattern.
	if nw, ok := extractNthWeekday(rest); ok {
		return &models.Recurrence{Frequency: "monthly", Interval: interval, NthWeekday: nw}, nil
	}
	if wds, ok := extractWeekdays(rest); ok {
		return &models.Recurrence{Frequency: "weekly", Interval: interval, Weekdays: wds}, nil
	}
	if reLastDayBare.MatchString(rest) {
		return &models.Recurrence{Frequency: "monthly", Interval: interval, LastDay: true}, nil
	}
	if md, errMsg, ok := extractMonthDays(rest); ok {
		if errMsg != "" {
			return nil, errors.New(errMsg)
		}
		return &models.Recurrence{Frequency: "monthly", Interval: interval, MonthDays: md}, nil
	}
	return nil, errors.New(`unrecognized repeat; try "every 2 weeks on mon, wed" or "every month on the 15th"`)
}

func extractNthWeekday(rest string) (*models.NthWeekday, bool) {
	m := reNthWeekday.FindStringSubmatch(rest)
	if m == nil {
		return nil, false
	}
	n, ok := ordinalWords[m[1]]
	if !ok {
		return nil, false
	}
	wd, ok := lookupWeekday(m[2])
	if !ok {
		return nil, false
	}
	return &models.NthWeekday{N: n, Weekday: wd}, true
}

func extractWeekdays(rest string) ([]int, bool) {
	s := strings.TrimSpace(reThePrefix.ReplaceAllString(rest, ""))
	toks := splitTokens(s)
	if len(toks) == 0 {
		return nil, false
	}
	var days []int
	for _, t := range toks {
		d, ok := lookupWeekday(t)
		if !ok {
			return nil, false
		}
		days = append(days, d)
	}
	return uniqueSorted(days), true
}

// extractMonthDays returns the parsed days. ok is true when the input looked
// like a day list (every token was a day number); errMsg is set when a token
// was a day number but out of range.
func extractMonthDays(rest string) (days []int, errMsg string, ok bool) {
	s := reThePrefix.ReplaceAllString(rest, "")
	s = reOfMonth.ReplaceAllString(s, "")
	s = reDaysPrefix.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	toks := splitTokens(s)
	if len(toks) == 0 {
		return nil, "", false
	}
	var ds []int
	for _, t := range toks {
		m := reDayNum.FindStringSubmatch(t)
		if m == nil {
			return nil, "", false
		}
		d, _ := strconv.Atoi(m[1])
		if d < 1 || d > 31 {
			return nil, "day of month must be 1-31", true
		}
		ds = append(ds, d)
	}
	return uniqueSorted(ds), "", true
}

// ----------------------------------------------------------------------------
// clause parsing (start / end / for)
// ----------------------------------------------------------------------------

func splitClauses(body string) (core, clauses string) {
	loc := reClauseKW.FindStringIndex(body)
	if loc == nil {
		return strings.TrimSpace(body), ""
	}
	return strings.TrimSpace(body[:loc[0]]), strings.TrimSpace(body[loc[0]:])
}

type parsedClauses struct {
	start, end, duration string
}

func parseClauses(s string) parsedClauses {
	type pos struct {
		kw  string
		idx int
	}
	matches := reClauseKW.FindAllStringIndex(s, -1)
	positions := make([]pos, 0, len(matches))
	for _, loc := range matches {
		positions = append(positions, pos{kw: s[loc[0]:loc[1]], idx: loc[0]})
	}
	var res parsedClauses
	for i, p := range positions {
		var endIdx int
		if i+1 < len(positions) {
			endIdx = positions[i+1].idx
		} else {
			endIdx = len(s)
		}
		val := strings.TrimSpace(s[p.idx+len(p.kw) : endIdx])
		switch p.kw {
		case "starting", "from":
			res.start = val
		case "ending", "until":
			res.end = val
		case "for":
			res.duration = val
		}
	}
	return res
}

func computeEnd(startISO, duration string) (string, bool) {
	m := reDuration.FindStringSubmatch(duration)
	if m == nil {
		return "", false
	}
	d, err := time.Parse("2006-01-02", startISO)
	if err != nil {
		return "", false
	}
	n, _ := strconv.Atoi(m[1])
	return iso(addUnits(d, singularUnit(m[2]), n)), true
}

// ----------------------------------------------------------------------------
// top-level schedule parser
// ----------------------------------------------------------------------------

// Parse turns the combined due/recurrence field into a structured Schedule.
// Empty input clears both (zero DueDate, nil Recurrence, nil error).
func Parse(raw string, now time.Time) (Schedule, error) {
	text := normalize(raw)
	if text == "" {
		return Schedule{}, nil
	}
	// Accept bare recurrence keywords ("daily", "weekly", ...) as shorthand
	// for "every <keyword>", matching Todoist.
	if reBareKeywords.MatchString(text) {
		text = "every " + text
	}

	loc := reEvery.FindStringSubmatchIndex(text)
	if loc != nil {
		fromCompletion := loc[4] != -1 // group 2 ("!") matched
		body := text[loc[1]:]
		core, clausesStr := splitClauses(body)
		recurrence, err := parseCore(core)
		if err != nil {
			return Schedule{}, err
		}
		recurrence.FromCompletion = fromCompletion

		cl := parseClauses(clausesStr)
		var dueDate string
		if cl.start != "" {
			dd, ok := ParseDate(cl.start, now)
			if !ok {
				return Schedule{}, fmt.Errorf("couldn't read start date %q", cl.start)
			}
			dueDate = dd
		} else {
			// No explicit start: align the first due date to the recurrence's
			// first occurrence on or after today, so a rule like "every last
			// friday" lands on the next last Friday instead of today. Plain
			// intervals (every day/week/month/year) keep today as the seed.
			if dd, ok := FirstDue(*recurrence, now); ok {
				dueDate = dd
			} else {
				dueDate = iso(now)
			}
		}
		switch {
		case cl.end != "":
			e, ok := ParseDate(cl.end, now)
			if !ok {
				return Schedule{}, fmt.Errorf("couldn't read end date %q", cl.end)
			}
			recurrence.EndDate = e
		case cl.duration != "":
			e, ok := computeEnd(dueDate, cl.duration)
			if !ok {
				return Schedule{}, fmt.Errorf("couldn't read duration %q", cl.duration)
			}
			recurrence.EndDate = e
		}
		return Schedule{DueDate: dueDate, Recurrence: recurrence}, nil
	}

	dueDate, ok := ParseDate(text, now)
	if !ok {
		return Schedule{}, fmt.Errorf("couldn't read a date from %q", strings.TrimSpace(raw))
	}
	return Schedule{DueDate: dueDate}, nil
}

// QuickAdd is the parsed result of a quick-add line: a title, any #label tags,
// a single !priority, and an optional trailing schedule.
type QuickAdd struct {
	Title     string
	Labels    []string
	Priority  string
	Schedule  Schedule
}

// Extract parses a quick-add line into a title, optional #label tags, and an
// optional trailing schedule. #label tokens (single words, no spaces/commas)
// are stripped first; the bare name without "#" is kept, de-duplicated
// case-insensitively in first-seen order. The remaining text is then split into
// title and schedule by scanning whitespace tokens left to right and taking the
// first split point at which the trailing suffix parses as a due date /
// recurrence, so the schedule is the longest trailing run and the title is
// everything before it.
//
// Because the schedule grammar is anchored at the start (see Parse), a
// candidate suffix parses only when the schedule phrase begins on its first
// token — leading title words never produce a false match. parseMonthDayPhrase
// ignores tokens after a valid month/day, so a candidate is accepted only when
// removing its last token changes or invalidates the parse; that rejects
// lenient matches such as "aug 15 milk" where "milk" would otherwise be
// silently dropped from the title.
//
// ok is false when the title would be empty (blank input, or only labels).
func Extract(raw string, now time.Time) (QuickAdd, bool) {
	labels, body := stripLabels(raw)
	priority, body := stripPriority(body)
	if body == "" {
		return QuickAdd{Labels: labels, Priority: priority}, false
	}
	toks := strings.Fields(body)
	for k := 1; k < len(toks); k++ {
		s, err := Parse(strings.Join(toks[k:], " "), now)
		if err != nil || (s.DueDate == "" && s.Recurrence == nil) {
			continue
		}
		if len(toks)-k >= 2 {
			s2, err2 := Parse(strings.Join(toks[k:len(toks)-1], " "), now)
			if err2 == nil && sameSchedule(s, s2) {
				continue // last token was ignored by the parser: trailing junk
			}
		}
		return QuickAdd{Title: strings.Join(toks[:k], " "), Labels: labels, Priority: priority, Schedule: s}, true
	}
	return QuickAdd{Title: body, Labels: labels, Priority: priority}, true
}

// stripPriority removes a leading or trailing !priority token from s, returning
// the priority name (without "!") and the cleaned text. Only the first
// !priority token is extracted; if there are more they are left as-is in the
// text (the caller will see them as title words).
func stripPriority(s string) (priority string, cleaned string) {
	loc := rePriority.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", s
	}
	// loc[2]:loc[3] is the capture group (the name after "!").
	priority = s[loc[2]:loc[3]]
	// Remove the matched token (loc[0]:loc[1]) and trim/collapse whitespace.
	cleaned = s[:loc[0]] + s[loc[1]:]
	cleaned = strings.TrimSpace(reSpaces.ReplaceAllString(cleaned, " "))
	return priority, cleaned
}

// stripLabels removes #label tags from s, returning the de-duplicated label
// names (without "#", first-seen order, case-insensitive) and the text with the
// tags removed and spacing collapsed to single spaces.
func stripLabels(s string) (labels []string, cleaned string) {
	cleaned = reLabel.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1:] // drop the leading "#"
		low := strings.ToLower(name)
		for _, e := range labels {
			if strings.ToLower(e) == low {
				return "" // duplicate: drop, keep the first casing
			}
		}
		labels = append(labels, name)
		return ""
	})
	return labels, strings.TrimSpace(reSpaces.ReplaceAllString(cleaned, " "))
}

// sameSchedule reports whether two Schedules carry the same due date and
// recurrence rule.
func sameSchedule(a, b Schedule) bool {
	if a.DueDate != b.DueDate {
		return false
	}
	ar, br := a.Recurrence, b.Recurrence
	if ar == nil || br == nil {
		return ar == br
	}
	return reflect.DeepEqual(*ar, *br)
}

// ----------------------------------------------------------------------------
// recurrence advance (date engine)
// ----------------------------------------------------------------------------
//
// The date engine turns a Recurrence into concrete dates. NextDue advances a
// stored due date to its next occurrence (used by the data layer when a
// recurring todo is completed); FirstDue resolves the initial due date when a
// recurrence is created without an explicit starting date. Both share one
// advance core so the parse path and the completion path agree.

// ErrAdvance indicates a recurrence rule could not be turned into a date
// (unknown frequency or no reachable target). It is unreachable for rules that
// passed parseCore/Valid; the data layer wraps it so it still maps to HTTP 400.
var ErrAdvance = errors.New("recurrence rule cannot be advanced")

// NextDue returns the next due date strictly after current per rc. With
// FromCompletion set (Todoist's "every!") it advances from now (the completion
// date) instead of current. recurring is false when the next occurrence would
// fall past rc.EndDate, signalling the recurrence window has closed.
func NextDue(current string, rc models.Recurrence, now time.Time) (next string, recurring bool, err error) {
	const layout = "2006-01-02"
	base, perr := time.Parse(layout, current)
	if perr != nil || rc.FromCompletion {
		base = now
	}
	t, err := advance(base, rc, false)
	if err != nil {
		return "", false, err
	}
	if rc.EndDate != "" && iso(t) > rc.EndDate {
		return "", false, nil
	}
	return iso(t), true, nil
}

// FirstDue returns the first due date for rc on or after now. A recurrence
// created without an explicit starting date uses this so a targeted rule such
// as "every last friday" or "every month on the 15th" lands on its first real
// occurrence rather than today. Plain intervals (every day/week/month/year)
// resolve to now itself. EndDate is not consulted: the first occurrence is the
// due date even when it already lies past the recurrence window. ok is false
// only for a malformed rule, which cannot reach here from Parse.
func FirstDue(rc models.Recurrence, now time.Time) (due string, ok bool) {
	t, err := advance(now, rc, true)
	if err != nil {
		return "", false
	}
	return iso(t), true
}

// advance dispatches a single recurrence step from base. When inclusive the
// base date itself qualifies (FirstDue); otherwise the result is strictly
// after base (NextDue).
func advance(base time.Time, rc models.Recurrence, inclusive bool) (time.Time, error) {
	switch rc.Frequency {
	case "daily":
		if inclusive {
			return base, nil
		}
		return base.AddDate(0, 0, rc.Interval), nil
	case "weekly":
		return nextWeekly(base, rc, inclusive)
	case "monthly":
		return nextMonthly(base, rc, inclusive)
	case "yearly":
		if inclusive {
			return base, nil
		}
		return addYears(base, rc.Interval), nil
	default:
		return time.Time{}, fmt.Errorf("%w: unknown frequency %q", ErrAdvance, rc.Frequency)
	}
}

// nextWeekly advances to the next target weekday. With no weekdays set it is a
// plain every-N-weeks step. With weekdays set, active weeks are every
// rc.Interval weeks anchored on base's week: a date is due iff its weekday is a
// target and its week index (whole weeks since base's Sunday) is a multiple of
// rc.Interval, which handles "every 2 weeks on mon, wed". When inclusive and
// base itself is a target weekday, base is returned (its week is always active).
func nextWeekly(base time.Time, rc models.Recurrence, inclusive bool) (time.Time, error) {
	if len(rc.Weekdays) == 0 {
		if inclusive {
			return base, nil
		}
		return base.AddDate(0, 0, 7*rc.Interval), nil
	}
	targets := make(map[int]bool, len(rc.Weekdays))
	for _, w := range rc.Weekdays {
		targets[w] = true
	}
	baseWD := int(base.Weekday())
	// base's own week is week 0, always a multiple of rc.Interval, so a target
	// weekday landing on base is the first occurrence.
	if inclusive && targets[baseWD] {
		return base, nil
	}
	for d := 1; d <= 7*rc.Interval; d++ {
		c := base.AddDate(0, 0, d)
		if !targets[int(c.Weekday())] {
			continue
		}
		weeks := (d + baseWD) / 7 // whole weeks since base's Sunday
		if weeks%rc.Interval == 0 {
			return c, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: no weekly target after base", ErrAdvance)
}

// nextMonthly advances to the next month-level target. Active months are every
// rc.Interval months from base's month; within each, candidate days come from
// MonthDays, LastDay, NthWeekday, or (if none set) the base day-of-month. The
// smallest candidate on or after base (inclusive) or strictly after base wins.
func nextMonthly(base time.Time, rc models.Recurrence, inclusive bool) (time.Time, error) {
	for offset := 0; offset <= 1200; offset++ {
		if offset%rc.Interval != 0 {
			continue
		}
		year := base.Year() + (int(base.Month())-1+offset)/12
		month := time.Month((int(base.Month())-1+offset)%12 + 1)
		for _, c := range monthCandidates(year, month, base, rc) {
			if c.After(base) || (inclusive && c.Equal(base)) {
				return c, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("%w: no monthly target after base", ErrAdvance)
}

// monthCandidates builds the set of due days in (year, month) for a monthly
// rule, sorted ascending. Targets out of range for the month are dropped.
func monthCandidates(year int, month time.Month, base time.Time, rc models.Recurrence) []time.Time {
	loc := base.Location()
	dim := lastDayOfMonth(year, month)
	var cands []time.Time
	addDay := func(d int) {
		if d >= 1 && d <= dim {
			cands = append(cands, time.Date(year, month, d, 0, 0, 0, 0, loc))
		}
	}
	for _, d := range rc.MonthDays {
		addDay(d)
	}
	if rc.LastDay {
		addDay(dim)
	}
	if rc.NthWeekday != nil {
		addDay(nthWeekdayDay(year, month, rc.NthWeekday))
	}
	// No explicit targets: keep the base day-of-month (plain "every month"),
	// clamping to the last day when the month is shorter (e.g. Jan 31 -> Feb 28).
	if len(rc.MonthDays) == 0 && !rc.LastDay && rc.NthWeekday == nil {
		d := base.Day()
		if d > dim {
			d = dim
		}
		cands = append(cands, time.Date(year, month, d, 0, 0, 0, 0, loc))
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
	return cands
}

// nthWeekdayDay returns the day-of-month of the Nth occurrence of a weekday in
// (year, month), or the last occurrence when n == -1. Returns 0 if that
// ordinal does not exist in the month (e.g. a 5th Monday).
func nthWeekdayDay(year int, month time.Month, nw *models.NthWeekday) int {
	dim := lastDayOfMonth(year, month)
	if nw.N == -1 {
		for d := dim; d >= 1; d-- {
			if int(time.Date(year, month, d, 0, 0, 0, 0, time.UTC).Weekday()) == nw.Weekday {
				return d
			}
		}
		return 0
	}
	count := 0
	for d := 1; d <= dim; d++ {
		if int(time.Date(year, month, d, 0, 0, 0, 0, time.UTC).Weekday()) == nw.Weekday {
			count++
			if count == nw.N {
				return d
			}
		}
	}
	return 0
}

// addYears shifts base by years, clamping Feb 29 overflow (Go's AddDate alone
// would land on Mar 1 in non-leap years).
func addYears(base time.Time, years int) time.Time {
	t := base.AddDate(years, 0, 0)
	if t.Day() != base.Day() {
		t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 0, -1)
	}
	return t
}

// ----------------------------------------------------------------------------
// formatting (round-trips back through Parse)
// ----------------------------------------------------------------------------

// Format renders the repeat pattern (without the leading "every" or start/end
// clauses) for badges and the input seed.
func Format(rc models.Recurrence) string {
	if rc.Frequency == "" {
		return ""
	}
	multi := func(n int, single, plural string) string {
		if n > 1 {
			return fmt.Sprintf("%d %s", n, plural)
		}
		return single
	}
	switch rc.Frequency {
	case "daily":
		return multi(rc.Interval, "day", "days")
	case "weekly":
		base := multi(rc.Interval, "week", "weeks")
		if len(rc.Weekdays) > 0 {
			wds := append([]int(nil), rc.Weekdays...)
			sort.Ints(wds)
			names := make([]string, len(wds))
			for i, d := range wds {
				names[i] = weekdayAbbr[d]
			}
			return base + " on " + strings.Join(names, ", ")
		}
		return base
	case "monthly":
		base := multi(rc.Interval, "month", "months")
		if rc.NthWeekday != nil {
			o, ok := ordinalWord[rc.NthWeekday.N]
			if !ok {
				o = strconv.Itoa(rc.NthWeekday.N)
			}
			wd := weekdayFull[rc.NthWeekday.Weekday]
			if rc.Interval > 1 {
				return fmt.Sprintf("%s on the %s %s", base, o, wd)
			}
			return fmt.Sprintf("%s %s", o, wd)
		}
		if rc.LastDay {
			return base + " on the last day"
		}
		if len(rc.MonthDays) == 1 {
			d := rc.MonthDays[0]
			return fmt.Sprintf("%s on the %d%s", base, d, ordinalSuffix(d))
		}
		if len(rc.MonthDays) > 1 {
			parts := make([]string, len(rc.MonthDays))
			for i, d := range rc.MonthDays {
				parts[i] = strconv.Itoa(d) + ordinalSuffix(d)
			}
			return base + " on the " + strings.Join(parts, ", ")
		}
		return base
	case "yearly":
		return multi(rc.Interval, "year", "years")
	}
	return ""
}

func ordinalSuffix(n int) string {
	switch {
	case n%10 == 1 && n != 11:
		return "st"
	case n%10 == 2 && n != 12:
		return "nd"
	case n%10 == 3 && n != 13:
		return "rd"
	default:
		return "th"
	}
}

// FormatSchedule renders a Schedule back to free text. The output round-trips
// through Parse, so editing shows what the user would retype. now is the
// reference "today": a due date equal to today omits the "starting" clause.
func FormatSchedule(dueDate string, rc *models.Recurrence, now time.Time) string {
	if rc == nil && dueDate == "" {
		return ""
	}
	if rc == nil {
		return dueDate
	}
	s := "every "
	if rc.FromCompletion {
		s = "every! "
	}
	s += Format(*rc)
	if dueDate != "" && dueDate != iso(now) {
		s += " starting " + dueDate
	}
	if rc.EndDate != "" {
		s += " ending " + rc.EndDate
	}
	return s
}

// ----------------------------------------------------------------------------
// shared helpers / regexes
// ----------------------------------------------------------------------------

func normalize(s string) string {
	s = strings.TrimSpace(s)
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.ToLower(s)
}

// splitTokens splits on commas, whitespace, and the word "and" (case folded
// already by normalize), dropping empties.
func splitTokens(s string) []string {
	s = reAndWord.ReplaceAllString(s, " ")
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
}

func uniqueSorted(ds []int) []int {
	sort.Ints(ds)
	out := make([]int, 0, len(ds))
	prev := -1
	for _, d := range ds {
		if d != prev {
			out = append(out, d)
			prev = d
		}
	}
	return out
}

var (
	digitsRe = regexp.MustCompile(`^\d+$`)

	reISO        = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	reSlash      = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})(?:/(\d{2,4}))?$`)
	reEndOf      = regexp.MustCompile(`^(?:end|last day) of (?:the\s+|this\s+)?(\w+)$`)
	reInN        = regexp.MustCompile(`^in\s+(\d+)\s+(day|days|week|weeks|month|months|year|years)$`)
	rePlusDays   = regexp.MustCompile(`^\+?(\d+)\s*(?:days?)?$`)
	reNextWord   = regexp.MustCompile(`^next\s+(\w+)$`)
	reThisWord   = regexp.MustCompile(`^this\s+(\w+)$`)
	reSingleWord = regexp.MustCompile(`^(\w+)$`)
	reMid        = regexp.MustCompile(`^mid\s+(\w+)$`)
	reOrdinalDay = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)$`)
	reDayNum     = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?$`)
	reYear       = regexp.MustCompile(`^\d{2,4}$`)

	reOther        = regexp.MustCompile(`^other\s+(.+)`)
	reLeadNum      = regexp.MustCompile(`^(\d+|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)\s+(.+)`)
	reWeekdaysOnly = regexp.MustCompile(`^(weekday|workday|weekdays|workdays)$`)
	reWeekendsOnly = regexp.MustCompile(`^(weekend|weekends)$`)
	reQuarter      = regexp.MustCompile(`^(quarter|quarters|quarterly)$`)
	reFortnight    = regexp.MustCompile(`^(fortnight|fortnights|fortnightly)$`)
	reHours        = regexp.MustCompile(`^(hour|hours)$`)
	reOn           = regexp.MustCompile(`\bon\s+(.+)$`)
	reLastDayQual  = regexp.MustCompile(`^last\s+day(?:\s+of\s+(?:the\s+)?month)?$`)
	reLastDayBare  = regexp.MustCompile(`^(?:the\s+)?last\s+day(?:\s+of\s+(?:the\s+)?month)?$`)
	reThePrefix    = regexp.MustCompile(`^the\s+`)
	reOfMonth      = regexp.MustCompile(`\s+of\s+(?:the\s+)?month$`)
	reDaysPrefix   = regexp.MustCompile(`^days?\s+`)
	reNthWeekday   = regexp.MustCompile(`^(first|second|third|fourth|fifth|last|1st|2nd|3rd|4th|5th)\s+(\w+)`)
	reAndWord      = regexp.MustCompile(`\band\b`)

	reDayUnit   = regexp.MustCompile(`^(day|days|daily)$`)
	reWeekUnit  = regexp.MustCompile(`^(week|weeks|weekly)$`)
	reMonthUnit = regexp.MustCompile(`^(month|months|monthly)$`)
	reYearUnit  = regexp.MustCompile(`^(year|years|yearly|annually)$`)
	reHourUnit  = regexp.MustCompile(`^(hour|hours)$`)

	reDuration     = regexp.MustCompile(`(\d+)\s*(day|days|week|weeks|month|months|year|years)`)
	reClauseKW     = regexp.MustCompile(`\b(starting|from|ending|until|for)\b`)
	reEvery        = regexp.MustCompile(`^(ev|every)(!)?(?:\s+|$)`)
	reBareKeywords = regexp.MustCompile(`^(daily|weekly|monthly|yearly|quarterly|fortnight|fortnightly|weekdays?|weekends?|workdays?)\b`)

	reLabel    = regexp.MustCompile(`#([^\s#,]+)`)
	rePriority = regexp.MustCompile(`(?:^|\s)!([^\s!,]+)`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

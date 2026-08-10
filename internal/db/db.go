// Package db provides a sqlite-backed persistence layer for todos.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/greboid/todo/internal/models"

	// modernc.org/sqlite is a pure-Go (no CGO) sqlite driver registered
	// under the driver name "sqlite".
	_ "modernc.org/sqlite"
)

// DB is the todo persistence layer.
type DB struct {
	conn *sql.DB
}

// New opens (or creates) the sqlite database at path and applies the schema.
// busy_timeout makes writes block briefly instead of erroring under concurrency.
func New(ctx context.Context, path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	conn.SetMaxOpenConns(1) // sqlite serializes writes; keep it simple and correct
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	d := &DB{conn: conn}
	if err := d.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the database handle.
func (d *DB) Close() error { return d.conn.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS boards (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT    NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_boards_position ON boards(position, id);

CREATE TABLE IF NOT EXISTS todos (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    board_id    INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    completed   INTEGER NOT NULL DEFAULT 0,
    parent_id   INTEGER NULL REFERENCES todos(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL DEFAULT 0,
    due_date    TEXT    NULL,
    recurrence  TEXT    NULL
);
CREATE INDEX IF NOT EXISTS idx_todos_parent ON todos(board_id, parent_id, position);

CREATE TABLE IF NOT EXISTS labels (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS todo_labels (
    todo_id  INTEGER NOT NULL REFERENCES todos(id)    ON DELETE CASCADE,
    label_id INTEGER NOT NULL REFERENCES labels(id)   ON DELETE CASCADE,
    PRIMARY KEY (todo_id, label_id)
);

CREATE TABLE IF NOT EXISTS predefined_labels (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.conn.ExecContext(ctx, schema); err != nil {
		return err
	}
	// schema is CREATE TABLE IF NOT EXISTS, which cannot add columns to an
	// already-existing table. Upgrade older DBs in place with idempotent
	// ALTER TABLE statements guarded by PRAGMA table_info.
	return d.addColumnsIfMissing(ctx, "todos", []columnSpec{
		{"due_date", "TEXT NULL"},
		{"recurrence", "TEXT NULL"},
	})
}

// columnSpec names a column and the tail of its ALTER TABLE ADD COLUMN DDL.
type columnSpec struct{ name, ddl string }

// addColumnsIfMissing adds each missing column to table via ALTER TABLE.
// Columns already present (per PRAGMA table_info) are skipped, making it a
// no-op on fresh and already-migrated databases.
func (d *DB) addColumnsIfMissing(ctx context.Context, table string, cols []columnSpec) error {
	rows, err := d.conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("table_info %s: %w", table, err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, c := range cols {
		if existing[c.name] {
			continue
		}
		if _, err := d.conn.ExecContext(ctx,
			`ALTER TABLE `+table+` ADD COLUMN `+c.name+` `+c.ddl); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, c.name, err)
		}
	}
	return nil
}

// ListAll returns every todo on the given board with its labels attached.
// boardID <= 0 returns todos across all boards (used by the legacy complete-cascade
// sync path which already re-scopes on the client).
func (d *DB) ListAll(ctx context.Context, boardID int64) ([]models.Todo, error) {
	const query = `SELECT id, board_id, title, description, completed, parent_id, position, due_date, recurrence
        FROM todos
        WHERE (? = 0 OR board_id = ?)
        ORDER BY COALESCE(parent_id, 0), position, id`
	rows, err := d.conn.QueryContext(ctx, query, boardID, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Todo
	for rows.Next() {
		var t models.Todo
		var parentID sql.NullInt64
		var completed int
		var dueNS, recurNS sql.NullString
		if err := rows.Scan(&t.ID, &t.BoardID, &t.Title, &t.Description, &completed, &parentID, &t.Position, &dueNS, &recurNS); err != nil {
			return nil, err
		}
		t.Completed = completed != 0
		if parentID.Valid {
			pid := parentID.Int64
			t.ParentID = &pid
		}
		if dueNS.Valid {
			t.DueDate = dueNS.String
		}
		if recurNS.Valid && recurNS.String != "" {
			var rc models.Recurrence
			if err := json.Unmarshal([]byte(recurNS.String), &rc); err != nil {
				return nil, err
			}
			t.Recurrence = &rc
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.attachLabels(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) attachLabels(ctx context.Context, ts []models.Todo) error {
	if len(ts) == 0 {
		return nil
	}
	ids := make([]any, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	q := `SELECT tl.todo_id, l.name FROM todo_labels tl
          JOIN labels l ON l.id = tl.label_id
          WHERE tl.todo_id IN (` + placeholders(len(ids)) + `)
          ORDER BY l.name`
	rows, err := d.conn.QueryContext(ctx, q, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[int64][]string, len(ts))
	for rows.Next() {
		var todoID int64
		var name string
		if err := rows.Scan(&todoID, &name); err != nil {
			return err
		}
		byID[todoID] = append(byID[todoID], name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range ts {
		ts[i].Labels = byID[ts[i].ID]
	}
	return nil
}

// placeholders returns a comma-separated list of n SQL parameter markers.
func placeholders(n int) string {
	return strings.Join(slices.Repeat([]string{"?"}, n), ",")
}

// Create inserts a todo. If Position is nil the item is appended after the
// current max position within its sibling group. Siblings after the inserted
// position are shifted down to keep ordering gapless.
//
// BoardID resolution: if ParentID is set the new todo inherits the parent's
// board (subtasks always live on their parent's board). Otherwise BoardID must
// be supplied; if neither is present the request is rejected.
func (d *DB) Create(ctx context.Context, in models.CreateTodo) (models.Todo, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()

	boardID, err := resolveBoardID(ctx, tx, in.ParentID, in.BoardID)
	if err != nil {
		return models.Todo{}, err
	}

	pos, err := resolvePosition(ctx, tx, boardID, in.ParentID, in.Position)
	if err != nil {
		return models.Todo{}, err
	}
	parent := toNullInt64(in.ParentID)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO todos (board_id, title, description, completed, parent_id, position, due_date, recurrence)
         VALUES (?, ?, ?, 0, ?, ?, ?, ?)`,
		boardID, in.Title, in.Description, parent, pos, toNullString(in.DueDate), recurrenceJSON(in.Recurrence))
	if err != nil {
		return models.Todo{}, fmt.Errorf("insert todo: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return models.Todo{}, err
	}
	if err := shiftSiblingsAfter(ctx, tx, boardID, in.ParentID, pos, 1, id); err != nil {
		return models.Todo{}, err
	}
	if err := setLabelsTx(ctx, tx, id, in.Labels); err != nil {
		return models.Todo{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Todo{}, err
	}
	return d.Get(ctx, id)
}

// resolveBoardID picks the board a new todo belongs to. Subtasks inherit the
// parent's board; top-level todos must name a board explicitly.
func resolveBoardID(ctx context.Context, tx *sql.Tx, parentID *int64, supplied *int64) (int64, error) {
	if parentID != nil {
		var bid int64
		err := tx.QueryRowContext(ctx, `SELECT board_id FROM todos WHERE id = ?`, *parentID).Scan(&bid)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, err
		}
		return bid, nil
	}
	if supplied == nil || *supplied <= 0 {
		return 0, ErrNoBoard
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards WHERE id = ?`, *supplied).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, ErrBoardNotFound
	}
	return *supplied, nil
}

// Get returns a single todo with labels.
func (d *DB) Get(ctx context.Context, id int64) (models.Todo, error) {
	var t models.Todo
	var parentID sql.NullInt64
	var completed int
	var dueNS, recurNS sql.NullString
	err := d.conn.QueryRowContext(ctx, `
        SELECT id, board_id, title, description, completed, parent_id, position, due_date, recurrence
        FROM todos WHERE id = ?`, id).Scan(
		&t.ID, &t.BoardID, &t.Title, &t.Description, &completed, &parentID, &t.Position, &dueNS, &recurNS)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Todo{}, ErrNotFound
	}
	if err != nil {
		return models.Todo{}, err
	}
	t.Completed = completed != 0
	if parentID.Valid {
		pid := parentID.Int64
		t.ParentID = &pid
	}
	if dueNS.Valid {
		t.DueDate = dueNS.String
	}
	if recurNS.Valid && recurNS.String != "" {
		var rc models.Recurrence
		if err := json.Unmarshal([]byte(recurNS.String), &rc); err != nil {
			return models.Todo{}, err
		}
		t.Recurrence = &rc
	}
	wrapped := []models.Todo{t}
	if err := d.attachLabels(ctx, wrapped); err != nil {
		return models.Todo{}, err
	}
	return wrapped[0], nil
}

// SetCompleted sets the completed flag on a todo and, recursively, all of its
// descendants. A single recursive CTE walks the subtree so the cascade is
// atomic. Returns the updated root todo.
func (d *DB) SetCompleted(ctx context.Context, id int64, completed bool) (models.Todo, error) {
	// Completing a recurring todo spawns a fresh incomplete instance (clone-next)
	// before the completion cascade runs. Un-completing does not spawn.
	if completed {
		if err := d.spawnNextIfRecurring(ctx, id); err != nil {
			return models.Todo{}, err
		}
	}
	value := boolToInt(completed)
	res, err := d.conn.ExecContext(ctx, `
        WITH descendants(id) AS (
            SELECT id FROM todos WHERE id = ?
            UNION ALL
            SELECT t.id FROM todos t JOIN descendants d ON t.parent_id = d.id
        )
        UPDATE todos SET completed = ?
        WHERE id IN (SELECT id FROM descendants)`, id, value)
	if err != nil {
		return models.Todo{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.Todo{}, ErrNotFound
	}
	return d.Get(ctx, id)
}

// spawnNextIfRecurring clones a recurring todo's next occurrence. If the todo
// has no recurrence rule it is a no-op. Otherwise it inserts a new incomplete
// todo (same board, parent, title, description, recurrence rule; due date
// advanced) at the end of the sibling group and copies the original's labels,
// leaving the soon-to-be-completed original in place. Runs in its own tx.
func (d *DB) spawnNextIfRecurring(ctx context.Context, id int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var title, description string
	var boardID int64
	var parentID sql.NullInt64
	var dueNS, recurNS sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT title, description, board_id, parent_id, due_date, recurrence FROM todos WHERE id = ?`, id).
		Scan(&title, &description, &boardID, &parentID, &dueNS, &recurNS)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Not recurring: nothing to do.
	if !recurNS.Valid || recurNS.String == "" {
		return nil
	}
	var rc models.Recurrence
	if err := json.Unmarshal([]byte(recurNS.String), &rc); err != nil {
		return fmt.Errorf("unmarshal recurrence: %w", err)
	}
	var due string
	if dueNS.Valid {
		due = dueNS.String
	}
	next, recurring, err := nextDueDate(due, rc)
	if err != nil {
		return err
	}
	if !recurring {
		// The recurrence window has ended (next would fall past EndDate); leave
		// the just-completed original and spawn no further clone.
		return nil
	}

	// Append the clone to the end of its sibling group (no shift needed).
	var parent *int64
	if parentID.Valid {
		pid := parentID.Int64
		parent = &pid
	}
	count, err := countSiblings(ctx, tx, boardID, parent)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO todos (board_id, title, description, completed, parent_id, position, due_date, recurrence)
         VALUES (?, ?, ?, 0, ?, ?, ?, ?)`,
		boardID, title, description, toNullInt64(parent), count, next, recurNS.String)
	if err != nil {
		return fmt.Errorf("insert recurring clone: %w", err)
	}
	cloneID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	// Copy the original's labels onto the clone in one statement.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO todo_labels (todo_id, label_id) SELECT ?, label_id FROM todo_labels WHERE todo_id = ?`,
		cloneID, id); err != nil {
		return fmt.Errorf("copy recurring clone labels: %w", err)
	}
	return tx.Commit()
}

// nextDueDate computes the next due date strictly after the current one per a
// Todoist-style recurrence rule. With rc.FromCompletion set (Todoist's
// "every!") the recurrence advances from today (the completion date) rather
// than the stored due date. If the next occurrence would fall after rc.EndDate
// the recurrence window has closed and recurring is false, so the caller skips
// spawning a clone. An unknown frequency errors out defensively even though
// validation ran at the API layer.
func nextDueDate(current string, rc models.Recurrence) (next string, recurring bool, err error) {
	const layout = "2006-01-02"
	base, perr := time.Parse(layout, current)
	if perr != nil || rc.FromCompletion {
		base = time.Now().UTC()
	}
	var t time.Time
	switch rc.Frequency {
	case "daily":
		t = base.AddDate(0, 0, rc.Interval)
	case "weekly":
		t, err = nextWeekly(base, rc)
	case "monthly":
		t, err = nextMonthly(base, rc)
	case "yearly":
		t = addYears(base, rc.Interval)
	default:
		return "", false, fmt.Errorf("%w: unknown frequency %q", ErrInvalidInput, rc.Frequency)
	}
	if err != nil {
		return "", false, err
	}
	if rc.EndDate != "" && t.Format(layout) > rc.EndDate {
		return "", false, nil // recurrence window closed
	}
	return t.Format(layout), true, nil
}

// nextWeekly advances to the next target weekday. With no weekdays set it is a
// plain every-N-weeks step. With weekdays set, active weeks are every
// rc.Interval weeks anchored on base's week: a date is due iff its weekday is a
// target and its week index (whole weeks since base's Sunday) is a multiple of
// rc.Interval. This correctly handles "every 2 weeks on mon, wed".
func nextWeekly(base time.Time, rc models.Recurrence) (time.Time, error) {
	if len(rc.Weekdays) == 0 {
		return base.AddDate(0, 0, 7*rc.Interval), nil
	}
	targets := make(map[int]bool, len(rc.Weekdays))
	for _, w := range rc.Weekdays {
		targets[w] = true
	}
	baseWD := int(base.Weekday())
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
	return time.Time{}, fmt.Errorf("%w: no weekly target after base", ErrInvalidInput)
}

// nextMonthly advances to the next month-level target. Active months are every
// rc.Interval months from base's month; within each, candidate days come from
// MonthDays, LastDay, NthWeekday, or (if none set) the base day-of-month. The
// smallest candidate strictly after base wins.
func nextMonthly(base time.Time, rc models.Recurrence) (time.Time, error) {
	for offset := 0; offset <= 1200; offset++ {
		if offset%rc.Interval != 0 {
			continue
		}
		year := base.Year() + (int(base.Month())-1+offset)/12
		month := time.Month((int(base.Month())-1+offset)%12 + 1)
		for _, c := range monthCandidates(year, month, base, rc) {
			if c.After(base) {
				return c, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("%w: no monthly target after base", ErrInvalidInput)
}

// monthCandidates builds the set of due days in (year, month) for a monthly
// rule, sorted ascending. Targets out of range for the month are dropped.
func monthCandidates(year int, month time.Month, base time.Time, rc models.Recurrence) []time.Time {
	loc := base.Location()
	dim := daysInMonthYM(year, month)
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
	dim := daysInMonthYM(year, month)
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

// daysInMonthYM returns the number of days in (year, month).
func daysInMonthYM(year int, month time.Month) int {
	firstOfNext := time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
	return firstOfNext.AddDate(0, 0, -1).Day()
}

// toNullString maps a pointer string to a bind value: nil -> nil (NULL), else
// the pointed-at string. Mirrors toNullInt64.
func toNullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// recurrenceJSON marshals a recurrence rule for storage, returning nil (NULL)
// when the rule is absent. The marshaled string is reused verbatim by the
// clone path so a recurrence chain continues unchanged.
func recurrenceJSON(rc *models.Recurrence) any {
	if rc == nil {
		return nil
	}
	b, err := json.Marshal(rc)
	if err != nil {
		// Recurrence is a fixed-shape struct; marshal only fails on a cycle,
		// which cannot occur here. Store NULL rather than failing the write.
		return nil
	}
	return string(b)
}

// Update applies a partial update to a todo. Position/ParentID changes route
// through Move for correct sibling reordering.
func (d *DB) Update(ctx context.Context, id int64, in models.UpdateTodo) (models.Todo, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Confirm the todo exists; scalar fields don't depend on current values.
	var exists int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE id = ?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Todo{}, ErrNotFound
		}
		return models.Todo{}, err
	}

	var sets []string
	var args []any
	if in.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *in.Title)
	}
	if in.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *in.Description)
	}
	if in.Completed != nil {
		sets = append(sets, "completed = ?")
		args = append(args, boolToInt(*in.Completed))
	}
	if in.DueDateSet {
		sets = append(sets, "due_date = ?")
		args = append(args, toNullString(in.DueDate)) // nil clears the column
	}
	if in.RecurrenceSet {
		sets = append(sets, "recurrence = ?")
		args = append(args, recurrenceJSON(in.Recurrence)) // nil clears
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := tx.ExecContext(ctx,
			`UPDATE todos SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return models.Todo{}, err
		}
	}
	if in.Labels != nil {
		if err := setLabelsTx(ctx, tx, id, *in.Labels); err != nil {
			return models.Todo{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Todo{}, err
	}

	// Parent/position changes go through Move to handle reordering.
	// OptionalParent encodes the absent/null/value tri-state; if Set is false
	// the parent is left unchanged, otherwise ID nil = root, non-nil = parent.
	if in.Set || in.Position != nil {
		mt := models.MoveTodo{Position: in.Position, OptionalParent: in.OptionalParent}
		if _, err := d.Move(ctx, id, mt); err != nil {
			return models.Todo{}, err
		}
	}
	return d.Get(ctx, id)
}

// Move relocates a todo within the tree. It may change its parent, its
// position among its siblings, or both. Other siblings are shifted to keep
// positions gapless within both the old and new sibling groups.
func (d *DB) Move(ctx context.Context, id int64, in models.MoveTodo) (models.Todo, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var curParent sql.NullInt64
	var curPos int
	var curBoard int64
	err = tx.QueryRowContext(ctx, `SELECT board_id, parent_id, position FROM todos WHERE id = ?`, id).
		Scan(&curBoard, &curParent, &curPos)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Todo{}, ErrNotFound
	}
	if err != nil {
		return models.Todo{}, err
	}

	var newParent *int64
	// Target board defaults to the todo's current board. If the todo is being
	// nested under a parent in another board, the parent's board wins (subtasks
	// always live on their parent's board).
	targetBoard := curBoard
	if in.Set {
		// Client explicitly set parentId (either a value or null).
		newParent = in.ID
		if newParent != nil {
			if *newParent == id {
				return models.Todo{}, ErrSelfParent
			}
			if err := checkCycle(ctx, tx, id, *newParent); err != nil {
				return models.Todo{}, err
			}
			// Inherit the parent's board so a subtask always shares its board.
			if err := tx.QueryRowContext(ctx,
				`SELECT board_id FROM todos WHERE id = ?`, *newParent).Scan(&targetBoard); err != nil {
				return models.Todo{}, err
			}
		}
		// newParent == nil here means "move to root"; keep current board.
	} else if curParent.Valid {
		p := curParent.Int64
		newParent = &p
	}

	// If only position changes and it equals current, nothing to do.
	targetPos := curPos
	if in.Position != nil {
		targetPos = *in.Position
	}
	movingToNewParent := in.Set && !sameParent(newParent, nullableToPtr(curParent))
	// Remove from current ordering (gap-closer). Scope by board so reordering
	// in one board never perturbs positions in another.
	if _, err := tx.ExecContext(ctx,
		`UPDATE todos SET position = position - 1
         WHERE board_id = ? AND parent_id IS ? AND position > ? AND id <> ?`,
		curBoard, curParent, curPos, id); err != nil {
		return models.Todo{}, err
	}

	if movingToNewParent {
		count, err := countSiblings(ctx, tx, targetBoard, newParent)
		if err != nil {
			return models.Todo{}, err
		}
		if in.Position == nil {
			targetPos = count
		} else {
			targetPos = min(targetPos, count)
		}
	} else if in.Position != nil {
		// Same sibling group: clamp into [0, count-1] of remaining siblings.
		count, err := countSiblings(ctx, tx, targetBoard, newParent)
		if err != nil {
			return models.Todo{}, err
		}
		targetPos = max(0, min(targetPos, count-1))
	}

	// Make room at target in the (possibly new) sibling group.
	if _, err := tx.ExecContext(ctx,
		`UPDATE todos SET position = position + 1
         WHERE board_id = ? AND parent_id IS ? AND position >= ? AND id <> ?`,
		targetBoard, toNullInt64(newParent), targetPos, id); err != nil {
		return models.Todo{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE todos SET board_id = ?, parent_id = ?, position = ? WHERE id = ?`,
		targetBoard, toNullInt64(newParent), targetPos, id); err != nil {
		return models.Todo{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Todo{}, err
	}
	return d.Get(ctx, id)
}

// checkCycle ensures assigning newParent as the todo's parent doesn't create a
// loop (newParent must not be a descendant of id).
func checkCycle(ctx context.Context, tx *sql.Tx, id, newParent int64) error {
	cur := newParent
	seen := make(map[int64]struct{})
	for cur != 0 {
		if cur == id {
			return ErrCycle
		}
		if _, stop := seen[cur]; stop {
			break
		}
		seen[cur] = struct{}{}
		var p sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT parent_id FROM todos WHERE id = ?`, cur).Scan(&p)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if !p.Valid {
			break
		}
		cur = p.Int64
	}
	return nil
}

// Delete removes a todo; ON DELETE CASCADE clears children and label links.
func (d *DB) Delete(ctx context.Context, id int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var parent sql.NullInt64
	var pos int
	var boardID int64
	err = tx.QueryRowContext(ctx, `SELECT board_id, parent_id, position FROM todos WHERE id = ?`, id).
		Scan(&boardID, &parent, &pos)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE todos SET position = position - 1 WHERE board_id = ? AND parent_id IS ? AND position > ?`,
		boardID, parent, pos); err != nil {
		return err
	}
	return tx.Commit()
}

// ListLabels returns all known labels, combining ad-hoc labels (attached to
// todos) with any predefined labels. The result is de-duplicated and sorted.
func (d *DB) ListLabels(ctx context.Context) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx, `
SELECT name FROM (
    SELECT name FROM labels
    UNION
    SELECT name FROM predefined_labels
)
ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddPredefinedLabel records a label as predefined so it always appears in the
// global label list even when no todo uses it. Idempotent on name.
func (d *DB) AddPredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO predefined_labels (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name)
	return err
}

// RemovePredefinedLabel removes a label from the predefined set. Ad-hoc usages
// on todos are unaffected.
func (d *DB) RemovePredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.conn.ExecContext(ctx, `DELETE FROM predefined_labels WHERE name = ?`, name)
	return err
}

// resolvePosition returns the absolute insert position. If requested is nil the
// item is appended to the end of the sibling group.
func resolvePosition(ctx context.Context, tx *sql.Tx, boardID int64, parent *int64, requested *int) (int, error) {
	count, err := countSiblings(ctx, tx, boardID, parent)
	if err != nil {
		return 0, err
	}
	if requested == nil {
		return count, nil
	}
	return max(0, min(*requested, count)), nil
}

// countSiblings counts todos sharing the same (board, parent). Both scopes are
// required: parent_id alone is ambiguous once multiple boards exist, since every
// board has its own set of root todos (parent_id IS NULL).
func countSiblings(ctx context.Context, tx *sql.Tx, boardID int64, parent *int64) (int, error) {
	var c int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM todos WHERE board_id = ? AND parent_id IS ?`,
		boardID, toNullInt64(parent)).Scan(&c)
	return c, err
}

// shiftSiblingsAfter increases positions of siblings that come at-or-after pos,
// skipping the inserted todo itself (to avoid a unique-ish collision if one existed).
func shiftSiblingsAfter(ctx context.Context, tx *sql.Tx, boardID int64, parent *int64, pos, delta int, skipID int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE todos SET position = position + ? WHERE board_id = ? AND parent_id IS ? AND position >= ? AND id <> ?`,
		delta, boardID, toNullInt64(parent), pos, skipID)
	return err
}

// setLabelsTx replaces the set of labels for a todo, creating any unknown
// label names as needed.
func setLabelsTx(ctx context.Context, tx *sql.Tx, todoID int64, labels []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_labels WHERE todo_id = ?`, todoID); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		// Upsert the label and fetch its id in one round-trip via RETURNING.
		var labelID int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO labels (name) VALUES (?)
			 ON CONFLICT(name) DO UPDATE SET name = excluded.name
			 RETURNING id`, l).Scan(&labelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO todo_labels (todo_id, label_id) VALUES (?, ?)`, todoID, labelID); err != nil {
			return err
		}
	}
	return nil
}

func toNullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ListBoards returns every board ordered by position.
func (d *DB) ListBoards(ctx context.Context) ([]models.Board, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, position FROM boards ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Board
	for rows.Next() {
		var b models.Board
		if err := rows.Scan(&b.ID, &b.Name, &b.Position); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBoard returns a single board by id.
func (d *DB) GetBoard(ctx context.Context, id int64) (models.Board, error) {
	var b models.Board
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, name, position FROM boards WHERE id = ?`, id).
		Scan(&b.ID, &b.Name, &b.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Board{}, ErrBoardNotFound
	}
	if err != nil {
		return models.Board{}, err
	}
	return b, nil
}

// CreateBoard inserts a board. If Position is nil it is appended after the
// current max position; later boards are shifted to keep ordering gapless.
func (d *DB) CreateBoard(ctx context.Context, in models.CreateBoard) (models.Board, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Board{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var pos int
	if in.Position != nil {
		pos = max(0, *in.Position)
		if _, err := tx.ExecContext(ctx,
			`UPDATE boards SET position = position + 1 WHERE position >= ?`, pos); err != nil {
			return models.Board{}, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM boards`).Scan(&pos); err != nil {
			return models.Board{}, err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO boards (name, position) VALUES (?, ?)`, in.Name, pos)
	if err != nil {
		return models.Board{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return models.Board{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Board{}, err
	}
	return d.GetBoard(ctx, id)
}

// UpdateBoard applies a partial update to a board. Position changes shift
// siblings to keep ordering gapless.
func (d *DB) UpdateBoard(ctx context.Context, id int64, in models.UpdateBoard) (models.Board, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Board{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var curPos int
	err = tx.QueryRowContext(ctx, `SELECT position FROM boards WHERE id = ?`, id).Scan(&curPos)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Board{}, ErrBoardNotFound
	}
	if err != nil {
		return models.Board{}, err
	}

	if in.Name != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE boards SET name = ? WHERE id = ?`, *in.Name, id); err != nil {
			return models.Board{}, err
		}
	}
	if in.Position != nil {
		var maxPos int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM boards`).Scan(&maxPos); err != nil {
			return models.Board{}, err
		}
		target := max(0, min(*in.Position, maxPos))
		if target != curPos {
			if target > curPos {
				if _, err := tx.ExecContext(ctx,
					`UPDATE boards SET position = position - 1 WHERE position > ? AND position <= ? AND id <> ?`,
					curPos, target, id); err != nil {
					return models.Board{}, err
				}
			} else {
				if _, err := tx.ExecContext(ctx,
					`UPDATE boards SET position = position + 1 WHERE position >= ? AND position < ? AND id <> ?`,
					target, curPos, id); err != nil {
					return models.Board{}, err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE boards SET position = ? WHERE id = ?`, target, id); err != nil {
				return models.Board{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Board{}, err
	}
	return d.GetBoard(ctx, id)
}

// DeleteBoard removes a board and (via ON DELETE CASCADE) all of its todos.
// The last remaining board cannot be deleted.
func (d *DB) DeleteBoard(ctx context.Context, id int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastBoard
	}
	var pos int
	err = tx.QueryRowContext(ctx, `SELECT position FROM boards WHERE id = ?`, id).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBoardNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM boards WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE boards SET position = position - 1 WHERE position > ?`, pos); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableToPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func sameParent(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Sentinel errors surfaced to the API layer.
var (
	ErrNotFound      = errors.New("todo not found")
	ErrSelfParent    = errors.New("todo cannot be its own parent")
	ErrCycle         = errors.New("moving this todo under that parent would create a cycle")
	ErrNoBoard       = errors.New("boardId is required for top-level todos")
	ErrBoardNotFound = errors.New("board not found")
	ErrLastBoard     = errors.New("cannot delete the last remaining board")
	ErrInvalidInput  = errors.New("invalid input")
)

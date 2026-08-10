// Package db provides the persistence layer for todos, backed by SQLite or
// Postgres. All data access goes through the bun query builder so the same code
// runs on either engine; the dialect (placeholder rebinding, identity columns,
// type mapping) is handled by bun.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/greboid/todo/internal/models"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/schema"
)

// DB is the todo persistence layer.
type DB struct {
	db *bun.DB
}

// New opens (or creates) the database for driver and applies the schema. driver
// selects the backend: "sqlite" (default; also "sqlite3"/"") opens a local
// SQLite file at dsn with a 5s busy timeout and foreign keys on; "postgres"
// (also "pg"/"postgresql") opens dsn as a Postgres connection string. SQLite
// serializes writes so its pool is capped at one connection; Postgres is left
// at the driver default.
func New(ctx context.Context, driver, dsn string) (*DB, error) {
	var sqldb *sql.DB
	var dl schema.Dialect
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		var err error
		sqldb, err = sql.Open(sqliteshim.ShimName, dsn+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
		if err != nil {
			return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
		}
		sqldb.SetMaxOpenConns(1) // sqlite serializes writes; keep it simple and correct
		dl = sqlitedialect.New()
	case "postgres", "postgresql", "pg":
		sqldb = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
		dl = pgdialect.New()
	default:
		return nil, fmt.Errorf("unsupported db driver %q (want sqlite or postgres)", driver)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("ping %s: %w", dl.Name(), err)
	}
	d := &DB{db: bun.NewDB(sqldb, dl)}
	if err := d.migrate(ctx); err != nil {
		_ = d.db.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the database handle.
func (d *DB) Close() error { return d.db.Close() }

// Persistence models. These are db-internal (unexported) and separate from the
// internal/models wire DTOs so the JSON contract is unchanged. Completed stays
// an int: scanning an existing SQLite INTEGER (or Postgres integer) column into
// a bool fails in database/sql, and the mapper converts it for the DTO.

type board struct {
	bun.BaseModel `bun:"table:boards"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull"`
	Position      int    `bun:"position,notnull"`
}

type todo struct {
	bun.BaseModel `bun:"table:todos"`
	ID            int64   `bun:"id,pk,autoincrement"`
	BoardID       int64   `bun:"board_id,notnull"`
	Title         string  `bun:"title,notnull"`
	Description   string  `bun:"description,notnull"`
	Completed     int     `bun:"completed,notnull"`
	ParentID      *int64  `bun:"parent_id,nullzero"`
	Position      int     `bun:"position,notnull"`
	DueDate       *string `bun:"due_date,nullzero"`
	Recurrence    *string `bun:"recurrence,nullzero"` // raw JSON text, as today
}

type label struct {
	bun.BaseModel `bun:"table:labels"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull,unique"`
}

type todoLabel struct {
	bun.BaseModel `bun:"table:todo_labels"`
	TodoID        int64 `bun:"todo_id,pk"`
	LabelID       int64 `bun:"label_id,pk"`
}

type predefinedLabel struct {
	bun.BaseModel `bun:"table:predefined_labels"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull,unique"`
}

func (b board) toModel() models.Board {
	return models.Board{ID: b.ID, Name: b.Name, Position: b.Position}
}

func (t todo) toModel(labels []string) models.Todo {
	m := models.Todo{
		ID:          t.ID,
		BoardID:     t.BoardID,
		Title:       t.Title,
		Description: t.Description,
		Completed:   t.Completed != 0,
		ParentID:    t.ParentID,
		Position:    t.Position,
		Labels:      labels,
	}
	if t.DueDate != nil {
		m.DueDate = *t.DueDate
	}
	if t.Recurrence != nil && *t.Recurrence != "" {
		var rc models.Recurrence
		if err := json.Unmarshal([]byte(*t.Recurrence), &rc); err == nil {
			m.Recurrence = &rc
		}
	}
	return m
}

// migrate creates the schema. Each statement is IF NOT EXISTS, so it is a no-op
// on an already-present table (existing SQLite DBs are left untouched; column
// names match the old schema so scanning is unaffected). Identity/sequence,
// composite primary keys, foreign keys, and indexes are all dialect-generated
// by bun so the same DDL runs on SQLite and Postgres.
func (d *DB) migrate(ctx context.Context) error {
	steps := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*board)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*todo)(nil)).IfNotExists().
				ForeignKey(`("board_id") REFERENCES boards(id) ON DELETE CASCADE`).
				ForeignKey(`("parent_id") REFERENCES todos(id) ON DELETE CASCADE`).
				Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*label)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*todoLabel)(nil)).IfNotExists().
				ForeignKey(`("todo_id") REFERENCES todos(id) ON DELETE CASCADE`).
				ForeignKey(`("label_id") REFERENCES labels(id) ON DELETE CASCADE`).
				Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*predefinedLabel)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateIndex().IfNotExists().Index("idx_boards_position").
				Table("boards").Column("position", "id").Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateIndex().IfNotExists().Index("idx_todos_parent").
				Table("todos").Column("board_id", "parent_id", "position").Exec(ctx)
			return err
		},
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ListAll returns every todo on the given board with its labels attached.
// boardID <= 0 returns todos across all boards (used by the legacy complete-cascade
// sync path which already re-scopes on the client).
func (d *DB) ListAll(ctx context.Context, boardID int64) ([]models.Todo, error) {
	var ts []todo
	q := d.db.NewSelect().Model(&ts)
	if boardID > 0 {
		q = q.Where("board_id = ?", boardID)
	}
	if err := q.OrderExpr("COALESCE(parent_id, 0), position, id").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]models.Todo, len(ts))
	for i, t := range ts {
		out[i] = t.toModel(nil)
	}
	if err := d.attachLabels(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachLabels fetches the labels for each todo and stamps them in place.
func (d *DB) attachLabels(ctx context.Context, ts []models.Todo) error {
	if len(ts) == 0 {
		return nil
	}
	ids := make([]int64, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	var rows []struct {
		TodoID int64  `bun:"todo_id"`
		Name   string `bun:"name"`
	}
	err := d.db.NewSelect().
		ColumnExpr("tl.todo_id, l.name").
		TableExpr("todo_labels AS tl").
		Join("JOIN labels AS l ON l.id = tl.label_id").
		Where("tl.todo_id IN (?)", bun.In(ids)).
		OrderExpr("l.name").
		Scan(ctx, &rows)
	if err != nil {
		return err
	}
	byID := make(map[int64][]string, len(ts))
	for _, r := range rows {
		byID[r.TodoID] = append(byID[r.TodoID], r.Name)
	}
	for i := range ts {
		ts[i].Labels = byID[ts[i].ID]
	}
	return nil
}

// labelNamesForTodo returns the sorted label names attached to a single todo.
// Usable inside a transaction (run is a bun.IDB) for the clone-next path.
func labelNamesForTodo(ctx context.Context, run bun.IDB, todoID int64) ([]string, error) {
	var names []string
	err := run.NewSelect().
		ColumnExpr("l.name").
		TableExpr("todo_labels AS tl").
		Join("JOIN labels AS l ON l.id = tl.label_id").
		Where("tl.todo_id = ?", todoID).
		OrderExpr("l.name").
		Scan(ctx, &names)
	return names, err
}

// Create inserts a todo. If Position is nil the item is appended after the
// current max position within its sibling group. Siblings after the inserted
// position are shifted down to keep ordering gapless.
//
// BoardID resolution: if ParentID is set the new todo inherits the parent's
// board (subtasks always live on their parent's board). Otherwise BoardID must
// be supplied; if neither is present the request is rejected.
func (d *DB) Create(ctx context.Context, in models.CreateTodo) (models.Todo, error) {
	tx, err := d.db.BeginTx(ctx, nil)
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
	t := todo{
		BoardID:     boardID,
		Title:       in.Title,
		Description: in.Description,
		ParentID:    in.ParentID,
		Position:    pos,
		DueDate:     in.DueDate,
		Recurrence:  recurrenceString(in.Recurrence),
	}
	if err := tx.NewInsert().Model(&t).Returning("id").Scan(ctx, &t.ID); err != nil {
		return models.Todo{}, fmt.Errorf("insert todo: %w", err)
	}
	if err := shiftSiblingsAfter(ctx, tx, boardID, in.ParentID, pos, 1, t.ID); err != nil {
		return models.Todo{}, err
	}
	if err := setLabelsTx(ctx, tx, t.ID, in.Labels); err != nil {
		return models.Todo{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Todo{}, err
	}
	return d.Get(ctx, t.ID)
}

// resolveBoardID picks the board a new todo belongs to. Subtasks inherit the
// parent's board; top-level todos must name a board explicitly.
func resolveBoardID(ctx context.Context, run bun.IDB, parentID, supplied *int64) (int64, error) {
	if parentID != nil {
		var bid int64
		err := run.NewSelect().ColumnExpr("board_id").TableExpr("todos").
			Where("id = ?", *parentID).Scan(ctx, &bid)
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
	var count int
	if err := run.NewSelect().ColumnExpr("COUNT(*)").TableExpr("boards").
		Where("id = ?", *supplied).Scan(ctx, &count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, ErrBoardNotFound
	}
	return *supplied, nil
}

// Get returns a single todo with labels.
func (d *DB) Get(ctx context.Context, id int64) (models.Todo, error) {
	var t todo
	err := d.db.NewSelect().Model(&t).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Todo{}, ErrNotFound
	}
	if err != nil {
		return models.Todo{}, err
	}
	wrapped := []models.Todo{t.toModel(nil)}
	if err := d.attachLabels(ctx, wrapped); err != nil {
		return models.Todo{}, err
	}
	return wrapped[0], nil
}

// SetCompleted sets the completed flag on a todo and, recursively, all of its
// descendants. Descendants are walked in Go (parent_id by parent_id) and the
// whole subtree is updated in one statement, so the cascade is as atomic as a
// single UPDATE. Returns the updated root todo.
func (d *DB) SetCompleted(ctx context.Context, id int64, completed bool) (models.Todo, error) {
	// Completing a recurring todo spawns a fresh incomplete instance (clone-next)
	// before the completion cascade runs. Un-completing does not spawn.
	if completed {
		if err := d.spawnNextIfRecurring(ctx, id); err != nil {
			return models.Todo{}, err
		}
	}
	ids, err := descendantIDs(ctx, d.db, id)
	if err != nil {
		return models.Todo{}, err
	}
	res, err := d.db.NewUpdate().Model((*todo)(nil)).
		Set("completed = ?", boolToInt(completed)).
		Where("id IN (?)", bun.In(ids)).Exec(ctx)
	if err != nil {
		return models.Todo{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.Todo{}, ErrNotFound
	}
	return d.Get(ctx, id)
}

// descendantIDs returns id and all of its transitive descendants (children,
// grandchildren, ...), walking parent_id in Go. Order does not matter: the
// caller updates the whole set at once. A seen set guards against any cycle.
func descendantIDs(ctx context.Context, run bun.IDB, id int64) ([]int64, error) {
	var ids []int64
	seen := make(map[int64]struct{})
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, ok := seen[cur]; ok {
			continue
		}
		seen[cur] = struct{}{}
		ids = append(ids, cur)
		var children []int64
		if err := run.NewSelect().ColumnExpr("id").TableExpr("todos").
			Where("parent_id = ?", cur).Scan(ctx, &children); err != nil {
			return nil, err
		}
		queue = append(queue, children...)
	}
	return ids, nil
}

// spawnNextIfRecurring clones a recurring todo's next occurrence. If the todo
// has no recurrence rule it is a no-op. Otherwise it inserts a new incomplete
// todo (same board, parent, title, description, recurrence rule; due date
// advanced) at the end of the sibling group and copies the original's labels,
// leaving the soon-to-be-completed original in place. Runs in its own tx.
func (d *DB) spawnNextIfRecurring(ctx context.Context, id int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var src todo
	err = tx.NewSelect().Model(&src).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Not recurring: nothing to do.
	if src.Recurrence == nil || *src.Recurrence == "" {
		return nil
	}
	var rc models.Recurrence
	if err := json.Unmarshal([]byte(*src.Recurrence), &rc); err != nil {
		return fmt.Errorf("unmarshal recurrence: %w", err)
	}
	var due string
	if src.DueDate != nil {
		due = *src.DueDate
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
	count, err := countSiblings(ctx, tx, src.BoardID, src.ParentID)
	if err != nil {
		return err
	}
	nextDate := next
	clone := todo{
		BoardID:     src.BoardID,
		Title:       src.Title,
		Description: src.Description,
		ParentID:    src.ParentID,
		Position:    count,
		DueDate:     &nextDate,
		Recurrence:  src.Recurrence,
	}
	if err := tx.NewInsert().Model(&clone).Returning("id").Scan(ctx, &clone.ID); err != nil {
		return fmt.Errorf("insert recurring clone: %w", err)
	}
	// Copy the original's labels onto the clone (re-linked by name).
	sourceLabels, err := labelNamesForTodo(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("copy recurring clone labels: %w", err)
	}
	if err := setLabelsTx(ctx, tx, clone.ID, sourceLabels); err != nil {
		return err
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

// recurrenceString is the *string form of recurrenceJSON, used to populate a
// model field for insert (the JSON is stored verbatim so a recurrence chain
// continues unchanged).
func recurrenceString(rc *models.Recurrence) *string {
	if rc == nil {
		return nil
	}
	b, err := json.Marshal(rc)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// Update applies a partial update to a todo. Position/ParentID changes route
// through Move for correct sibling reordering.
func (d *DB) Update(ctx context.Context, id int64, in models.UpdateTodo) (models.Todo, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Confirm the todo exists; scalar fields don't depend on current values.
	var exists int64
	if err := tx.NewSelect().ColumnExpr("id").TableExpr("todos").
		Where("id = ?", id).Scan(ctx, &exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Todo{}, ErrNotFound
		}
		return models.Todo{}, err
	}

	q := tx.NewUpdate().Model((*todo)(nil)).Where("id = ?", id)
	if in.Title != nil {
		q = q.Set("title = ?", *in.Title)
	}
	if in.Description != nil {
		q = q.Set("description = ?", *in.Description)
	}
	if in.Completed != nil {
		q = q.Set("completed = ?", boolToInt(*in.Completed))
	}
	if in.DueDateSet {
		q = q.Set("due_date = ?", toNullString(in.DueDate)) // nil clears the column
	}
	if in.RecurrenceSet {
		q = q.Set("recurrence = ?", recurrenceJSON(in.Recurrence)) // nil clears
	}
	if in.Labels != nil {
		if err := setLabelsTx(ctx, tx, id, *in.Labels); err != nil {
			return models.Todo{}, err
		}
	}
	// An UPDATE with no SET is invalid SQL; only run when a field changed.
	if in.Title != nil || in.Description != nil || in.Completed != nil || in.DueDateSet || in.RecurrenceSet {
		if _, err := q.Exec(ctx); err != nil {
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
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Todo{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var cur todo
	err = tx.NewSelect().Model(&cur).Where("id = ?", id).Scan(ctx)
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
	targetBoard := cur.BoardID
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
			if err := tx.NewSelect().ColumnExpr("board_id").TableExpr("todos").
				Where("id = ?", *newParent).Scan(ctx, &targetBoard); err != nil {
				return models.Todo{}, err
			}
		}
		// newParent == nil here means "move to root"; keep current board.
	} else if cur.ParentID != nil {
		newParent = cur.ParentID
	}

	// If only position changes and it equals current, nothing to do.
	targetPos := cur.Position
	if in.Position != nil {
		targetPos = *in.Position
	}
	movingToNewParent := in.Set && !sameParent(newParent, cur.ParentID)
	// Remove from current ordering (gap-closer). Scope by board so reordering
	// in one board never perturbs positions in another. IS NOT DISTINCT FROM
	// is null-safe equality (nil binds NULL, matching root todos) on both engines.
	if _, err := tx.NewUpdate().Model((*todo)(nil)).
		Set("position = position - 1").
		Where("board_id = ?", cur.BoardID).
		Where("parent_id IS NOT DISTINCT FROM ?", toNullInt64(cur.ParentID)).
		Where("position > ?", cur.Position).
		Where("id <> ?", id).Exec(ctx); err != nil {
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
	if _, err := tx.NewUpdate().Model((*todo)(nil)).
		Set("position = position + 1").
		Where("board_id = ?", targetBoard).
		Where("parent_id IS NOT DISTINCT FROM ?", toNullInt64(newParent)).
		Where("position >= ?", targetPos).
		Where("id <> ?", id).Exec(ctx); err != nil {
		return models.Todo{}, err
	}

	if _, err := tx.NewUpdate().Model((*todo)(nil)).
		Set("board_id = ?", targetBoard).
		Set("parent_id = ?", toNullInt64(newParent)).
		Set("position = ?", targetPos).
		Where("id = ?", id).Exec(ctx); err != nil {
		return models.Todo{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Todo{}, err
	}
	return d.Get(ctx, id)
}

// checkCycle ensures assigning newParent as the todo's parent doesn't create a
// loop (newParent must not be a descendant of id).
func checkCycle(ctx context.Context, run bun.IDB, id, newParent int64) error {
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
		err := run.NewSelect().ColumnExpr("parent_id").TableExpr("todos").
			Where("id = ?", cur).Scan(ctx, &p)
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
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var cur todo
	err = tx.NewSelect().Model(&cur).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.NewDelete().Model((*todo)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewUpdate().Model((*todo)(nil)).
		Set("position = position - 1").
		Where("board_id = ?", cur.BoardID).
		Where("parent_id IS NOT DISTINCT FROM ?", toNullInt64(cur.ParentID)).
		Where("position > ?", cur.Position).
		Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// ListLabels returns all known labels, combining ad-hoc labels (attached to
// todos) with any predefined labels. The two sets are fetched separately and
// merged in Go (bun parenthesizes compound selects per-branch, which SQLite
// rejects), then de-duplicated and sorted.
func (d *DB) ListLabels(ctx context.Context) ([]string, error) {
	var adhoc, predefined []string
	if err := d.db.NewSelect().ColumnExpr("name").TableExpr("labels").Scan(ctx, &adhoc); err != nil {
		return nil, err
	}
	if err := d.db.NewSelect().ColumnExpr("name").TableExpr("predefined_labels").Scan(ctx, &predefined); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(adhoc)+len(predefined))
	out := make([]string, 0, len(adhoc)+len(predefined))
	for _, n := range adhoc {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, n := range predefined {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// AddPredefinedLabel records a label as predefined so it always appears in the
// global label list even when no todo uses it. Idempotent on name.
func (d *DB) AddPredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.db.NewInsert().Model(&predefinedLabel{Name: name}).
		On("CONFLICT (name) DO NOTHING").Exec(ctx)
	return err
}

// RemovePredefinedLabel removes a label from the predefined set. Ad-hoc usages
// on todos are unaffected.
func (d *DB) RemovePredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.db.NewDelete().Model((*predefinedLabel)(nil)).Where("name = ?", name).Exec(ctx)
	return err
}

// resolvePosition returns the absolute insert position. If requested is nil the
// item is appended to the end of the sibling group.
func resolvePosition(ctx context.Context, run bun.IDB, boardID int64, parent *int64, requested *int) (int, error) {
	count, err := countSiblings(ctx, run, boardID, parent)
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
func countSiblings(ctx context.Context, run bun.IDB, boardID int64, parent *int64) (int, error) {
	var c int
	err := run.NewSelect().ColumnExpr("COUNT(*)").TableExpr("todos").
		Where("board_id = ?", boardID).
		Where("parent_id IS NOT DISTINCT FROM ?", toNullInt64(parent)).
		Scan(ctx, &c)
	return c, err
}

// shiftSiblingsAfter increases positions of siblings that come at-or-after pos,
// skipping the inserted todo itself (to avoid a unique-ish collision if one existed).
func shiftSiblingsAfter(ctx context.Context, run bun.IDB, boardID int64, parent *int64, pos, delta int, skipID int64) error {
	_, err := run.NewUpdate().Model((*todo)(nil)).
		Set("position = position + ?", delta).
		Where("board_id = ?", boardID).
		Where("parent_id IS NOT DISTINCT FROM ?", toNullInt64(parent)).
		Where("position >= ?", pos).
		Where("id <> ?", skipID).
		Exec(ctx)
	return err
}

// setLabelsTx replaces the set of labels for a todo, creating any unknown
// label names as needed.
func setLabelsTx(ctx context.Context, run bun.IDB, todoID int64, labels []string) error {
	if _, err := run.NewDelete().Model((*todoLabel)(nil)).Where("todo_id = ?", todoID).Exec(ctx); err != nil {
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
		var lid int64
		if err := run.NewInsert().Model(&label{Name: l}).
			On("CONFLICT (name) DO UPDATE SET name = excluded.name").
			Returning("id").Scan(ctx, &lid); err != nil {
			return err
		}
		if _, err := run.NewInsert().Model(&todoLabel{TodoID: todoID, LabelID: lid}).Ignore().Exec(ctx); err != nil {
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
	var bs []board
	if err := d.db.NewSelect().Model(&bs).OrderExpr("position, id").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]models.Board, len(bs))
	for i, b := range bs {
		out[i] = b.toModel()
	}
	return out, nil
}

// GetBoard returns a single board by id.
func (d *DB) GetBoard(ctx context.Context, id int64) (models.Board, error) {
	var b board
	err := d.db.NewSelect().Model(&b).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Board{}, ErrBoardNotFound
	}
	if err != nil {
		return models.Board{}, err
	}
	return b.toModel(), nil
}

// CreateBoard inserts a board. If Position is nil it is appended after the
// current max position; later boards are shifted to keep ordering gapless.
func (d *DB) CreateBoard(ctx context.Context, in models.CreateBoard) (models.Board, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Board{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var pos int
	if in.Position != nil {
		pos = max(0, *in.Position)
		if _, err := tx.NewUpdate().Model((*board)(nil)).
			Set("position = position + 1").Where("position >= ?", pos).
			Exec(ctx); err != nil {
			return models.Board{}, err
		}
	} else {
		if err := tx.NewSelect().ColumnExpr("COALESCE(MAX(position)+1, 0)").TableExpr("boards").
			Scan(ctx, &pos); err != nil {
			return models.Board{}, err
		}
	}
	b := board{Name: in.Name, Position: pos}
	if err := tx.NewInsert().Model(&b).Returning("id").Scan(ctx, &b.ID); err != nil {
		return models.Board{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Board{}, err
	}
	return d.GetBoard(ctx, b.ID)
}

// UpdateBoard applies a partial update to a board. Position changes shift
// siblings to keep ordering gapless.
func (d *DB) UpdateBoard(ctx context.Context, id int64, in models.UpdateBoard) (models.Board, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Board{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var curPos int
	err = tx.NewSelect().ColumnExpr("position").TableExpr("boards").
		Where("id = ?", id).Scan(ctx, &curPos)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Board{}, ErrBoardNotFound
	}
	if err != nil {
		return models.Board{}, err
	}

	if in.Name != nil {
		if _, err := tx.NewUpdate().Model((*board)(nil)).
			Set("name = ?", *in.Name).Where("id = ?", id).Exec(ctx); err != nil {
			return models.Board{}, err
		}
	}
	if in.Position != nil {
		var maxPos int
		if err := tx.NewSelect().ColumnExpr("COALESCE(MAX(position), 0)").TableExpr("boards").
			Scan(ctx, &maxPos); err != nil {
			return models.Board{}, err
		}
		target := max(0, min(*in.Position, maxPos))
		if target != curPos {
			if target > curPos {
				if _, err := tx.NewUpdate().Model((*board)(nil)).
					Set("position = position - 1").
					Where("position > ?", curPos).
					Where("position <= ?", target).
					Where("id <> ?", id).Exec(ctx); err != nil {
					return models.Board{}, err
				}
			} else {
				if _, err := tx.NewUpdate().Model((*board)(nil)).
					Set("position = position + 1").
					Where("position >= ?", target).
					Where("position < ?", curPos).
					Where("id <> ?", id).Exec(ctx); err != nil {
					return models.Board{}, err
				}
			}
			if _, err := tx.NewUpdate().Model((*board)(nil)).
				Set("position = ?", target).Where("id = ?", id).Exec(ctx); err != nil {
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
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.NewSelect().ColumnExpr("COUNT(*)").TableExpr("boards").Scan(ctx, &count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastBoard
	}
	var pos int
	err = tx.NewSelect().ColumnExpr("position").TableExpr("boards").
		Where("id = ?", id).Scan(ctx, &pos)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBoardNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.NewDelete().Model((*board)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewUpdate().Model((*board)(nil)).
		Set("position = position - 1").Where("position > ?", pos).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
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

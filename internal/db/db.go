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

	"github.com/greboid/todo/internal/filter"
	"github.com/greboid/todo/internal/models"
	"github.com/greboid/todo/internal/schedule"

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
	Priority      string  `bun:"priority,notnull,default:''"`
	DueDate       *string `bun:"due_date,nullzero"`
	Recurrence    *string `bun:"recurrence,nullzero"` // raw JSON text, as today
}

type label struct {
	bun.BaseModel `bun:"table:labels"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull,unique"`
	Color         string `bun:"color,default:''"`
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

type priority struct {
	bun.BaseModel `bun:"table:priorities"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull,unique"`
	Color         string `bun:"color,default:''"`
}

type predefinedPriority struct {
	bun.BaseModel `bun:"table:predefined_priorities"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull,unique"`
	Position      int    `bun:"position,notnull,default:0"`
}

type savedSearch struct {
	bun.BaseModel `bun:"table:saved_searches"`
	ID            int64  `bun:"id,pk,autoincrement"`
	Name          string `bun:"name,notnull"`
	Query         string `bun:"query,notnull"`
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
		Priority:    t.Priority,
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
		// Add the color column to pre-existing labels tables. IF NOT EXISTS
		// on CREATE TABLE does not add new columns to an existing table, so
		// we ALTER explicitly. Wrapped in a column-existence check so it is
		// a no-op once the column is present (Postgres lacks ADD COLUMN IF NOT
		// EXISTS in older versions; we check information_schema uniformly).
		func(ctx context.Context) error {
			return addColumnIfMissing(ctx, d.db, "labels", "color", "TEXT NOT NULL DEFAULT ''")
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
			_, err := d.db.NewCreateTable().Model((*priority)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		// Add the color column to pre-existing priorities tables (same pattern
		// as labels above).
		func(ctx context.Context) error {
			return addColumnIfMissing(ctx, d.db, "priorities", "color", "TEXT NOT NULL DEFAULT ''")
		},
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*predefinedPriority)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		// Saved searches: named filter queries re-appliable from the toolbar.
		func(ctx context.Context) error {
			_, err := d.db.NewCreateTable().Model((*savedSearch)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		// Add the priority column to pre-existing todos tables. On a fresh DB
		// the CREATE TABLE above already includes it; this handles upgrades.
		func(ctx context.Context) error {
			return addColumnIfMissing(ctx, d.db, "todos", "priority", "TEXT NOT NULL DEFAULT ''")
		},
		// Add the position column to pre-existing predefined_priorities tables
		// so priorities carry an explicit user-defined order.
		func(ctx context.Context) error {
			return addColumnIfMissing(ctx, d.db, "predefined_priorities", "position", "INTEGER NOT NULL DEFAULT 0")
		},
		// Seed the three default priorities (low, medium, high). Idempotent.
		func(ctx context.Context) error {
			defaults := []struct {
				name     string
				position int
			}{{"low", 0}, {"medium", 1}, {"high", 2}}
			for _, def := range defaults {
				if _, err := d.db.NewInsert().Model(&predefinedPriority{Name: def.name, Position: def.position}).
					On("CONFLICT (name) DO NOTHING").Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		},
		// Seed a default board on a fresh database (and heal any existing
		// database that somehow has none) so the app is usable immediately.
		// Idempotent: only inserts when the boards table is empty.
		func(ctx context.Context) error {
			n, err := d.db.NewSelect().Model((*board)(nil)).Count(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			_, err = d.db.NewInsert().Model(&board{Name: "Personal", Position: 0}).Exec(ctx)
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
		func(ctx context.Context) error {
			_, err := d.db.NewCreateIndex().IfNotExists().Index("idx_predef_priorities_position").
				Table("predefined_priorities").Column("position", "id").Exec(ctx)
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

// addColumnIfMissing adds a column to a table if it does not already exist.
// Works on both SQLite and Postgres via a uniform information_schema /
// pragma_table_info check.
func addColumnIfMissing(ctx context.Context, db *bun.DB, table, column, decl string) error {
	var count int
	// Postgres: check information_schema.columns. SQLite: check
	// pragma_table_info. We detect the dialect at runtime.
	switch db.Dialect().Name().String() {
	case "pg":
		err := db.NewSelect().
			ColumnExpr("COUNT(*)").
			TableExpr("information_schema.columns").
			Where("table_name = ?", table).
			Where("column_name = ?", column).
			Scan(ctx, &count)
		if err != nil {
			return err
		}
	default:
		err := db.NewSelect().
			ColumnExpr("COUNT(*)").
			TableExpr("pragma_table_info(?)", table).
			Where("name = ?", column).
			Scan(ctx, &count)
		if err != nil {
			return err
		}
	}
	if count > 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
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
	if err := d.attachPriorityColors(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// toFilterItem projects a wire todo into the shape the filter package evaluates.
func toFilterItem(t models.Todo) filter.Item {
	return filter.Item{
		ID:            t.ID,
		ParentID:      t.ParentID,
		Position:      t.Position,
		Title:         t.Title,
		Description:   t.Description,
		Completed:     t.Completed,
		Labels:        t.Labels,
		Priority:      t.Priority,
		DueDate:       t.DueDate,
		HasRecurrence: t.Recurrence != nil,
	}
}

// ListFiltered returns the todos on a board that satisfy q, plus the ancestors
// that keep the tree connected. It reuses [DB.ListAll] for the full board
// (labels attached) and applies the filter in Go. today is the reference ISO
// date (YYYY-MM-DD) used by the date presets. When q carries sort criteria,
// the visible items are reordered within each sibling group (positions
// reassigned) so the rendered tree reflects the requested order.
func (d *DB) ListFiltered(ctx context.Context, boardID int64, q filter.Query, today string) ([]models.Todo, error) {
	all, err := d.ListAll(ctx, boardID)
	if err != nil {
		return nil, err
	}
	rankOf, err := d.priorityRanks(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]filter.Item, len(all))
	byID := make(map[int64]models.Todo, len(all))
	for i, t := range all {
		items[i] = toFilterItem(t)
		if t.Priority != "" {
			if rank, ok := rankOf[t.Priority]; ok {
				items[i].PriorityRank = rank
			}
		}
		byID[t.ID] = t
	}
	visible := filter.Apply(items, q, today)
	visible = filter.Sort(visible, q)
	out := make([]models.Todo, 0, len(visible))
	for _, it := range visible {
		t := byID[it.ID]
		t.Position = it.Position
		out = append(out, t)
	}
	return out, nil
}

// priorityRanks returns a map of priority name → position from
// predefined_priorities, so the filter can sort by user-defined rank.
func (d *DB) priorityRanks(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		Name     string `bun:"name"`
		Position int    `bun:"position"`
	}
	if err := d.db.NewSelect().ColumnExpr("name, position").TableExpr("predefined_priorities").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Name] = r.Position
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
		Color  string `bun:"color"`
	}
	err := d.db.NewSelect().
		ColumnExpr("tl.todo_id, l.name, l.color").
		TableExpr("todo_labels AS tl").
		Join("JOIN labels AS l ON l.id = tl.label_id").
		Where("tl.todo_id IN (?)", bun.In(ids)).
		OrderExpr("l.name").
		Scan(ctx, &rows)
	if err != nil {
		return err
	}
	byID := make(map[int64][]string, len(ts))
	colorByID := make(map[int64][]models.LabelColor, len(ts))
	for _, r := range rows {
		byID[r.TodoID] = append(byID[r.TodoID], r.Name)
		colorByID[r.TodoID] = append(colorByID[r.TodoID], models.LabelColor{Name: r.Name, Color: r.Color})
	}
	for i := range ts {
		ts[i].Labels = byID[ts[i].ID]
		ts[i].LabelColors = colorByID[ts[i].ID]
	}
	return nil
}

// attachPriorityColors stamps the user-defined colour for each todo's priority
// onto the PriorityColor field. Todos with no priority are left empty.
func (d *DB) attachPriorityColors(ctx context.Context, ts []models.Todo) error {
	names := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		if t.Priority != "" {
			names[t.Priority] = struct{}{}
		}
	}
	if len(names) == 0 {
		return nil
	}
	colorOf := make(map[string]string, len(names))
	for name := range names {
		var c string
		if err := d.db.NewSelect().ColumnExpr("color").TableExpr("priorities").
			Where("name = ?", name).Scan(ctx, &c); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		colorOf[name] = c
	}
	for i := range ts {
		if ts[i].Priority != "" {
			ts[i].PriorityColor = colorOf[ts[i].Priority]
		}
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
		Priority:    in.Priority,
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
	if err := d.attachPriorityColors(ctx, wrapped); err != nil {
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
		Priority:    src.Priority,
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

// nextDueDate advances current to the next due date strictly after it per a
// recurrence rule, advancing from today (the completion date) when
// FromCompletion is set. The date-advance engine lives in package schedule so
// the parse path and this completion path share one implementation; this
// wrapper feeds a deterministic now and preserves the ErrInvalidInput mapping
// for HTTP 400.
func nextDueDate(current string, rc models.Recurrence) (next string, recurring bool, err error) {
	next, recurring, err = schedule.NextDue(current, rc, time.Now().UTC())
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return next, recurring, nil
}

// toNullString maps a pointer string to a bind value: nil -> nil (NULL), else
// the pointed-at string. Mirrors toNullInt64.
func toNullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// priorityValue maps an optional priority to its bind value: nil (clear) or
// empty string both bind "" so the NOT NULL column stays valid.
func priorityValue(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
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
	if in.PrioritySet {
		q = q.Set("priority = ?", priorityValue(in.Priority)) // nil clears
	}
	if in.Labels != nil {
		if err := setLabelsTx(ctx, tx, id, *in.Labels); err != nil {
			return models.Todo{}, err
		}
	}
	// An UPDATE with no SET is invalid SQL; only run when a field changed.
	if in.Title != nil || in.Description != nil || in.Completed != nil || in.DueDateSet || in.RecurrenceSet || in.PrioritySet {
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
// rejects), then de-duplicated and sorted. Each label carries its colour (a
// hex string, empty when no user-defined colour is set).
func (d *DB) ListLabels(ctx context.Context) ([]models.Label, error) {
	var adhoc []struct {
		Name  string `bun:"name"`
		Color string `bun:"color"`
	}
	if err := d.db.NewSelect().ColumnExpr("name, color").TableExpr("labels").Scan(ctx, &adhoc); err != nil {
		return nil, err
	}
	var predefined []string
	if err := d.db.NewSelect().ColumnExpr("name").TableExpr("predefined_labels").Scan(ctx, &predefined); err != nil {
		return nil, err
	}
	colorOf := make(map[string]string, len(adhoc))
	out := make([]models.Label, 0, len(adhoc)+len(predefined))
	seenLow := make(map[string]struct{}, len(adhoc)+len(predefined))
	for _, l := range adhoc {
		low := strings.ToLower(l.Name)
		if _, dup := seenLow[low]; dup {
			continue
		}
		seenLow[low] = struct{}{}
		colorOf[low] = l.Color
		out = append(out, models.Label{Name: l.Name, Color: l.Color})
	}
	for _, n := range predefined {
		low := strings.ToLower(n)
		if _, dup := seenLow[low]; dup {
			continue
		}
		seenLow[low] = struct{}{}
		out = append(out, models.Label{Name: n, Color: colorOf[low]})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// SetLabelColor sets the user-defined colour for a label. An empty colour
// clears it so the label reverts to the auto-assigned palette colour.
// Matching is case-insensitive: if the label exists under different casing,
// that row is updated.
func (d *DB) SetLabelColor(ctx context.Context, name string, color string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	var existing string
	err := d.db.NewSelect().ColumnExpr("name").TableExpr("labels").
		Where("LOWER(name) = ?", strings.ToLower(name)).Limit(1).Scan(ctx, &existing)
	if err == nil {
		_, err = d.db.NewUpdate().Model((*label)(nil)).
			Set("color = ?", color).
			Where("name = ?", existing).
			Exec(ctx)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = d.db.NewInsert().Model(&label{Name: name, Color: color}).
		On("CONFLICT (name) DO UPDATE SET color = excluded.color").
		Exec(ctx)
	return err
}

// AddPredefinedLabel records a label as predefined so it always appears in the
// global label list even when no todo uses it. Idempotent on name
// (case-insensitive).
func (d *DB) AddPredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	low := strings.ToLower(name)
	var existing string
	err := d.db.NewSelect().ColumnExpr("name").TableExpr("predefined_labels").
		Where("LOWER(name) = ?", low).Limit(1).Scan(ctx, &existing)
	if err == nil {
		return nil // already exists (case-insensitive match)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = d.db.NewInsert().Model(&predefinedLabel{Name: name}).Ignore().Exec(ctx)
	return err
}

// RemovePredefinedLabel removes a label from the predefined set. Ad-hoc usages
// on todos are unaffected. Matching is case-insensitive.
func (d *DB) RemovePredefinedLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.db.NewDelete().Model((*predefinedLabel)(nil)).
		Where("LOWER(name) = ?", strings.ToLower(name)).Exec(ctx)
	return err
}

// DeleteLabel removes a label everywhere: its todo attachments, its colour row,
// and its predefined entry. Matching is case-insensitive. Returns ErrNotFound
// when no label with that name exists in any of the three tables.
func (d *DB) DeleteLabel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// The todo_links delete is explicit (not left to ON DELETE CASCADE) so
	// pre-existing databases whose todo_labels table lacks the foreign key
	// are cleaned up too.
	low := strings.ToLower(name)
	affected := int64(0)
	for _, del := range []struct {
		model any
		where string
	}{
		{(*todoLabel)(nil), "label_id IN (SELECT id FROM labels WHERE LOWER(name) = ?)"},
		{(*label)(nil), "LOWER(name) = ?"},
		{(*predefinedLabel)(nil), "LOWER(name) = ?"},
	} {
		res, err := tx.NewDelete().Model(del.model).Where(del.where, low).Exec(ctx)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		affected += n
	}
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ListPriorities returns all known priorities, combining predefined priorities
// with any priorities currently in use across todos. De-duplicated. Each
// priority carries its colour (a hex string, empty when unset) and its position
// from the predefined set (nil when the priority is only ad-hoc). Ordered by
// position (predefined first, ad-hoc last, alphabetical within each group).
func (d *DB) ListPriorities(ctx context.Context) ([]models.Priority, error) {
	var adhoc []string
	if err := d.db.NewSelect().ColumnExpr("DISTINCT priority").TableExpr("todos").
		Where("priority <> ''").Scan(ctx, &adhoc); err != nil {
		return nil, err
	}
	var predefined []struct {
		Name     string `bun:"name"`
		Position int    `bun:"position"`
	}
	if err := d.db.NewSelect().ColumnExpr("name, position").TableExpr("predefined_priorities").
		OrderExpr("position, id").Scan(ctx, &predefined); err != nil {
		return nil, err
	}
	var colored []struct {
		Name  string `bun:"name"`
		Color string `bun:"color"`
	}
	if err := d.db.NewSelect().ColumnExpr("name, color").TableExpr("priorities").Scan(ctx, &colored); err != nil {
		return nil, err
	}
	colorOf := make(map[string]string, len(colored))
	for _, p := range colored {
		colorOf[p.Name] = p.Color
	}
	seen := make(map[string]struct{}, len(adhoc)+len(predefined))
	out := make([]models.Priority, 0, len(adhoc)+len(predefined))
	for _, p := range predefined {
		if _, dup := seen[p.Name]; dup {
			continue
		}
		seen[p.Name] = struct{}{}
		pos := p.Position
		out = append(out, models.Priority{Name: p.Name, Color: colorOf[p.Name], Position: &pos})
	}
	for _, n := range adhoc {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, models.Priority{Name: n, Color: colorOf[n]})
	}
	return out, nil
}

// SetPriorityColor sets the user-defined colour for a priority. An empty colour
// clears it so the priority reverts to the auto-assigned palette colour.
func (d *DB) SetPriorityColor(ctx context.Context, name string, color string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.db.NewInsert().Model(&priority{Name: name, Color: color}).
		On("CONFLICT (name) DO UPDATE SET color = excluded.color").
		Exec(ctx)
	return err
}

// AddPredefinedPriority records a priority as predefined so it always appears in
// the global priority list even when no todo uses it. Idempotent on name. New
// priorities are appended at the end of the ordering (position = max + 1).
func (d *DB) AddPredefinedPriority(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	var maxPos int
	if err := d.db.NewSelect().ColumnExpr("COALESCE(MAX(position), -1)").
		TableExpr("predefined_priorities").Scan(ctx, &maxPos); err != nil {
		return err
	}
	_, err := d.db.NewInsert().Model(&predefinedPriority{Name: name, Position: maxPos + 1}).
		On("CONFLICT (name) DO NOTHING").Exec(ctx)
	return err
}

// RemovePredefinedPriority removes a priority from the predefined set. Todos
// already using it are unaffected (they keep the priority until cleared).
func (d *DB) RemovePredefinedPriority(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidInput
	}
	_, err := d.db.NewDelete().Model((*predefinedPriority)(nil)).Where("name = ?", name).Exec(ctx)
	return err
}

// ReorderPriorities sets the position of each predefined priority to match the
// order of the supplied names slice. Names not already in the predefined set
// are ignored. Priorities missing from the slice retain their old positions.
func (d *DB) ReorderPriorities(ctx context.Context, names []string) error {
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := d.db.NewUpdate().Model((*predefinedPriority)(nil)).
			Set("position = ?", i).
			Where("name = ?", name).
			Exec(ctx); err != nil {
			return err
		}
	}
	return nil
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
// label names as needed. Label matching is case-insensitive: if a label
// differs only in casing from an existing one, the existing one is reused.
func setLabelsTx(ctx context.Context, run bun.IDB, todoID int64, labels []string) error {
	if _, err := run.NewDelete().Model((*todoLabel)(nil)).Where("todo_id = ?", todoID).Exec(ctx); err != nil {
		return err
	}
	seenLow := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		if _, dup := seenLow[low]; dup {
			continue
		}
		seenLow[low] = struct{}{}
		var lid int64
		err := run.NewSelect().ColumnExpr("id").TableExpr("labels").
			Where("LOWER(name) = ?", low).Limit(1).Scan(ctx, &lid)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := run.NewInsert().Model(&label{Name: l}).
				Returning("id").Scan(ctx, &lid); err != nil {
				return err
			}
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

// ListSavedSearches returns every saved search in creation order.
func (d *DB) ListSavedSearches(ctx context.Context) ([]models.SavedSearch, error) {
	var ss []savedSearch
	if err := d.db.NewSelect().Model(&ss).OrderExpr("id").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]models.SavedSearch, len(ss))
	for i, s := range ss {
		out[i] = models.SavedSearch{ID: s.ID, Name: s.Name, Query: s.Query}
	}
	return out, nil
}

// CreateSavedSearch stores a named filter query.
func (d *DB) CreateSavedSearch(ctx context.Context, in models.CreateSavedSearch) (models.SavedSearch, error) {
	s := savedSearch{Name: in.Name, Query: in.Query}
	if err := d.db.NewInsert().Model(&s).Returning("id").Scan(ctx, &s.ID); err != nil {
		return models.SavedSearch{}, err
	}
	return models.SavedSearch{ID: s.ID, Name: s.Name, Query: s.Query}, nil
}

// DeleteSavedSearch removes a saved search by id.
func (d *DB) DeleteSavedSearch(ctx context.Context, id int64) error {
	res, err := d.db.NewDelete().Model((*savedSearch)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSavedSearchNotFound
	}
	return nil
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

	ErrSavedSearchNotFound = errors.New("saved search not found")
)

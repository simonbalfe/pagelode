package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type State string

const (
	Pending      State = "pending"
	NeedsBrowser State = "needs_browser"
	Done         State = "done"
	Dead         State = "dead"
	Failed       State = "failed"
)

type Entry struct {
	URL  string
	Host string
}

type Item struct {
	ID       int64
	URL      string
	Host     string
	Attempts int
}

type Completion struct {
	ID           int64
	State        State
	Outcome      string
	Provider     string
	StatusCode   int
	Detail       string
	ContentRef   string
	CountAttempt bool
}

type Count struct {
	State   State
	Ready   int64
	Waiting int64
}

const schemaLockKey int64 = 0x7061_6765_6c6f_6465

type Queue struct {
	pool  *pgxpool.Pool
	table string
}

func Open(ctx context.Context, dsn string) (*Queue, error) {
	return open(ctx, dsn, "pagelode")
}

func open(ctx context.Context, dsn, schema string) (*Queue, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open queue: %w", err)
	}
	q := &Queue{pool: pool, table: pgx.Identifier{schema, "urls"}.Sanitize()}
	if err := q.migrate(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open queue: %w", err)
	}
	return q, nil
}

func (q *Queue) migrate(ctx context.Context, schema string) error {
	stmts := []string{
		`SELECT pg_advisory_xact_lock(` + fmt.Sprint(schemaLockKey) + `)`,
		`CREATE SCHEMA IF NOT EXISTS ` + pgx.Identifier{schema}.Sanitize(),
		`CREATE TABLE IF NOT EXISTS ` + q.table + ` (
			id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			url         text NOT NULL UNIQUE,
			host        text NOT NULL,
			state       text NOT NULL,
			attempts    integer NOT NULL DEFAULT 0,
			next_at     timestamptz NOT NULL DEFAULT now(),
			lease_owner text,
			outcome     text,
			provider    text,
			status_code integer,
			detail      text,
			content_ref text,
			created_at  timestamptz NOT NULL DEFAULT now(),
			updated_at  timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS urls_state_next_at_idx ON ` + q.table + ` (state, next_at)`,
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (q *Queue) Close() {
	q.pool.Close()
}

func (q *Queue) Import(ctx context.Context, entries []Entry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	for i, e := range entries {
		if e.URL == "" || e.Host == "" {
			return 0, fmt.Errorf("import urls: entry %d: empty url or host", i)
		}
	}

	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("import urls: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE pagelode_import (ord bigint NOT NULL, url text NOT NULL, host text NOT NULL) ON COMMIT DROP`); err != nil {
		return 0, fmt.Errorf("import urls: %w", err)
	}
	rows := pgx.CopyFromSlice(len(entries), func(i int) ([]any, error) {
		return []any{int64(i), entries[i].URL, entries[i].Host}, nil
	})
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"pagelode_import"}, []string{"ord", "url", "host"}, rows); err != nil {
		return 0, fmt.Errorf("import urls: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO `+q.table+` (url, host, state)
		SELECT url, host, $1 FROM pagelode_import ORDER BY ord
		ON CONFLICT (url) DO NOTHING`, string(Pending))
	if err != nil {
		return 0, fmt.Errorf("import urls: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("import urls: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (q *Queue) Claim(ctx context.Context, state State, limit int, lease time.Duration, owner string) ([]Item, error) {
	if state != Pending && state != NeedsBrowser {
		return nil, fmt.Errorf("claim urls: state %q is not claimable", state)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("claim urls: limit %d must be positive", limit)
	}
	if lease <= 0 {
		return nil, fmt.Errorf("claim urls: lease %s must be positive", lease)
	}

	rows, err := q.pool.Query(ctx, `
		WITH picked AS MATERIALIZED (
			SELECT id, next_at FROM `+q.table+`
			WHERE state = $1 AND next_at <= now()
			ORDER BY next_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE `+q.table+` u
			SET next_at = now() + $3::bigint * interval '1 microsecond',
				lease_owner = NULLIF($4, ''),
				updated_at = now()
			FROM picked
			WHERE u.id = picked.id
			RETURNING u.id, u.url, u.host, u.attempts, picked.next_at AS due
		)
		SELECT id, url, host, attempts FROM claimed ORDER BY due, id`,
		string(state), limit, ceilMicroseconds(lease), owner)
	if err != nil {
		return nil, fmt.Errorf("claim urls: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var it Item
		err := row.Scan(&it.ID, &it.URL, &it.Host, &it.Attempts)
		return it, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim urls: %w", err)
	}
	return items, nil
}

func (q *Queue) Complete(ctx context.Context, completions []Completion) error {
	if len(completions) == 0 {
		return nil
	}
	n := len(completions)
	var (
		ids      = make([]int64, n)
		states   = make([]string, n)
		outcomes = make([]string, n)
		provs    = make([]string, n)
		codes    = make([]int32, n)
		details  = make([]string, n)
		refs     = make([]string, n)
		counts   = make([]bool, n)
	)
	for i, c := range completions {
		switch c.State {
		case Done, Dead, Failed, NeedsBrowser:
		default:
			return fmt.Errorf("complete urls: url %d: state %q is not a completion state", c.ID, c.State)
		}
		ids[i], states[i], outcomes[i], provs[i] = c.ID, string(c.State), c.Outcome, c.Provider
		codes[i], details[i], refs[i], counts[i] = int32(c.StatusCode), c.Detail, c.ContentRef, c.CountAttempt
	}

	_, err := q.pool.Exec(ctx, `
		UPDATE `+q.table+` u
		SET state = c.state,
			outcome = NULLIF(c.outcome, ''),
			provider = NULLIF(c.provider, ''),
			status_code = NULLIF(c.status_code, 0),
			detail = NULLIF(c.detail, ''),
			content_ref = NULLIF(c.content_ref, ''),
			attempts = u.attempts + CASE WHEN c.count_attempt THEN 1 ELSE 0 END,
			next_at = now(),
			lease_owner = NULL,
			updated_at = now()
		FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[], $5::integer[], $6::text[], $7::text[], $8::boolean[])
			AS c(id, state, outcome, provider, status_code, detail, content_ref, count_attempt)
		WHERE u.id = c.id`,
		ids, states, outcomes, provs, codes, details, refs, counts)
	if err != nil {
		return fmt.Errorf("complete urls: %w", err)
	}
	return nil
}

func (q *Queue) Retry(ctx context.Context, id int64, after time.Duration, countAttempt bool, detail string) error {
	if after < 0 {
		return fmt.Errorf("retry url %d: delay %s must not be negative", id, after)
	}
	tag, err := q.pool.Exec(ctx, `
		UPDATE `+q.table+`
		SET next_at = now() + $2::bigint * interval '1 microsecond',
			attempts = attempts + CASE WHEN $3 THEN 1 ELSE 0 END,
			detail = NULLIF($4, ''),
			lease_owner = NULL,
			updated_at = now()
		WHERE id = $1`,
		id, ceilMicroseconds(after), countAttempt, detail)
	if err != nil {
		return fmt.Errorf("retry url %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("retry url %d: no such url", id)
	}
	return nil
}

func (q *Queue) Reset(ctx context.Context, from, to State) (int64, error) {
	if !from.valid() || !to.valid() {
		return 0, fmt.Errorf("reset urls: unknown state in %q -> %q", from, to)
	}
	tag, err := q.pool.Exec(ctx, `
		UPDATE `+q.table+`
		SET state = $2, attempts = 0, next_at = now(), lease_owner = NULL,
			outcome = NULL, provider = NULL, status_code = NULL, detail = NULL, content_ref = NULL,
			updated_at = now()
		WHERE state = $1`,
		string(from), string(to))
	if err != nil {
		return 0, fmt.Errorf("reset urls: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (q *Queue) Remaining(ctx context.Context, state State) (bool, error) {
	if !state.valid() {
		return false, fmt.Errorf("remaining urls: unknown state %q", state)
	}
	var ok bool
	err := q.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+q.table+` WHERE state = $1)`, string(state)).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("remaining urls: %w", err)
	}
	return ok, nil
}

func (q *Queue) Counts(ctx context.Context) ([]Count, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT state,
			count(*) FILTER (WHERE next_at <= now()),
			count(*) FILTER (WHERE next_at > now())
		FROM `+q.table+`
		GROUP BY state
		ORDER BY state`)
	if err != nil {
		return nil, fmt.Errorf("count urls: %w", err)
	}
	counts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Count, error) {
		var (
			c     Count
			state string
		)
		err := row.Scan(&state, &c.Ready, &c.Waiting)
		c.State = State(state)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("count urls: %w", err)
	}
	return counts, nil
}

func (s State) valid() bool {
	switch s {
	case Pending, NeedsBrowser, Done, Dead, Failed:
		return true
	}
	return false
}

func ceilMicroseconds(d time.Duration) int64 {
	us := d / time.Microsecond
	if d%time.Microsecond != 0 {
		us++
	}
	return int64(us)
}

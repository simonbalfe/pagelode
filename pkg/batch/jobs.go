package batch

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

const (
	Schema       = "pagelode"
	HTTPKind     = "extract_http"
	BrowserKind  = "extract_browser"
	httpQueue    = "http"
	browserQueue = "browser"

	poolConnections = 32
)

type HTTPArgs struct {
	URL  string `json:"url" river:"unique"`
	Host string `json:"host"`
}

func (HTTPArgs) Kind() string { return HTTPKind }

func (HTTPArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: httpQueue, MaxAttempts: 1, UniqueOpts: uniqueByURL()}
}

type BrowserArgs struct {
	URL  string `json:"url" river:"unique"`
	Host string `json:"host"`
}

func (BrowserArgs) Kind() string { return BrowserKind }

func (BrowserArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: browserQueue, MaxAttempts: 1, UniqueOpts: uniqueByURL()}
}

func uniqueByURL() river.UniqueOpts {
	return river.UniqueOpts{ByArgs: true, ByState: rivertype.JobStates()}
}

func Kinds() []string {
	return []string{HTTPKind, BrowserKind}
}

func Stage(kind string) string {
	switch kind {
	case HTTPKind:
		return httpQueue
	case BrowserKind:
		return browserQueue
	}
	return kind
}

type State string

const (
	Pending  State = "pending"
	Running  State = "running"
	Retrying State = "retrying"
	Done     State = "done"
	Dead     State = "dead"
	Failed   State = "failed"
)

func States() []State {
	return []State{Pending, Running, Retrying, Done, Dead, Failed}
}

func StateOf(state rivertype.JobState) State {
	switch state {
	case rivertype.JobStateRunning:
		return Running
	case rivertype.JobStateRetryable:
		return Retrying
	case rivertype.JobStateCompleted:
		return Done
	case rivertype.JobStateCancelled:
		return Dead
	case rivertype.JobStateDiscarded:
		return Failed
	}
	return Pending
}

func (s State) RetryFrom() (rivertype.JobState, error) {
	switch s {
	case Failed:
		return rivertype.JobStateDiscarded, nil
	case Dead:
		return rivertype.JobStateCancelled, nil
	}
	return "", fmt.Errorf("batch: only %s and %s URLs can be retried, not %q", Failed, Dead, s)
}

type Output struct {
	Outcome    string `json:"outcome"`
	Provider   string `json:"provider,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	ContentRef string `json:"contentRef,omitempty"`
}

type Queue struct {
	pool   *pgxpool.Pool
	driver *riverpgxv5.Driver
	client *river.Client[pgx.Tx]
	schema string
}

func Open(ctx context.Context, dsn string) (*Queue, error) {
	return open(ctx, dsn, Schema)
}

func open(ctx context.Context, dsn, schema string) (*Queue, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("batch: open database: %w", err)
	}
	config.MaxConns = poolConnections
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("batch: open database: %w", err)
	}
	q, err := setUp(ctx, pool, schema)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return q, nil
}

func setUp(ctx context.Context, pool *pgxpool.Pool, schema string) (*Queue, error) {
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return nil, fmt.Errorf("batch: create schema: %w", err)
	}
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: schema, Logger: discardLogger()})
	if err != nil {
		return nil, fmt.Errorf("batch: create River tables: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("batch: create River tables: %w", err)
	}
	client, err := river.NewClient(driver, &river.Config{Schema: schema, Logger: discardLogger()})
	if err != nil {
		return nil, fmt.Errorf("batch: create River client: %w", err)
	}
	return &Queue{pool: pool, driver: driver, client: client, schema: schema}, nil
}

func (q *Queue) Close() {
	q.pool.Close()
}

func (q *Queue) Retry(ctx context.Context, ids []int64) (int64, error) {
	var retried int64
	for _, id := range ids {
		job, err := q.client.JobRetry(ctx, id)
		if err != nil {
			return retried, fmt.Errorf("batch: retry job %d: %w", id, err)
		}
		if job.State != rivertype.JobStateRunning {
			retried++
		}
	}
	return retried, nil
}

func (q *Queue) busy(ctx context.Context, kinds ...string) (bool, error) {
	params := river.NewJobListParams().
		Kinds(kinds...).
		States(rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable, rivertype.JobStateRunning, rivertype.JobStateScheduled).
		First(1)
	result, err := q.client.JobList(ctx, params)
	if err != nil {
		return false, fmt.Errorf("batch: list unfinished jobs: %w", err)
	}
	return len(result.Jobs) > 0, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

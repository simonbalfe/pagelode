package batch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/simonbalfe/pagelode/pkg/fetch"
)

type Extractor interface {
	ExtractHTTP(ctx context.Context, url string) (fetch.Result, error)
	ExtractBrowser(ctx context.Context, url string) (fetch.Result, error)
}

type Options struct {
	Owner            string
	HTTPWorkers      int
	BrowserWorkers   int
	Lease            time.Duration
	PageTimeout      time.Duration
	HostConcurrency  int
	Idle             time.Duration
	UntilEmpty       bool
	Progress         io.Writer
	ProgressInterval time.Duration
	Counts           func(context.Context) (Counts, error)
	Color            bool

	hostBusyDelay     time.Duration
	capacityDelay     time.Duration
	fetchPollInterval time.Duration
}

type Stats struct {
	Done         atomic.Int64
	Dead         atomic.Int64
	Failed       atomic.Int64
	NeedsBrowser atomic.Int64
	Deferred     atomic.Int64
}

const (
	writeAllowance = 2 * flushInterval
	handOffTimeout = 30 * time.Second
)

func (q *Queue) Run(ctx context.Context, extractor Extractor, sink *Writer, options Options) (*Stats, error) {
	options, err := options.withDefaults()
	if err != nil {
		return nil, errors.Join(err, sink.Close())
	}
	runCtx, fail := context.WithCancelCause(ctx)
	defer fail(nil)
	r := &runner{q: q, extractor: extractor, sink: sink, options: options, stats: &Stats{}, hosts: newHostGate(options.HostConcurrency), fail: fail}

	workers := river.NewWorkers()
	river.AddWorker(workers, &httpWorker{r: r})
	river.AddWorker(workers, &browserWorker{r: r})
	queues := map[string]river.QueueConfig{httpQueue: {MaxWorkers: options.HTTPWorkers}}
	watched := []string{HTTPKind}
	if options.BrowserWorkers > 0 {
		queues[browserQueue] = river.QueueConfig{MaxWorkers: options.BrowserWorkers}
		watched = append(watched, BrowserKind)
	}
	client, err := river.NewClient(q.driver, &river.Config{
		Schema:                      q.schema,
		Queues:                      queues,
		Workers:                     workers,
		JobTimeout:                  options.PageTimeout + writeAllowance,
		RescueStuckJobsAfter:        options.Lease,
		CompletedJobRetentionPeriod: -1,
		CancelledJobRetentionPeriod: -1,
		DiscardedJobRetentionPeriod: -1,
		FetchPollInterval:           options.fetchPollInterval,
		Logger:                      r.logger(),
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("batch: create River client: %w", err), sink.Close())
	}
	if err := client.Start(context.WithoutCancel(runCtx)); err != nil {
		return nil, errors.Join(fmt.Errorf("batch: start River client: %w", err), sink.Close())
	}
	stopProgress := r.reportProgress(runCtx)
	r.wait(runCtx, client, watched)
	stopProgress()

	closeErr := sink.Close()
	if cause := context.Cause(runCtx); cause != nil && cause != context.Cause(ctx) && !errors.Is(cause, errEmpty) {
		return r.stats, errors.Join(cause, closeErr)
	}
	return r.stats, closeErr
}

var errEmpty = errors.New("batch: no URLs left")

func (r *runner) wait(ctx context.Context, client *river.Client[pgx.Tx], watched []string) {
	ticker := time.NewTicker(r.options.Idle)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			client.StopAndCancel(context.Background())
			return
		case <-ticker.C:
			if !r.options.UntilEmpty {
				continue
			}
			busy, err := r.q.busy(ctx, watched...)
			if err != nil {
				if ctx.Err() == nil {
					r.fail(err)
				}
				continue
			}
			if !busy {
				r.fail(errEmpty)
				client.Stop(context.Background())
				return
			}
		}
	}
}

func (o Options) withDefaults() (Options, error) {
	if o.Owner == "" {
		return o, errors.New("batch: owner is required")
	}
	if o.HTTPWorkers == 0 {
		o.HTTPWorkers = 100
	}
	if o.Lease == 0 {
		o.Lease = 10 * time.Minute
	}
	if o.PageTimeout == 0 {
		o.PageTimeout = 10 * time.Second
	}
	if o.HostConcurrency == 0 {
		o.HostConcurrency = 4
	}
	if o.Idle == 0 {
		o.Idle = 2 * time.Second
	}
	if o.ProgressInterval == 0 {
		o.ProgressInterval = 10 * time.Second
	}
	if o.hostBusyDelay == 0 {
		o.hostBusyDelay = time.Second
	}
	if o.capacityDelay == 0 {
		o.capacityDelay = 5 * time.Second
	}
	if o.fetchPollInterval == 0 {
		o.fetchPollInterval = river.FetchPollIntervalDefault
	}
	if o.HTTPWorkers < 1 || o.BrowserWorkers < 0 || o.HostConcurrency < 1 {
		return o, errors.New("batch: workers and host concurrency must be positive")
	}
	if timeout := o.PageTimeout + writeAllowance; o.Lease <= timeout {
		return o, fmt.Errorf("batch: lease %s must exceed page timeout %s plus %s for writing the page", o.Lease, o.PageTimeout, writeAllowance)
	}
	return o, nil
}

type runner struct {
	q         *Queue
	extractor Extractor
	sink      *Writer
	options   Options
	stats     *Stats
	hosts     *hostGate
	fail      context.CancelCauseFunc
}

type httpWorker struct {
	river.WorkerDefaults[HTTPArgs]
	r *runner
}

func (w *httpWorker) Work(ctx context.Context, job *river.Job[HTTPArgs]) error {
	r := w.r
	if !r.hosts.acquire(job.Args.Host) {
		return r.snooze(jittered(r.options.hostBusyDelay))
	}
	defer r.hosts.release(job.Args.Host)

	pageCtx, cancel := context.WithTimeout(ctx, r.options.PageTimeout)
	result, err := r.extractor.ExtractHTTP(pageCtx, job.Args.URL)
	cancel()
	if ctx.Err() != nil {
		return r.snooze(0)
	}
	if err != nil {
		r.stats.Failed.Add(1)
		return err
	}
	switch result.Outcome {
	case "ok":
		return r.store(ctx, job.ID, result)
	case "dead":
		return r.dead(ctx, result)
	case fetch.OutcomeNeedsBrowser:
		return r.handOff(ctx, job, result)
	}
	r.stats.Failed.Add(1)
	return r.failure(ctx, result)
}

type browserWorker struct {
	river.WorkerDefaults[BrowserArgs]
	r *runner
}

func (w *browserWorker) Work(ctx context.Context, job *river.Job[BrowserArgs]) error {
	r := w.r
	if !r.hosts.acquire(job.Args.Host) {
		return r.snooze(jittered(r.options.hostBusyDelay))
	}
	defer r.hosts.release(job.Args.Host)

	pageCtx, cancel := context.WithTimeout(ctx, r.options.PageTimeout)
	result, err := r.extractor.ExtractBrowser(pageCtx, job.Args.URL)
	cancel()
	if ctx.Err() != nil {
		return r.snooze(0)
	}
	if errors.Is(err, fetch.ErrSaturated) || errors.Is(err, fetch.ErrUnavailable) {
		return r.snooze(jittered(r.options.capacityDelay))
	}
	if err != nil {
		r.stats.Failed.Add(1)
		return err
	}
	switch result.Outcome {
	case "ok":
		return r.store(ctx, job.ID, result)
	case "dead":
		return r.dead(ctx, result)
	}
	r.stats.Failed.Add(1)
	return r.failure(ctx, result)
}

func (r *runner) snooze(delay time.Duration) error {
	r.stats.Deferred.Add(1)
	return river.JobSnooze(delay)
}

func (r *runner) store(ctx context.Context, id int64, result fetch.Result) error {
	ref, err := r.sink.Add(ctx, id, result)
	if err != nil {
		if ctx.Err() == nil {
			r.fail(err)
		}
		return r.snooze(0)
	}
	output := outputOf(result)
	output.ContentRef = ref
	if err := river.RecordOutput(ctx, output); err != nil {
		return err
	}
	r.stats.Done.Add(1)
	return nil
}

func (r *runner) dead(ctx context.Context, result fetch.Result) error {
	r.stats.Dead.Add(1)
	if err := river.RecordOutput(ctx, outputOf(result)); err != nil {
		return err
	}
	return river.JobCancel(fmt.Errorf("dead: %s", describe(result)))
}

func (r *runner) failure(ctx context.Context, result fetch.Result) error {
	if err := river.RecordOutput(ctx, outputOf(result)); err != nil {
		return err
	}
	return fmt.Errorf("%s: %s", result.Outcome, describe(result))
}

func (r *runner) handOff(ctx context.Context, job *river.Job[HTTPArgs], result fetch.Result) error {
	if err := river.RecordOutput(ctx, outputOf(result)); err != nil {
		return err
	}
	txCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handOffTimeout)
	defer cancel()
	tx, err := r.q.pool.Begin(txCtx)
	if err != nil {
		return fmt.Errorf("batch: hand %s to the browser stage: %w", job.Args.URL, err)
	}
	defer tx.Rollback(txCtx)
	client := river.ClientFromContext[pgx.Tx](ctx)
	if _, err := client.InsertTx(txCtx, tx, BrowserArgs(job.Args), nil); err != nil {
		return fmt.Errorf("batch: hand %s to the browser stage: %w", job.Args.URL, err)
	}
	if _, err := river.JobCompleteTx[*riverpgxv5.Driver](txCtx, tx, job); err != nil {
		return fmt.Errorf("batch: hand %s to the browser stage: %w", job.Args.URL, err)
	}
	if err := tx.Commit(txCtx); err != nil {
		return fmt.Errorf("batch: hand %s to the browser stage: %w", job.Args.URL, err)
	}
	r.stats.NeedsBrowser.Add(1)
	return nil
}

func (r *runner) logger() *slog.Logger {
	if r.options.Progress == nil {
		return discardLogger()
	}
	return slog.New(slog.NewTextHandler(r.options.Progress, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func outputOf(result fetch.Result) Output {
	output := Output{Outcome: result.Outcome, Provider: string(result.Provider)}
	if count := len(result.Attempts); count > 0 {
		output.StatusCode = result.Attempts[count-1].Status
		output.Detail = result.Attempts[count-1].Detail
	}
	return output
}

func describe(result fetch.Result) string {
	output := outputOf(result)
	if output.Detail != "" {
		return output.Detail
	}
	if output.StatusCode != 0 {
		return fmt.Sprintf("status %d", output.StatusCode)
	}
	return result.Outcome
}

func jittered(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return delay/2 + rand.N(delay)
}

type hostGate struct {
	mu       sync.Mutex
	limit    int
	inFlight map[string]int
}

func newHostGate(limit int) *hostGate {
	return &hostGate{limit: limit, inFlight: make(map[string]int)}
}

func (g *hostGate) acquire(host string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight[host] >= g.limit {
		return false
	}
	g.inFlight[host]++
	return true
}

func (g *hostGate) release(host string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight[host] <= 1 {
		delete(g.inFlight, host)
		return
	}
	g.inFlight[host]--
}

package batch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simonbalfe/pagelode/limit"
	"github.com/simonbalfe/pagelode/orchestrator"
	"github.com/simonbalfe/pagelode/queue"
)

type Queue interface {
	Claim(ctx context.Context, state queue.State, limit int, lease time.Duration, owner string) ([]queue.Item, error)
	Complete(ctx context.Context, completions []queue.Completion) error
	Retry(ctx context.Context, id int64, after time.Duration, countAttempt bool, detail string) error
	Remaining(ctx context.Context, state queue.State) (bool, error)
}

type Extractor interface {
	ExtractHTTP(ctx context.Context, url string) (orchestrator.Result, error)
	ExtractBrowser(ctx context.Context, url string) (orchestrator.Result, error)
}

type Options struct {
	Owner            string
	HTTPWorkers      int
	BrowserWorkers   int
	Lease            time.Duration
	PageTimeout      time.Duration
	MaxAttempts      int
	HostConcurrency  int
	Idle             time.Duration
	UntilEmpty       bool
	Progress         io.Writer
	ProgressInterval time.Duration
}

type Stats struct {
	Done         atomic.Int64
	Dead         atomic.Int64
	Failed       atomic.Int64
	NeedsBrowser atomic.Int64
	Deferred     atomic.Int64
	Retried      atomic.Int64
}

const (
	claimBatch       = 100
	hostBusyDelay    = time.Second
	capacityDelay    = 5 * time.Second
	failureBaseDelay = time.Minute
	completeTimeout  = 30 * time.Second
)

func Run(ctx context.Context, q Queue, extractor Extractor, sink *Writer, options Options) (*Stats, error) {
	options, err := options.withDefaults()
	if err != nil {
		return nil, errors.Join(err, sink.Close(context.Background()))
	}
	r := &runner{q: q, extractor: extractor, sink: sink, options: options, stats: &Stats{}, hosts: newHostGate(options.HostConcurrency)}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var stages sync.WaitGroup
	stages.Go(func() {
		if err := r.stage(runCtx, queue.Pending, options.HTTPWorkers, r.processHTTP, queue.Pending); err != nil {
			cancel(err)
		}
	})
	if options.BrowserWorkers > 0 {
		stages.Go(func() {
			if err := r.stage(runCtx, queue.NeedsBrowser, options.BrowserWorkers, r.processBrowser, queue.Pending, queue.NeedsBrowser); err != nil {
				cancel(err)
			}
		})
	}
	stopProgress := r.reportProgress(runCtx)
	stages.Wait()
	stopProgress()

	closeErr := sink.Close(context.Background())
	if cause := context.Cause(runCtx); cause != nil && cause != context.Cause(ctx) {
		return r.stats, errors.Join(cause, closeErr)
	}
	return r.stats, closeErr
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
		o.PageTimeout = 90 * time.Second
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
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
	if o.HTTPWorkers < 1 || o.BrowserWorkers < 0 || o.MaxAttempts < 1 || o.HostConcurrency < 1 {
		return o, errors.New("batch: workers, max attempts, and host concurrency must be positive")
	}
	if o.Lease <= o.PageTimeout+flushInterval {
		return o, fmt.Errorf("batch: lease %s must exceed page timeout %s plus the %s flush interval", o.Lease, o.PageTimeout, flushInterval)
	}
	return o, nil
}

type runner struct {
	q         Queue
	extractor Extractor
	sink      *Writer
	options   Options
	stats     *Stats
	hosts     *hostGate
}

func (r *runner) stage(ctx context.Context, state queue.State, workers int, process func(context.Context, queue.Item) error, watched ...queue.State) error {
	jobs := make(chan queue.Item)
	errs := make(chan error, workers)
	var pool sync.WaitGroup
	for range workers {
		pool.Go(func() {
			for item := range jobs {
				if err := process(ctx, item); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	err := r.feed(ctx, state, min(claimBatch, workers), jobs, errs, watched)
	close(jobs)
	pool.Wait()
	close(errs)
	for workerErr := range errs {
		err = errors.Join(err, workerErr)
	}
	return err
}

func (r *runner) feed(ctx context.Context, state queue.State, batchSize int, jobs chan<- queue.Item, errs <-chan error, watched []queue.State) error {
	for {
		items, err := r.q.Claim(ctx, state, batchSize, r.options.Lease, r.options.Owner)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("batch: claim %s: %w", state, err)
		}
		for index, item := range items {
			select {
			case jobs <- item:
			case err := <-errs:
				r.release(items[index:])
				return err
			case <-ctx.Done():
				r.release(items[index:])
				return nil
			}
		}
		if len(items) > 0 {
			continue
		}
		if err := r.sink.Flush(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if r.options.UntilEmpty {
			empty, err := r.empty(ctx, watched)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if empty {
				return nil
			}
		}
		select {
		case err := <-errs:
			return err
		case <-ctx.Done():
			return nil
		case <-time.After(r.options.Idle):
		}
	}
}

func (r *runner) empty(ctx context.Context, states []queue.State) (bool, error) {
	for _, state := range states {
		remaining, err := r.q.Remaining(ctx, state)
		if err != nil {
			return false, fmt.Errorf("batch: check remaining %s: %w", state, err)
		}
		if remaining {
			return false, nil
		}
	}
	return true, nil
}

func (r *runner) processHTTP(ctx context.Context, item queue.Item) error {
	if !r.hosts.acquire(item.Host) {
		return r.putBack(item, jittered(hostBusyDelay), "host busy")
	}
	defer r.hosts.release(item.Host)

	pageCtx, cancel := context.WithTimeout(ctx, r.options.PageTimeout)
	result, err := r.extractor.ExtractHTTP(pageCtx, item.URL)
	cancel()
	if ctx.Err() != nil {
		return r.putBack(item, 0, "shutdown")
	}
	if err != nil {
		r.stats.Failed.Add(1)
		return r.complete(queue.Completion{ID: item.ID, State: queue.Failed, Outcome: "failed", Detail: err.Error(), CountAttempt: true})
	}
	switch result.Outcome {
	case "ok":
		return r.store(ctx, item, result)
	case "dead":
		r.stats.Dead.Add(1)
		return r.complete(completion(item, queue.Dead, result))
	case orchestrator.OutcomeNeedsBrowser:
		r.stats.NeedsBrowser.Add(1)
		return r.complete(completion(item, queue.NeedsBrowser, result))
	default:
		r.stats.Failed.Add(1)
		failed := completion(item, queue.Failed, result)
		failed.CountAttempt = true
		return r.complete(failed)
	}
}

func (r *runner) processBrowser(ctx context.Context, item queue.Item) error {
	if !r.hosts.acquire(item.Host) {
		return r.putBack(item, jittered(hostBusyDelay), "host busy")
	}
	defer r.hosts.release(item.Host)

	pageCtx, cancel := context.WithTimeout(ctx, r.options.PageTimeout)
	result, err := r.extractor.ExtractBrowser(pageCtx, item.URL)
	cancel()
	if ctx.Err() != nil {
		return r.putBack(item, 0, "shutdown")
	}
	if errors.Is(err, limit.ErrSaturated) || errors.Is(err, limit.ErrUnavailable) {
		return r.putBack(item, jittered(capacityDelay), err.Error())
	}
	if err != nil {
		r.stats.Failed.Add(1)
		return r.complete(queue.Completion{ID: item.ID, State: queue.Failed, Outcome: "failed", Detail: err.Error(), CountAttempt: true})
	}
	switch result.Outcome {
	case "ok":
		return r.store(ctx, item, result)
	case "dead":
		r.stats.Dead.Add(1)
		return r.complete(completion(item, queue.Dead, result))
	}
	if item.Attempts+1 < r.options.MaxAttempts {
		r.stats.Retried.Add(1)
		delay := jittered(failureBaseDelay << min(item.Attempts, 6))
		if err := r.q.Retry(context.WithoutCancel(ctx), item.ID, delay, true, lastDetail(result)); err != nil {
			return fmt.Errorf("batch: retry %d: %w", item.ID, err)
		}
		return nil
	}
	r.stats.Failed.Add(1)
	failed := completion(item, queue.Failed, result)
	failed.CountAttempt = true
	return r.complete(failed)
}

func (r *runner) store(ctx context.Context, item queue.Item, result orchestrator.Result) error {
	if err := r.sink.Add(ctx, item, result); err != nil {
		if ctx.Err() != nil {
			return r.putBack(item, 0, "shutdown")
		}
		return err
	}
	r.stats.Done.Add(1)
	return nil
}

func (r *runner) complete(completion queue.Completion) error {
	ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
	defer cancel()
	if err := r.q.Complete(ctx, []queue.Completion{completion}); err != nil {
		return fmt.Errorf("batch: complete %d: %w", completion.ID, err)
	}
	return nil
}

func (r *runner) putBack(item queue.Item, delay time.Duration, detail string) error {
	r.stats.Deferred.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
	defer cancel()
	if err := r.q.Retry(ctx, item.ID, delay, false, detail); err != nil {
		return fmt.Errorf("batch: put back %d: %w", item.ID, err)
	}
	return nil
}

func (r *runner) release(items []queue.Item) {
	for _, item := range items {
		if err := r.putBack(item, 0, "shutdown"); err != nil {
			return
		}
	}
}

func (r *runner) reportProgress(ctx context.Context) func() {
	if r.options.Progress == nil {
		return func() {}
	}
	stop := make(chan struct{})
	var reporter sync.WaitGroup
	reporter.Go(func() {
		started := time.Now()
		ticker := time.NewTicker(r.options.ProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.writeProgress(time.Since(started))
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	})
	return func() {
		close(stop)
		reporter.Wait()
		r.writeProgress(0)
	}
}

func (r *runner) writeProgress(elapsed time.Duration) {
	s := r.stats
	line := fmt.Sprintf("pagelode batch: done %d, dead %d, failed %d, needs browser %d, retried %d, deferred %d", s.Done.Load(), s.Dead.Load(), s.Failed.Load(), s.NeedsBrowser.Load(), s.Retried.Load(), s.Deferred.Load())
	if elapsed > 0 {
		line += fmt.Sprintf(", %.1f done/s", float64(s.Done.Load())/elapsed.Seconds())
	}
	fmt.Fprintln(r.options.Progress, line)
}

func completion(item queue.Item, state queue.State, result orchestrator.Result) queue.Completion {
	completion := queue.Completion{ID: item.ID, State: state, Outcome: result.Outcome, Provider: string(result.Provider), Detail: lastDetail(result)}
	if count := len(result.Attempts); count > 0 {
		completion.StatusCode = result.Attempts[count-1].Status
	}
	return completion
}

func lastDetail(result orchestrator.Result) string {
	if count := len(result.Attempts); count > 0 {
		return result.Attempts[count-1].Detail
	}
	return ""
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

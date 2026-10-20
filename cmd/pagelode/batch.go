package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/simonbalfe/pagelode/batch"
	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/queue"
)

type batchRunFlags struct {
	queueURL        *string
	out             *string
	httpWorkers     *int
	browserWorkers  *int
	untilEmpty      *bool
	maxAttempts     *int
	hostConcurrency *int
	lease           *time.Duration
	pageTimeout     *time.Duration
}

func defineBatchRunFlags(flags *flag.FlagSet, configuration config.Config) batchRunFlags {
	return batchRunFlags{
		queueURL:        defineQueueFlag(flags, configuration),
		out:             flags.String("out", "data/pages", "directory for the gzipped JSONL page files"),
		httpWorkers:     flags.Int("http-workers", 100, "URLs loaded over HTTP at once"),
		browserWorkers:  flags.Int("browser-workers", -1, "URLs rendered at once; -1 uses every browser slot (nodes × PAGELODE_BROWSER_NODE_SLOTS), 0 skips the browser stage"),
		untilEmpty:      flags.Bool("until-empty", false, "exit when no URL is left instead of waiting for more"),
		maxAttempts:     flags.Int("max-attempts", 3, "browser failures before a URL is marked failed"),
		hostConcurrency: flags.Int("host-concurrency", 4, "pages of one host loaded at once by this process"),
		lease:           flags.Duration("lease", 10*time.Minute, "how long a claimed URL stays reserved for this process"),
		pageTimeout:     flags.Duration("page-timeout", 90*time.Second, "time budget for one stage of one URL"),
	}
}

func defineQueueFlag(flags *flag.FlagSet, configuration config.Config) *string {
	return flags.String("queue", configuration.QueueURL, "Postgres `URL` of the shared queue (PAGELODE_QUEUE_URL)")
}

func batchCommand(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: pagelode batch <import|run|status|retry> [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "import":
		return batchImport(ctx, configuration, args[1:], stdout, stderr)
	case "run":
		return batchRun(ctx, configuration, args[1:], stdout, stderr)
	case "status":
		return batchStatus(ctx, configuration, args[1:], stdout, stderr)
	case "retry":
		return batchRetry(ctx, configuration, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown batch command %q; use import, run, status, or retry", args[0])
	}
}

func openQueue(ctx context.Context, queueURL string) (*queue.Queue, error) {
	if queueURL == "" {
		return nil, errors.New("set PAGELODE_QUEUE_URL or --queue to a Postgres URL")
	}
	q, err := queue.Open(ctx, queueURL)
	if err != nil {
		return nil, fmt.Errorf("open queue: %w", err)
	}
	return q, nil
}

func parseBatchFlags(flags *flag.FlagSet, args []string) (bool, error) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func batchImport(ctx context.Context, configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("batch import", stderr)
	queueURL := defineQueueFlag(flags, configuration)
	batchSize := flags.Int("batch-size", 10_000, "URLs inserted per transaction")
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one file of URLs or domains, one per line, or - for standard input")
	}
	input := io.Reader(os.Stdin)
	if path := flags.Arg(0); path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open input: %w", err)
		}
		defer file.Close()
		input = file
	}
	q, err := openQueue(ctx, *queueURL)
	if err != nil {
		return err
	}
	defer q.Close()

	stats, err := batch.Import(ctx, q, input, *batchSize)
	fmt.Fprintf(stdout, "%d lines, %d new URLs queued, %d duplicates, %d invalid\n", stats.Lines, stats.Inserted, stats.Lines-stats.Invalid-stats.Inserted, stats.Invalid)
	return err
}

func batchRun(ctx context.Context, configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("batch run", stderr)
	options := defineBatchRunFlags(flags, configuration)
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("batch run takes no arguments")
	}
	browserWorkers := *options.browserWorkers
	if browserWorkers < 0 {
		browserWorkers = len(configuration.BrowserURLs) * configuration.BrowserNodeSlots
	}
	owner, err := batchOwner()
	if err != nil {
		return err
	}

	q, err := openQueue(ctx, *options.queueURL)
	if err != nil {
		return err
	}
	defer q.Close()
	app, err := newApplication(configuration)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			fmt.Fprintln(stderr, "pagelode: close:", err)
		}
	}()
	sink, err := batch.NewWriter(*options.out, owner, q)
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "pagelode batch: %s running %d HTTP and %d browser workers, writing to %s\n", owner, *options.httpWorkers, browserWorkers, *options.out)
	stats, err := batch.Run(ctx, q, app.extractor, sink, batch.Options{
		Owner:           owner,
		HTTPWorkers:     *options.httpWorkers,
		BrowserWorkers:  browserWorkers,
		Lease:           *options.lease,
		PageTimeout:     *options.pageTimeout,
		MaxAttempts:     *options.maxAttempts,
		HostConcurrency: *options.hostConcurrency,
		UntilEmpty:      *options.untilEmpty,
		Progress:        stderr,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d done, %d dead, %d failed, %d sent to the browser stage, %d retried, %d put back\n", stats.Done.Load(), stats.Dead.Load(), stats.Failed.Load(), stats.NeedsBrowser.Load(), stats.Retried.Load(), stats.Deferred.Load())
	return nil
}

func batchStatus(ctx context.Context, configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("batch status", stderr)
	queueURL := defineQueueFlag(flags, configuration)
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	q, err := openQueue(ctx, *queueURL)
	if err != nil {
		return err
	}
	defer q.Close()
	counts, err := q.Counts(ctx)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(table, "state\tready\twaiting\ttotal\t")
	for _, count := range counts {
		fmt.Fprintf(table, "%s\t%d\t%d\t%d\t\n", count.State, count.Ready, count.Waiting, count.Ready+count.Waiting)
	}
	return table.Flush()
}

func batchRetry(ctx context.Context, configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("batch retry", stderr)
	queueURL := defineQueueFlag(flags, configuration)
	from := flags.String("state", string(queue.Failed), "state to move back: failed, dead, or done")
	to := flags.String("to", string(queue.Pending), "state to move URLs to: pending or needs_browser")
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	q, err := openQueue(ctx, *queueURL)
	if err != nil {
		return err
	}
	defer q.Close()
	moved, err := q.Reset(ctx, queue.State(*from), queue.State(*to))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d URLs moved from %s to %s\n", moved, *from, *to)
	return nil
}

func batchOwner() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("batch owner: %w", err)
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid()), nil
}

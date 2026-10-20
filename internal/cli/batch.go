package cli

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

	"github.com/riverqueue/river/rivertype"

	"github.com/simonbalfe/pagelode/internal/store"
	"github.com/simonbalfe/pagelode/pkg/batch"
)

type batchRunFlags struct {
	databaseURL      *string
	out              *string
	httpWorkers      *int
	browserWorkers   *int
	untilEmpty       *bool
	hostConcurrency  *int
	lease            *time.Duration
	pageTimeout      *time.Duration
	progressInterval *time.Duration
}

func defineBatchRunFlags(flags *flag.FlagSet, configuration Config) batchRunFlags {
	return batchRunFlags{
		databaseURL:      defineDatabaseFlag(flags, configuration),
		out:              flags.String("out", "data/pages", "directory for the gzipped JSONL page files"),
		httpWorkers:      flags.Int("http-workers", 1000, "URLs loaded over HTTP at once"),
		browserWorkers:   flags.Int("browser-workers", -1, "URLs rendered at once; -1 uses every browser slot (nodes × PAGELODE_BROWSER_NODE_SLOTS), 0 skips the browser stage"),
		untilEmpty:       flags.Bool("until-empty", false, "exit when no URL is left instead of waiting for more"),
		hostConcurrency:  flags.Int("host-concurrency", 4, "pages of one host loaded at once by this process"),
		lease:            flags.Duration("lease", 10*time.Minute, "how long a running URL may go without finishing before another process takes it over"),
		pageTimeout:      flags.Duration("page-timeout", 10*time.Second, "time budget for loading one URL in one stage; a page that takes longer fails without a retry"),
		progressInterval: flags.Duration("progress-interval", 10*time.Second, "how often to print queue-wide progress to stderr"),
	}
}

func defineDatabaseFlag(flags *flag.FlagSet, configuration Config) *string {
	return flags.String("database", configuration.DatabaseURL, "Postgres `URL` of the shared queue (PAGELODE_DATABASE_URL)")
}

func batchCommand(configuration Config, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: pagelode extract batch <import|run|status|retry> [flags]")
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
		return fmt.Errorf("unknown extract batch command %q; use import, run, status, or retry", args[0])
	}
}

func openQueue(ctx context.Context, databaseURL string) (*batch.Queue, error) {
	if databaseURL == "" {
		return nil, errors.New("extract batch keeps its queue in Postgres: set PAGELODE_DATABASE_URL or --database to a Postgres URL")
	}
	q, err := batch.Open(ctx, databaseURL)
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

func batchImport(ctx context.Context, configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("extract batch import", stderr)
	databaseURL := defineDatabaseFlag(flags, configuration)
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
	q, err := openQueue(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer q.Close()

	stats, err := q.Import(ctx, input, *batchSize)
	fmt.Fprintf(stdout, "%d lines, %d new URLs queued, %d duplicates, %d invalid\n", stats.Lines, stats.Inserted, stats.Duplicates, stats.Invalid)
	return err
}

func batchRun(ctx context.Context, configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("extract batch run", stderr)
	options := defineBatchRunFlags(flags, configuration)
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("extract batch run takes no arguments")
	}
	browserWorkers := *options.browserWorkers
	if browserWorkers < 0 {
		browserWorkers = len(configuration.BrowserURLs) * configuration.BrowserNodeSlots
	}
	owner, err := batchOwner()
	if err != nil {
		return err
	}

	q, err := openQueue(ctx, *options.databaseURL)
	if err != nil {
		return err
	}
	defer q.Close()
	configuration.DatabaseURL = *options.databaseURL
	app, err := newApplication(ctx, configuration)
	if err != nil {
		return err
	}
	defer app.Close()
	sink, err := batch.NewWriter(*options.out, owner)
	if err != nil {
		return err
	}

	header := palette(stderr)
	fmt.Fprintf(stderr, "%s  %d HTTP + %d browser workers  %s  %s\n", header("1", "pagelode batch"), *options.httpWorkers, browserWorkers, header("2", "writing to"), *options.out)
	stats, err := q.Run(ctx, app.extractor, sink, batch.Options{
		Owner:            owner,
		HTTPWorkers:      *options.httpWorkers,
		BrowserWorkers:   browserWorkers,
		Lease:            *options.lease,
		PageTimeout:      *options.pageTimeout,
		HostConcurrency:  *options.hostConcurrency,
		UntilEmpty:       *options.untilEmpty,
		Progress:         stderr,
		Color:            colorful(stderr),
		ProgressInterval: *options.progressInterval,
		Counts: func(ctx context.Context) (batch.Counts, error) {
			return batchCounts(ctx, app.store)
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d done, %d dead, %d failed, %d sent to the browser stage, %d put back\n", stats.Done.Load(), stats.Dead.Load(), stats.Failed.Load(), stats.NeedsBrowser.Load(), stats.Deferred.Load())
	return nil
}

func batchStatus(ctx context.Context, configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("extract batch status", stderr)
	databaseURL := defineDatabaseFlag(flags, configuration)
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	q, persistence, err := openQueueAndStore(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer q.Close()
	defer persistence.Close()
	counts, err := batchCounts(ctx, persistence)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(table, "stage\tstate\tURLs\t")
	for _, kind := range batch.Kinds() {
		stage := batch.Stage(kind)
		for _, state := range batch.States() {
			fmt.Fprintf(table, "%s\t%s\t%d\t\n", stage, state, counts[stage][state])
		}
	}
	return table.Flush()
}

func batchCounts(ctx context.Context, persistence *store.Store) (batch.Counts, error) {
	rows, err := persistence.BatchJobCounts(ctx, batch.Kinds())
	if err != nil {
		return nil, err
	}
	counts := make(batch.Counts)
	for _, row := range rows {
		stage := batch.Stage(row.Kind)
		if counts[stage] == nil {
			counts[stage] = make(map[batch.State]int64)
		}
		counts[stage][batch.StateOf(rivertype.JobState(row.State))] += row.Jobs
	}
	return counts, nil
}

func batchRetry(ctx context.Context, configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("extract batch retry", stderr)
	databaseURL := defineDatabaseFlag(flags, configuration)
	from := flags.String("state", string(batch.Failed), "state to retry: failed or dead")
	if proceed, err := parseBatchFlags(flags, args); !proceed {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("extract batch retry takes no arguments")
	}
	jobState, err := batch.State(*from).RetryFrom()
	if err != nil {
		return err
	}
	q, persistence, err := openQueueAndStore(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer q.Close()
	defer persistence.Close()
	ids, err := persistence.BatchJobIDs(ctx, batch.Kinds(), string(jobState))
	if err != nil {
		return err
	}
	retried, err := q.Retry(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d %s URLs moved back to pending in their stage\n", retried, *from)
	return nil
}

func openQueueAndStore(ctx context.Context, databaseURL string) (*batch.Queue, *store.Store, error) {
	q, err := openQueue(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	persistence, err := store.Open(ctx, databaseURL)
	if err != nil {
		q.Close()
		return nil, nil, err
	}
	return q, persistence, nil
}

func batchOwner() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("batch owner: %w", err)
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid()), nil
}

package batch

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/simonbalfe/pagelode/pkg/fetch"
)

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	dsn := os.Getenv("PAGELODE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PAGELODE_TEST_DATABASE_URL not set")
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "pagelode_test_" + hex.EncodeToString(suffix)
	q, err := open(context.Background(), dsn, schema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		defer q.Close()
		if _, err := q.pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
	})
	return q
}

func importURLs(t *testing.T, q *Queue, urls ...string) {
	t.Helper()
	stats, err := q.Import(context.Background(), strings.NewReader(strings.Join(urls, "\n")), 100)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if stats.Inserted != int64(len(urls)) {
		t.Fatalf("Import() inserted %d of %d URLs", stats.Inserted, len(urls))
	}
}

type fakeExtractor struct {
	http    func(ctx context.Context, url string) (fetch.Result, error)
	browser func(ctx context.Context, url string) (fetch.Result, error)
}

func (f fakeExtractor) ExtractHTTP(ctx context.Context, url string) (fetch.Result, error) {
	return f.http(ctx, url)
}

func (f fakeExtractor) ExtractBrowser(ctx context.Context, url string) (fetch.Result, error) {
	return f.browser(ctx, url)
}

func ok(url string, provider fetch.Provider) fetch.Result {
	return fetch.Result{URL: url, Content: "content of " + url, Provider: provider, Outcome: "ok", Links: []string{}}
}

func okOver(provider fetch.Provider) func(context.Context, string) (fetch.Result, error) {
	return func(_ context.Context, url string) (fetch.Result, error) { return ok(url, provider), nil }
}

func needsBrowser(_ context.Context, url string) (fetch.Result, error) {
	return fetch.Result{URL: url, Outcome: fetch.OutcomeNeedsBrowser}, nil
}

func testOptions(options Options) Options {
	options.Owner = "test-owner"
	options.Idle = 20 * time.Millisecond
	options.hostBusyDelay = 5 * time.Millisecond
	options.capacityDelay = 5 * time.Millisecond
	options.fetchPollInterval = 100 * time.Millisecond
	return options
}

func runUntilEmpty(t *testing.T, q *Queue, extractor Extractor, options Options) (*Stats, string) {
	t.Helper()
	dir := t.TempDir()
	options = testOptions(options)
	options.UntilEmpty = true
	if options.BrowserWorkers == 0 {
		options.BrowserWorkers = 2
	}
	sink, err := NewWriter(dir, options.Owner)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stats, err := q.Run(ctx, extractor, sink, options)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Run() did not stop once the queue was empty")
	}
	return stats, dir
}

type jobKey struct {
	kind string
	url  string
}

func listJobs(t *testing.T, q *Queue) map[jobKey]*rivertype.JobRow {
	t.Helper()
	result, err := q.client.JobList(context.Background(), river.NewJobListParams().First(10_000))
	if err != nil {
		t.Fatalf("JobList() error = %v", err)
	}
	jobs := make(map[jobKey]*rivertype.JobRow, len(result.Jobs))
	for _, job := range result.Jobs {
		var args HTTPArgs
		if err := json.Unmarshal(job.EncodedArgs, &args); err != nil {
			t.Fatalf("decode args of job %d: %v", job.ID, err)
		}
		jobs[jobKey{kind: job.Kind, url: args.URL}] = job
	}
	return jobs
}

func job(t *testing.T, jobs map[jobKey]*rivertype.JobRow, kind, url string) *rivertype.JobRow {
	t.Helper()
	found, ok := jobs[jobKey{kind: kind, url: url}]
	if !ok {
		t.Fatalf("no %s job for %s", kind, url)
	}
	return found
}

func outputOfJob(t *testing.T, job *rivertype.JobRow) Output {
	t.Helper()
	var metadata struct {
		Output Output `json:"output"`
	}
	if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
		t.Fatalf("decode metadata of job %d: %v", job.ID, err)
	}
	return metadata.Output
}

func TestRunRoutesEachURLThroughTheStagesItNeeds(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	const plain, shell, gone = "https://plain.example/a", "https://shell.example/b", "https://gone.example/c"
	importURLs(t, q, plain, shell, gone)
	extractor := fakeExtractor{
		http: func(_ context.Context, url string) (fetch.Result, error) {
			switch url {
			case plain:
				return ok(url, fetch.ProviderTLS), nil
			case gone:
				return fetch.Result{URL: url, Outcome: "dead", Attempts: []fetch.Attempt{{Status: 404}}}, nil
			}
			return needsBrowser(context.Background(), url)
		},
		browser: okOver(fetch.ProviderPatchright),
	}

	stats, dir := runUntilEmpty(t, q, extractor, Options{})

	jobs := listJobs(t, q)
	for _, test := range []struct {
		kind, url string
		state     rivertype.JobState
		provider  fetch.Provider
	}{
		{HTTPKind, plain, rivertype.JobStateCompleted, fetch.ProviderTLS},
		{HTTPKind, shell, rivertype.JobStateCompleted, ""},
		{BrowserKind, shell, rivertype.JobStateCompleted, fetch.ProviderPatchright},
		{HTTPKind, gone, rivertype.JobStateCancelled, ""},
	} {
		found := job(t, jobs, test.kind, test.url)
		if found.State != test.state {
			t.Errorf("%s %s state = %q, want %q", test.kind, test.url, found.State, test.state)
		}
		if test.provider == "" {
			continue
		}
		output := outputOfJob(t, found)
		record := readRecord(t, dir, output.ContentRef)
		if record.ID != found.ID || record.URL != test.url || record.Provider != test.provider {
			t.Errorf("record at %q = id %d url %q provider %q, want job %d from %q", output.ContentRef, record.ID, record.URL, record.Provider, found.ID, test.provider)
		}
	}
	if output := outputOfJob(t, job(t, jobs, HTTPKind, shell)); output.Outcome != fetch.OutcomeNeedsBrowser {
		t.Errorf("HTTP output for the shell = %+v, want outcome %s", output, fetch.OutcomeNeedsBrowser)
	}
	if output := outputOfJob(t, job(t, jobs, HTTPKind, gone)); output.StatusCode != 404 {
		t.Errorf("HTTP output for the dead page = %+v, want status 404", output)
	}
	if len(jobs) != 4 {
		t.Errorf("%d jobs, want 4", len(jobs))
	}
	if stats.Done.Load() != 2 || stats.Dead.Load() != 1 || stats.NeedsBrowser.Load() != 1 {
		t.Errorf("stats done %d dead %d needs browser %d, want 2, 1, 1", stats.Done.Load(), stats.Dead.Load(), stats.NeedsBrowser.Load())
	}
}

func TestRunSnoozesWithoutUsingAttemptsWhenTheBrowserHasNoCapacity(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	const url = "https://shell.example/"
	importURLs(t, q, url)
	var calls atomic.Int32
	extractor := fakeExtractor{
		http: needsBrowser,
		browser: func(_ context.Context, url string) (fetch.Result, error) {
			switch calls.Add(1) {
			case 1:
				return fetch.Result{}, fmt.Errorf("extract: %w", fetch.ErrSaturated)
			case 2:
				return fetch.Result{}, fmt.Errorf("extract: %w", fetch.ErrUnavailable)
			}
			return ok(url, fetch.ProviderPatchright), nil
		},
	}

	runUntilEmpty(t, q, extractor, Options{})

	found := job(t, listJobs(t, q), BrowserKind, url)
	if found.State != rivertype.JobStateCompleted || found.Attempt != 1 || len(found.Errors) != 0 || calls.Load() != 3 {
		t.Errorf("browser job = %q at attempt %d with %d errors after %d calls, want completed at attempt 1 with no errors after 3", found.State, found.Attempt, len(found.Errors), calls.Load())
	}
}

func TestRunFailsAFailedRenderAtOnceAndRetryRequeuesIt(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	const url = "https://blocked.example/"
	importURLs(t, q, url)
	var calls atomic.Int32
	extractor := fakeExtractor{
		http: needsBrowser,
		browser: func(_ context.Context, url string) (fetch.Result, error) {
			calls.Add(1)
			return fetch.Result{URL: url, Outcome: "failed", Attempts: []fetch.Attempt{{Provider: fetch.ProviderPatchright, Outcome: "blocked", Status: 403}}}, nil
		},
	}

	stats, _ := runUntilEmpty(t, q, extractor, Options{})

	found := job(t, listJobs(t, q), BrowserKind, url)
	if found.State != rivertype.JobStateDiscarded || found.Attempt != 1 || len(found.Errors) != 1 || calls.Load() != 1 {
		t.Fatalf("browser job = %q at attempt %d with %d errors after %d calls, want discarded at 1 with 1 error after 1", found.State, found.Attempt, len(found.Errors), calls.Load())
	}
	if stats.Failed.Load() != 1 {
		t.Errorf("stats failed %d, want 1", stats.Failed.Load())
	}

	retried, err := q.Retry(context.Background(), []int64{found.ID})
	if err != nil || retried != 1 {
		t.Fatalf("Retry() = %d, %v; want 1, nil", retried, err)
	}
	found = job(t, listJobs(t, q), BrowserKind, url)
	if found.State != rivertype.JobStateAvailable || found.MaxAttempts != 2 {
		t.Errorf("retried job = %q with max attempts %d, want available with 2", found.State, found.MaxAttempts)
	}
}

func TestWriterAnswersOnlyOnceThePageIsInItsFinalFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := NewWriter(dir, "test-owner")
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	refs := make([]string, 20)
	var adds sync.WaitGroup
	for id := range int64(len(refs)) {
		adds.Go(func() {
			ref, err := sink.Add(context.Background(), id, ok("https://host"+strconv.FormatInt(id, 10)+".example/", fetch.ProviderTLS))
			if err != nil {
				t.Errorf("Add(%d) error = %v", id, err)
				return
			}
			name, _, _ := strings.Cut(ref, ":")
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("Add(%d) returned %q before the file existed under its final name: %v", id, ref, err)
				return
			}
			refs[id] = ref
		})
	}
	adds.Wait()
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for id, ref := range refs {
		if ref == "" {
			continue
		}
		if record := readRecord(t, dir, ref); record.ID != int64(id) || record.URL != "https://host"+strconv.Itoa(id)+".example/" {
			t.Errorf("record at %q = id %d url %q, want %d", ref, record.ID, record.URL, id)
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*"+partSuffix)); len(parts) != 0 {
		t.Errorf("unfinished files left behind: %v", parts)
	}
}

func TestRunCompletesEveryPageWithAReadableRecord(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	urls := make([]string, 50)
	for index := range urls {
		urls[index] = "https://host" + strconv.Itoa(index) + ".example/"
	}
	importURLs(t, q, urls...)
	extractor := fakeExtractor{http: okOver(fetch.ProviderTLS), browser: okOver(fetch.ProviderPatchright)}

	_, dir := runUntilEmpty(t, q, extractor, Options{HTTPWorkers: 8})

	jobs := listJobs(t, q)
	for _, url := range urls {
		found := job(t, jobs, HTTPKind, url)
		if found.State != rivertype.JobStateCompleted {
			t.Errorf("%s state = %q, want completed", url, found.State)
			continue
		}
		if record := readRecord(t, dir, outputOfJob(t, found).ContentRef); record.ID != found.ID || record.URL != url {
			t.Errorf("record for %s = id %d url %q, want job %d", url, record.ID, record.URL, found.ID)
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*"+partSuffix)); len(parts) != 0 {
		t.Errorf("unfinished files left behind: %v", parts)
	}
}

func TestRunNeverLoadsMoreThanHostConcurrencyPagesOfOneHost(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	urls := make([]string, 20)
	for index := range urls {
		urls[index] = "https://same.example/" + strconv.Itoa(index)
	}
	importURLs(t, q, urls...)
	var active, peak atomic.Int32
	extractor := fakeExtractor{
		http: func(_ context.Context, url string) (fetch.Result, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			return ok(url, fetch.ProviderTLS), nil
		},
		browser: okOver(fetch.ProviderPatchright),
	}

	runUntilEmpty(t, q, extractor, Options{HTTPWorkers: 10, HostConcurrency: 2})

	if peak.Load() > 2 {
		t.Errorf("peak concurrent pages for one host = %d, want at most 2", peak.Load())
	}
	jobs := listJobs(t, q)
	for _, url := range urls {
		if found := job(t, jobs, HTTPKind, url); found.State != rivertype.JobStateCompleted {
			t.Errorf("%s state = %q, want completed", url, found.State)
		}
	}
}

func TestRunPutsRunningURLsBackWithoutUsingAttemptsOnShutdown(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	const url = "https://slow.example/"
	importURLs(t, q, url)
	started := make(chan struct{})
	extractor := fakeExtractor{
		http: func(ctx context.Context, url string) (fetch.Result, error) {
			close(started)
			select {
			case <-ctx.Done():
				return fetch.Result{}, ctx.Err()
			case <-time.After(30 * time.Second):
				return ok(url, fetch.ProviderTLS), nil
			}
		},
		browser: okOver(fetch.ProviderPatchright),
	}
	sink, err := NewWriter(t.TempDir(), "test-owner")
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	stats, err := q.Run(ctx, extractor, sink, testOptions(Options{}))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil on shutdown", err)
	}

	found := job(t, listJobs(t, q), HTTPKind, url)
	if found.State != rivertype.JobStateAvailable || found.Attempt != 0 || len(found.Errors) != 0 {
		t.Errorf("job after shutdown = %q at attempt %d with %d errors, want available at 0 with none", found.State, found.Attempt, len(found.Errors))
	}
	if stats.Deferred.Load() != 1 {
		t.Errorf("deferred = %d, want 1", stats.Deferred.Load())
	}
}

func TestImportCountsNewDuplicateAndInvalidLines(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	input := "example.com\n# comment\n\nhttps://Example.com/#top\nnot a url ://\nhttp://other.example/a\nftp://files.example/\n"

	stats, err := q.Import(context.Background(), strings.NewReader(input), 2)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if want := (ImportStats{Lines: 5, Invalid: 2, Inserted: 2, Duplicates: 1}); stats != want {
		t.Errorf("Import() stats = %+v, want %+v", stats, want)
	}
	jobs := listJobs(t, q)
	for _, url := range []string{"https://example.com/", "http://other.example/a"} {
		if found := job(t, jobs, HTTPKind, url); found.State != rivertype.JobStateAvailable || found.Queue != httpQueue {
			t.Errorf("%s = %q on queue %q, want available on %q", url, found.State, found.Queue, httpQueue)
		}
	}
	if len(jobs) != 2 {
		t.Errorf("%d jobs, want 2", len(jobs))
	}

	stats, err = q.Import(context.Background(), strings.NewReader(input), 1)
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if want := (ImportStats{Lines: 5, Invalid: 2, Inserted: 0, Duplicates: 3}); stats != want {
		t.Errorf("second Import() stats = %+v, want %+v", stats, want)
	}
}

func readRecord(t *testing.T, dir, ref string) Record {
	t.Helper()
	name, lineText, found := strings.Cut(ref, ":")
	line, err := strconv.Atoi(lineText)
	if !found || err != nil {
		t.Fatalf("content ref %q is not <file>:<line>", ref)
	}
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip %s: %v", name, err)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for current := 1; scanner.Scan(); current++ {
		if current != line {
			continue
		}
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode %s line %d: %v", name, line, err)
		}
		return record
	}
	t.Fatalf("%s has no line %d", name, line)
	return Record{}
}

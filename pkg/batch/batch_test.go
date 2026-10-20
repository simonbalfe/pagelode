package batch

import (
	"bufio"
	"compress/gzip"
	"context"
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

	"github.com/simonbalfe/pagelode/pkg/limit"
	"github.com/simonbalfe/pagelode/pkg/orchestrator"
	"github.com/simonbalfe/pagelode/pkg/page"
	"github.com/simonbalfe/pagelode/pkg/queue"
)

type memoryQueue struct {
	mu         sync.Mutex
	rows       map[int64]*memoryRow
	nextID     int64
	onComplete func(queue.Completion)
}

type memoryRow struct {
	item       queue.Item
	state      queue.State
	readyAt    time.Time
	contentRef string
}

func newMemoryQueue(t *testing.T, urls ...string) *memoryQueue {
	t.Helper()
	q := &memoryQueue{rows: make(map[int64]*memoryRow)}
	for _, raw := range urls {
		entry, err := Normalize(raw)
		if err != nil {
			t.Fatalf("Normalize(%q) error = %v", raw, err)
		}
		q.nextID++
		q.rows[q.nextID] = &memoryRow{item: queue.Item{ID: q.nextID, URL: entry.URL, Host: entry.Host}, state: queue.Pending}
	}
	return q
}

func (q *memoryQueue) Claim(_ context.Context, state queue.State, limit int, lease time.Duration, _ string) ([]queue.Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	var items []queue.Item
	for id := int64(1); id <= q.nextID && len(items) < limit; id++ {
		row := q.rows[id]
		if row.state == state && !row.readyAt.After(now) {
			row.readyAt = now.Add(lease)
			items = append(items, row.item)
		}
	}
	return items, nil
}

func (q *memoryQueue) Complete(_ context.Context, completions []queue.Completion) error {
	for _, completion := range completions {
		if q.onComplete != nil {
			q.onComplete(completion)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, completion := range completions {
		row := q.rows[completion.ID]
		row.state = completion.State
		row.contentRef = completion.ContentRef
		row.readyAt = time.Time{}
		if completion.CountAttempt {
			row.item.Attempts++
		}
	}
	return nil
}

func (q *memoryQueue) Retry(_ context.Context, id int64, _ time.Duration, countAttempt bool, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	row := q.rows[id]
	row.readyAt = time.Time{}
	if countAttempt {
		row.item.Attempts++
	}
	return nil
}

func (q *memoryQueue) Remaining(_ context.Context, state queue.State) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, row := range q.rows {
		if row.state == state {
			return true, nil
		}
	}
	return false, nil
}

func (q *memoryQueue) row(id int64) memoryRow {
	q.mu.Lock()
	defer q.mu.Unlock()
	return *q.rows[id]
}

type fakeExtractor struct {
	http    func(url string) (orchestrator.Result, error)
	browser func(url string) (orchestrator.Result, error)
}

func (f fakeExtractor) ExtractHTTP(_ context.Context, url string) (orchestrator.Result, error) {
	return f.http(url)
}

func (f fakeExtractor) ExtractBrowser(_ context.Context, url string) (orchestrator.Result, error) {
	return f.browser(url)
}

func ok(url string, provider page.Provider) orchestrator.Result {
	return orchestrator.Result{URL: url, Content: "content of " + url, Provider: provider, Outcome: "ok", Links: []string{}}
}

func needsBrowser(url string) (orchestrator.Result, error) {
	return orchestrator.Result{URL: url, Outcome: orchestrator.OutcomeNeedsBrowser}, nil
}

func runUntilEmpty(t *testing.T, q *memoryQueue, extractor Extractor, options Options) (*Stats, string) {
	t.Helper()
	dir := t.TempDir()
	options.Owner = "test-owner"
	options.UntilEmpty = true
	options.Idle = 5 * time.Millisecond
	if options.BrowserWorkers == 0 {
		options.BrowserWorkers = 2
	}
	sink, err := NewWriter(dir, options.Owner, q)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stats, err := Run(ctx, q, extractor, sink, options)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Run() did not stop once the queue was empty")
	}
	return stats, dir
}

func TestRunRoutesEachURLThroughTheStagesItNeeds(t *testing.T) {
	t.Parallel()
	q := newMemoryQueue(t, "https://plain.example/a", "https://shell.example/b", "https://gone.example/c")
	extractor := fakeExtractor{
		http: func(url string) (orchestrator.Result, error) {
			switch {
			case strings.Contains(url, "plain"):
				return ok(url, page.ProviderTLS), nil
			case strings.Contains(url, "gone"):
				return orchestrator.Result{URL: url, Outcome: "dead"}, nil
			default:
				return needsBrowser(url)
			}
		},
		browser: func(url string) (orchestrator.Result, error) { return ok(url, page.ProviderPatchright), nil },
	}

	_, dir := runUntilEmpty(t, q, extractor, Options{})

	for id, want := range map[int64]queue.State{1: queue.Done, 2: queue.Done, 3: queue.Dead} {
		if got := q.row(id).state; got != want {
			t.Errorf("row %d state = %q, want %q", id, got, want)
		}
	}
	for id, provider := range map[int64]page.Provider{1: page.ProviderTLS, 2: page.ProviderPatchright} {
		record := readRecord(t, dir, q.row(id).contentRef)
		if record.ID != id || record.URL != q.row(id).item.URL || record.Provider != provider {
			t.Errorf("record at %q = id %d url %q provider %q, want row %d from %q", q.row(id).contentRef, record.ID, record.URL, record.Provider, id, provider)
		}
	}
}

func TestRunPutsURLsBackWithoutUsingAttemptsWhenTheBrowserHasNoCapacity(t *testing.T) {
	t.Parallel()
	q := newMemoryQueue(t, "https://shell.example/")
	var calls atomic.Int32
	extractor := fakeExtractor{
		http: needsBrowser,
		browser: func(url string) (orchestrator.Result, error) {
			switch calls.Add(1) {
			case 1:
				return orchestrator.Result{}, fmt.Errorf("extract: %w", limit.ErrSaturated)
			case 2:
				return orchestrator.Result{}, fmt.Errorf("extract: %w", limit.ErrUnavailable)
			default:
				return ok(url, page.ProviderPatchright), nil
			}
		},
	}

	runUntilEmpty(t, q, extractor, Options{MaxAttempts: 1})

	if row := q.row(1); row.state != queue.Done || row.item.Attempts != 0 || calls.Load() != 3 {
		t.Errorf("row = %q with %d attempts after %d browser calls, want done with 0 attempts after 3", row.state, row.item.Attempts, calls.Load())
	}
}

func TestRunFailsAURLOnlyAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	q := newMemoryQueue(t, "https://blocked.example/")
	var calls atomic.Int32
	extractor := fakeExtractor{
		http: needsBrowser,
		browser: func(url string) (orchestrator.Result, error) {
			calls.Add(1)
			return orchestrator.Result{URL: url, Outcome: "failed", Attempts: []orchestrator.Attempt{{Provider: page.ProviderPatchright, Outcome: "blocked", Status: 403}}}, nil
		},
	}

	runUntilEmpty(t, q, extractor, Options{MaxAttempts: 3})

	if row := q.row(1); row.state != queue.Failed || row.item.Attempts != 3 || calls.Load() != 3 {
		t.Errorf("row = %q with %d attempts after %d browser calls, want failed with 3 after 3", row.state, row.item.Attempts, calls.Load())
	}
}

func TestRunCompletesPagesOnlyAfterTheirFileIsOnDisk(t *testing.T) {
	t.Parallel()
	urls := make([]string, 50)
	for index := range urls {
		urls[index] = "https://host" + strconv.Itoa(index) + ".example/"
	}
	q := newMemoryQueue(t, urls...)
	dir := t.TempDir()
	var missing atomic.Int32
	q.onComplete = func(completion queue.Completion) {
		name, _, _ := strings.Cut(completion.ContentRef, ":")
		if _, err := os.Stat(filepath.Join(dir, name)); completion.State == queue.Done && err != nil {
			missing.Add(1)
		}
	}
	extractor := fakeExtractor{
		http:    func(url string) (orchestrator.Result, error) { return ok(url, page.ProviderTLS), nil },
		browser: func(url string) (orchestrator.Result, error) { return ok(url, page.ProviderPatchright), nil },
	}
	sink, err := NewWriter(dir, "test-owner", q)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	if _, err := Run(context.Background(), q, extractor, sink, Options{Owner: "test-owner", UntilEmpty: true, Idle: 5 * time.Millisecond, HTTPWorkers: 8}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if missing.Load() != 0 {
		t.Errorf("%d pages completed before their file existed under its final name", missing.Load())
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*"+partSuffix)); len(parts) != 0 {
		t.Errorf("unfinished files left behind: %v", parts)
	}
	for id := int64(1); id <= int64(len(urls)); id++ {
		if got := q.row(id).state; got != queue.Done {
			t.Errorf("row %d state = %q, want done", id, got)
		}
	}
}

func TestRunNeverLoadsMoreThanHostConcurrencyPagesOfOneHost(t *testing.T) {
	t.Parallel()
	urls := make([]string, 20)
	for index := range urls {
		urls[index] = "https://same.example/" + strconv.Itoa(index)
	}
	q := newMemoryQueue(t, urls...)
	var active, peak atomic.Int32
	extractor := fakeExtractor{
		http: func(url string) (orchestrator.Result, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			return ok(url, page.ProviderTLS), nil
		},
		browser: func(url string) (orchestrator.Result, error) { return ok(url, page.ProviderPatchright), nil },
	}

	runUntilEmpty(t, q, extractor, Options{HTTPWorkers: 10, HostConcurrency: 2})

	if peak.Load() > 2 {
		t.Errorf("peak concurrent pages for one host = %d, want at most 2", peak.Load())
	}
	for id := int64(1); id <= int64(len(urls)); id++ {
		if got := q.row(id).state; got != queue.Done {
			t.Errorf("row %d state = %q, want done", id, got)
		}
	}
}

func TestRunReleasesClaimedURLsOnShutdown(t *testing.T) {
	t.Parallel()
	q := newMemoryQueue(t, "https://slow.example/")
	started := make(chan struct{})
	extractor := fakeExtractor{
		http: func(url string) (orchestrator.Result, error) {
			close(started)
			time.Sleep(50 * time.Millisecond)
			return ok(url, page.ProviderTLS), nil
		},
	}
	sink, err := NewWriter(t.TempDir(), "test-owner", q)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	if _, err := Run(ctx, q, extractor, sink, Options{Owner: "test-owner", Idle: 5 * time.Millisecond}); err != nil {
		t.Fatalf("Run() error = %v, want nil on shutdown", err)
	}

	if row := q.row(1); row.state != queue.Pending || !row.readyAt.IsZero() || row.item.Attempts != 0 {
		t.Errorf("row after shutdown = %q ready at %v with %d attempts, want pending, claimable now, 0 attempts", row.state, row.readyAt, row.item.Attempts)
	}
}

func TestImportCountsNewDuplicateAndInvalidLines(t *testing.T) {
	t.Parallel()
	q := &recordingImporter{seen: map[string]bool{}}
	input := "example.com\n# comment\n\nhttps://Example.com/#top\nnot a url ://\nhttp://other.example/a\nftp://files.example/\n"

	stats, err := Import(context.Background(), q, strings.NewReader(input), 2)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if want := (ImportStats{Lines: 5, Invalid: 2, Inserted: 2}); stats != want {
		t.Errorf("Import() stats = %+v, want %+v", stats, want)
	}
	if want := []string{"https://example.com/", "http://other.example/a"}; strings.Join(q.inserted, " ") != strings.Join(want, " ") {
		t.Errorf("inserted URLs = %v, want %v", q.inserted, want)
	}
}

type recordingImporter struct {
	seen     map[string]bool
	inserted []string
}

func (r *recordingImporter) Import(_ context.Context, entries []queue.Entry) (int64, error) {
	var inserted int64
	for _, entry := range entries {
		if r.seen[entry.URL] {
			continue
		}
		r.seen[entry.URL] = true
		r.inserted = append(r.inserted, entry.URL)
		inserted++
	}
	return inserted, nil
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

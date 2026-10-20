package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	dsn := os.Getenv("PAGELODE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PAGELODE_TEST_POSTGRES_URL not set")
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

func entries(names ...string) []Entry {
	out := make([]Entry, len(names))
	for i, n := range names {
		out[i] = Entry{URL: "https://" + n + ".example/", Host: n + ".example"}
	}
	return out
}

func mustImport(t *testing.T, q *Queue, names ...string) {
	t.Helper()
	if _, err := q.Import(context.Background(), entries(names...)); err != nil {
		t.Fatalf("Import: %v", err)
	}
}

func mustClaim(t *testing.T, q *Queue, state State, limit int, lease time.Duration) []Item {
	t.Helper()
	items, err := q.Claim(context.Background(), state, limit, lease, "test")
	if err != nil {
		t.Fatalf("Claim(%s): %v", state, err)
	}
	return items
}

func mustComplete(t *testing.T, q *Queue, completions ...Completion) {
	t.Helper()
	if err := q.Complete(context.Background(), completions); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func mustCounts(t *testing.T, q *Queue) []Count {
	t.Helper()
	counts, err := q.Counts(context.Background())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	return counts
}

func urls(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.URL
	}
	return out
}

type stored struct {
	State      State
	Outcome    string
	Provider   string
	StatusCode int
	Detail     string
	ContentRef string
	Attempts   int
	Leased     bool
}

func storedRow(t *testing.T, q *Queue, id int64) stored {
	t.Helper()
	var (
		s     stored
		state string
	)
	err := q.pool.QueryRow(context.Background(), `
		SELECT state, coalesce(outcome, ''), coalesce(provider, ''), coalesce(status_code, 0),
			coalesce(detail, ''), coalesce(content_ref, ''), attempts, lease_owner IS NOT NULL
		FROM `+q.table+` WHERE id = $1`, id).
		Scan(&state, &s.Outcome, &s.Provider, &s.StatusCode, &s.Detail, &s.ContentRef, &s.Attempts, &s.Leased)
	if err != nil {
		t.Fatalf("read row %d: %v", id, err)
	}
	s.State = State(state)
	return s
}

func TestValidation(t *testing.T) {
	t.Parallel()

	q := &Queue{}
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"claim terminal state", func() error { _, err := q.Claim(ctx, Done, 1, time.Minute, "w"); return err }},
		{"claim unknown state", func() error { _, err := q.Claim(ctx, "bogus", 1, time.Minute, "w"); return err }},
		{"claim zero limit", func() error { _, err := q.Claim(ctx, Pending, 0, time.Minute, "w"); return err }},
		{"claim zero lease", func() error { _, err := q.Claim(ctx, Pending, 1, 0, "w"); return err }},
		{"complete to pending", func() error { return q.Complete(ctx, []Completion{{ID: 1, State: Pending}}) }},
		{"complete unknown state", func() error { return q.Complete(ctx, []Completion{{ID: 1, State: "bogus"}}) }},
		{"retry negative delay", func() error { return q.Retry(ctx, 1, -time.Second, false, "") }},
		{"reset unknown from", func() error { _, err := q.Reset(ctx, "bogus", Pending); return err }},
		{"reset unknown to", func() error { _, err := q.Reset(ctx, Failed, "bogus"); return err }},
		{"remaining unknown state", func() error { _, err := q.Remaining(ctx, "bogus"); return err }},
		{"import empty url", func() error { _, err := q.Import(ctx, []Entry{{Host: "a.example"}}); return err }},
		{"import empty host", func() error { _, err := q.Import(ctx, []Entry{{URL: "https://a.example/"}}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("err = nil, want error")
			}
		})
	}
}

func TestImport(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)

	steps := []struct {
		name    string
		entries []Entry
		want    int64
	}{
		{"repeat within batch", entries("a", "b", "a"), 2},
		{"overlaps earlier batch", entries("b", "c"), 1},
		{"only duplicates", entries("a", "c"), 0},
		{"empty", nil, 0},
	}
	for _, s := range steps {
		got, err := q.Import(context.Background(), s.entries)
		if err != nil {
			t.Fatalf("Import(%s): %v", s.name, err)
		}
		if got != s.want {
			t.Errorf("Import(%s) = %d, want %d", s.name, got, s.want)
		}
	}

	items := mustClaim(t, q, Pending, 10, time.Hour)
	if got, want := urls(items), []string{"https://a.example/", "https://b.example/", "https://c.example/"}; !slices.Equal(got, want) {
		t.Fatalf("claimed %v, want %v in import order", got, want)
	}
	for _, it := range items {
		if want := it.URL[len("https://") : len(it.URL)-1]; it.Host != want || it.Attempts != 0 {
			t.Errorf("item %+v, want host %q and 0 attempts", it, want)
		}
	}
}

func TestImportLargeBatch(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)

	batch := make([]Entry, 10_000)
	for i := range batch {
		batch[i] = Entry{URL: fmt.Sprintf("https://bulk.example/%d", i), Host: "bulk.example"}
	}
	got, err := q.Import(context.Background(), batch)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if want := int64(len(batch)); got != want {
		t.Fatalf("Import = %d, want %d", got, want)
	}
	if got, want := mustCounts(t, q), []Count{{State: Pending, Ready: 10_000}}; !slices.Equal(got, want) {
		t.Fatalf("Counts = %+v, want %+v", got, want)
	}
}

func TestClaimConcurrentReturnsEachRowOnce(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)

	const rows, workers = 1000, 8
	names := make([]string, rows)
	for i := range names {
		names[i] = fmt.Sprintf("u%d", i)
	}
	mustImport(t, q, names...)

	var (
		mu   sync.Mutex
		seen = make(map[int64]int, rows)
		wg   sync.WaitGroup
	)
	errs := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				items, err := q.Claim(context.Background(), Pending, 17, time.Hour, fmt.Sprintf("worker-%d", w))
				if err != nil {
					errs <- err
					return
				}
				if len(items) == 0 {
					return
				}
				mu.Lock()
				for _, it := range items {
					seen[it.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Claim: %v", err)
	}

	if len(seen) != rows {
		t.Errorf("claimed %d distinct rows, want %d", len(seen), rows)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("row %d claimed %d times, want 1", id, n)
		}
	}
	if items := mustClaim(t, q, Pending, rows, time.Hour); len(items) != 0 {
		t.Errorf("Claim after draining returned %d rows, want 0", len(items))
	}
}

func TestClaimLeaseExpires(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	mustImport(t, q, "a", "b", "c")

	const lease = 750 * time.Millisecond
	if got := mustClaim(t, q, Pending, 10, lease); len(got) != 3 {
		t.Fatalf("first Claim returned %d rows, want 3", len(got))
	}
	if got := mustClaim(t, q, Pending, 10, lease); len(got) != 0 {
		t.Fatalf("Claim during lease returned %d rows, want 0", len(got))
	}

	time.Sleep(lease + 250*time.Millisecond)
	got := mustClaim(t, q, Pending, 10, time.Hour)
	if len(got) != 3 {
		t.Fatalf("Claim after lease returned %d rows, want 3", len(got))
	}
	for _, it := range got {
		if it.Attempts != 0 {
			t.Errorf("%s attempts = %d, want 0: an expired lease is not an attempt", it.URL, it.Attempts)
		}
	}
}

func TestComplete(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	mustImport(t, q, "ok", "gone", "broken", "js", "shell")
	items := mustClaim(t, q, Pending, 10, time.Hour)
	if len(items) != 5 {
		t.Fatalf("Claim returned %d rows, want 5", len(items))
	}
	ok, gone, broken, js, shell := items[0].ID, items[1].ID, items[2].ID, items[3].ID, items[4].ID

	mustComplete(t, q,
		Completion{ID: ok, State: Done, Outcome: "ok", Provider: "http", StatusCode: 200, ContentRef: "out-1.jsonl:7", CountAttempt: true},
		Completion{ID: gone, State: Dead, Outcome: "dead", Provider: "http", StatusCode: 404, Detail: "not found", CountAttempt: true},
		Completion{ID: broken, State: Failed, Outcome: "failed", Detail: "too many redirects", CountAttempt: true},
		Completion{ID: js, State: NeedsBrowser, Outcome: "needs_browser", Provider: "http", StatusCode: 200, CountAttempt: true},
		Completion{ID: shell, State: NeedsBrowser, Outcome: "needs_browser", Provider: "http", StatusCode: 200},
	)

	wantRows := map[int64]stored{
		ok:     {State: Done, Outcome: "ok", Provider: "http", StatusCode: 200, ContentRef: "out-1.jsonl:7", Attempts: 1},
		gone:   {State: Dead, Outcome: "dead", Provider: "http", StatusCode: 404, Detail: "not found", Attempts: 1},
		broken: {State: Failed, Outcome: "failed", Detail: "too many redirects", Attempts: 1},
		js:     {State: NeedsBrowser, Outcome: "needs_browser", Provider: "http", StatusCode: 200, Attempts: 1},
		shell:  {State: NeedsBrowser, Outcome: "needs_browser", Provider: "http", StatusCode: 200},
	}
	for id, want := range wantRows {
		if got := storedRow(t, q, id); got != want {
			t.Errorf("row %d = %+v, want %+v", id, got, want)
		}
	}

	if got := mustClaim(t, q, Pending, 10, time.Hour); len(got) != 0 {
		t.Errorf("Claim(Pending) returned %v, want none", urls(got))
	}
	browser := mustClaim(t, q, NeedsBrowser, 10, time.Hour)
	if got, want := urls(browser), []string{"https://js.example/", "https://shell.example/"}; !slices.Equal(got, want) {
		t.Fatalf("Claim(NeedsBrowser) = %v, want %v", got, want)
	}
	if browser[0].Attempts != 1 || browser[1].Attempts != 0 {
		t.Errorf("browser attempts = %d, %d; want 1, 0", browser[0].Attempts, browser[1].Attempts)
	}
}

func TestRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         State
		after         time.Duration
		countAttempt  bool
		wantClaimable bool
		wantAttempts  int
	}{
		{name: "now counting", state: Pending, after: 0, countAttempt: true, wantClaimable: true, wantAttempts: 1},
		{name: "now not counting", state: Pending, after: 0, countAttempt: false, wantClaimable: true, wantAttempts: 0},
		{name: "later", state: Pending, after: time.Hour, countAttempt: true, wantClaimable: false, wantAttempts: 1},
		{name: "browser stage keeps state", state: NeedsBrowser, after: 0, countAttempt: false, wantClaimable: true, wantAttempts: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := newTestQueue(t)
			mustImport(t, q, "a")
			id := mustClaim(t, q, Pending, 1, time.Hour)[0].ID
			if tt.state == NeedsBrowser {
				mustComplete(t, q, Completion{ID: id, State: NeedsBrowser, Outcome: "needs_browser"})
				mustClaim(t, q, NeedsBrowser, 1, time.Hour)
			}

			if err := q.Retry(context.Background(), id, tt.after, tt.countAttempt, "browser saturated"); err != nil {
				t.Fatalf("Retry: %v", err)
			}

			got := storedRow(t, q, id)
			if got.State != tt.state || got.Attempts != tt.wantAttempts || got.Detail != "browser saturated" || got.Leased {
				t.Errorf("row = %+v, want state %s, %d attempts, the retry detail, no lease", got, tt.state, tt.wantAttempts)
			}
			items := mustClaim(t, q, tt.state, 1, time.Hour)
			if claimable := len(items) == 1; claimable != tt.wantClaimable {
				t.Errorf("claimable = %v, want %v", claimable, tt.wantClaimable)
			}
		})
	}

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()
		q := newTestQueue(t)
		if err := q.Retry(context.Background(), 42, 0, false, ""); err == nil {
			t.Fatal("Retry(unknown id) err = nil, want error")
		}
	})
}

func TestReset(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	mustImport(t, q, "a", "b", "c")
	items := mustClaim(t, q, Pending, 10, time.Hour)
	mustComplete(t, q,
		Completion{ID: items[0].ID, State: Failed, Outcome: "failed", Provider: "http", StatusCode: 500, Detail: "boom", CountAttempt: true},
		Completion{ID: items[1].ID, State: Failed, Outcome: "failed", Detail: "boom", CountAttempt: true},
		Completion{ID: items[2].ID, State: Done, Outcome: "ok", ContentRef: "out.jsonl:1", CountAttempt: true},
	)

	n, err := q.Reset(context.Background(), Failed, Pending)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if n != 2 {
		t.Fatalf("Reset = %d, want 2", n)
	}
	if got, want := storedRow(t, q, items[0].ID), (stored{State: Pending}); got != want {
		t.Errorf("reset row = %+v, want %+v", got, want)
	}

	again := mustClaim(t, q, Pending, 10, time.Hour)
	if got, want := urls(again), []string{"https://a.example/", "https://b.example/"}; !slices.Equal(got, want) {
		t.Fatalf("Claim after Reset = %v, want %v", got, want)
	}
	if got, want := mustCounts(t, q), []Count{{State: Done, Ready: 1}, {State: Pending, Waiting: 2}}; !slices.Equal(got, want) {
		t.Errorf("Counts = %+v, want %+v", got, want)
	}
}

func TestRemaining(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)

	check := func(step string, wantPending, wantBrowser bool) {
		t.Helper()
		for _, c := range []struct {
			state State
			want  bool
		}{{Pending, wantPending}, {NeedsBrowser, wantBrowser}} {
			got, err := q.Remaining(context.Background(), c.state)
			if err != nil {
				t.Fatalf("%s: Remaining(%s): %v", step, c.state, err)
			}
			if got != c.want {
				t.Errorf("%s: Remaining(%s) = %v, want %v", step, c.state, got, c.want)
			}
		}
	}

	check("empty", false, false)
	mustImport(t, q, "a", "b")
	check("imported", true, false)
	items := mustClaim(t, q, Pending, 10, time.Hour)
	check("leased", true, false)
	mustComplete(t, q,
		Completion{ID: items[0].ID, State: NeedsBrowser, Outcome: "needs_browser"},
		Completion{ID: items[1].ID, State: Done, Outcome: "ok"},
	)
	check("http stage done", false, true)
	mustClaim(t, q, NeedsBrowser, 10, time.Hour)
	check("browser leased", false, true)
	mustComplete(t, q, Completion{ID: items[0].ID, State: Dead, Outcome: "dead"})
	check("all terminal", false, false)
}

func TestCounts(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)

	if got := mustCounts(t, q); len(got) != 0 {
		t.Fatalf("Counts on empty queue = %+v, want none", got)
	}
	mustImport(t, q, "a", "b", "c", "d")
	items := mustClaim(t, q, Pending, 2, time.Hour)
	if got, want := mustCounts(t, q), []Count{{State: Pending, Ready: 2, Waiting: 2}}; !slices.Equal(got, want) {
		t.Fatalf("Counts after claim = %+v, want %+v", got, want)
	}

	mustComplete(t, q, Completion{ID: items[0].ID, State: Done, Outcome: "ok"})
	if err := q.Retry(context.Background(), items[1].ID, time.Hour, false, "backoff"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got, want := mustCounts(t, q), []Count{{State: Done, Ready: 1}, {State: Pending, Ready: 2, Waiting: 1}}; !slices.Equal(got, want) {
		t.Fatalf("Counts after complete and retry = %+v, want %+v", got, want)
	}
}

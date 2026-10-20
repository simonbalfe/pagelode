package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/simonbalfe/pagelode/pkg/batch"
	"github.com/simonbalfe/pagelode/pkg/emails"
	"github.com/simonbalfe/pagelode/pkg/fetch"
)

func testSchema(t *testing.T) (string, string) {
	t.Helper()
	url := os.Getenv("PAGELODE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PAGELODE_TEST_DATABASE_URL not set")
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	return url, "pagelode_test_" + hex.EncodeToString(suffix)
}

func openTest(t *testing.T, url, schemaName string) *Store {
	t.Helper()
	store, err := open(context.Background(), url, schemaName)
	if err != nil {
		t.Fatalf("open(%s) error = %v", schemaName, err)
	}
	return store
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	url, schemaName := testSchema(t)
	store := openTest(t, url, schemaName)
	t.Cleanup(func() { dropSchema(t, store, schemaName) })
	return store
}

func dropSchema(t *testing.T, store *Store, schemaName string) {
	t.Helper()
	defer store.Close()
	if _, err := store.pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE"); err != nil {
		t.Errorf("drop schema %s: %v", schemaName, err)
	}
}

func TestStateSurvivesReopen(t *testing.T) {
	t.Parallel()
	url, schemaName := testSchema(t)
	session := fetch.Session{
		UserAgent: "Chrome/155",
		Cookies:   []fetch.Cookie{{Name: "cf_clearance", Value: "token", Domain: "example.com"}},
		ProxyURL:  "http://user-abc:secret@proxy.example:1000",
		Headers:   map[string]string{"accept-language": "en-GB"},
	}

	first := openTest(t, url, schemaName)
	routes := fetch.NewRoutes(time.Hour, nil)
	clearances := fetch.NewClearances(time.Hour)
	restore(t, first, routes, clearances)
	routes.Record("Example.com", fetch.ProviderPatchright)
	clearances.Record("example.com", session)
	clearances.Record("forgotten.example", session)
	clearances.Forget("forgotten.example")
	if err := first.SaveGeneration("pl-identity", 3); err != nil {
		t.Fatalf("SaveGeneration() error = %v", err)
	}
	first.Close()

	second := openTest(t, url, schemaName)
	t.Cleanup(func() { dropSchema(t, second, schemaName) })
	routes = fetch.NewRoutes(time.Hour, nil)
	clearances = fetch.NewClearances(time.Hour)
	restore(t, second, routes, clearances)

	if got, ok := routes.Preferred("example.com"); !ok || got != fetch.ProviderPatchright {
		t.Errorf("Preferred(example.com) after reopen = %q, %v; want patchright, true", got, ok)
	}
	got, ok := clearances.Get("example.com")
	if !ok || got.ProxyURL != session.ProxyURL || got.Headers["accept-language"] != "en-GB" || len(got.Cookies) != 1 || got.UserAgent != session.UserAgent {
		t.Errorf("Get(example.com) after reopen = %+v, %v; want %+v, true", got, ok, session)
	}
	if _, ok := clearances.Get("forgotten.example"); ok {
		t.Error("Get(forgotten.example) after reopen ok = true; want forgotten clearance to stay deleted")
	}
	if generation, err := second.Generation("pl-identity"); err != nil || generation != 3 {
		t.Errorf("Generation(pl-identity) = %d, %v; want 3, nil", generation, err)
	}
	if generation, err := second.Generation("pl-unknown"); err != nil || generation != 0 {
		t.Errorf("Generation(pl-unknown) = %d, %v; want 0, nil", generation, err)
	}
}

func TestLoadDropsExpiredEntries(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	past := time.Now().Add(-time.Minute)
	if err := store.SaveRoute(fetch.Route{Host: "old.example", Provider: fetch.ProviderPatchright, Expires: past}); err != nil {
		t.Fatalf("SaveRoute() error = %v", err)
	}
	if err := store.SaveClearance(fetch.Clearance{Host: "old.example", Expires: past}); err != nil {
		t.Fatalf("SaveClearance() error = %v", err)
	}
	routes, err := store.LoadRoutes(time.Now())
	if err != nil || len(routes) != 0 {
		t.Errorf("LoadRoutes() = %v, %v; want no expired routes", routes, err)
	}
	clearances, err := store.LoadClearances(time.Now())
	if err != nil || len(clearances) != 0 {
		t.Errorf("LoadClearances() = %v, %v; want no expired clearances", clearances, err)
	}
}

func restore(t *testing.T, store *Store, routes *fetch.Routes, clearances *fetch.Clearances) {
	t.Helper()
	if err := routes.Restore(store); err != nil {
		t.Fatalf("routes.Restore() error = %v", err)
	}
	if err := clearances.Restore(store); err != nil {
		t.Fatalf("clearances.Restore() error = %v", err)
	}
}

func report(addresses ...string) emails.Report {
	found := make([]emails.Address, 0, len(addresses))
	for _, address := range addresses {
		found = append(found, emails.Address{Address: address, Sources: []emails.Source{
			{URL: "https://example.com/", FoundIn: "footer"},
			{URL: "https://example.com/contact", FoundIn: "mailto"},
		}})
	}
	return emails.Report{Outcome: "ok", Emails: found, Summary: emails.Summary{PagesVisited: 2, DurationMS: 120}}
}

func count(t *testing.T, store *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSaveEmailReportReplacesPreviousResultsAndMarksSitesDone(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SaveEmailReport(ctx, "https://example.com/", report("a@example.com", "b@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, store, `SELECT count(*) FROM email_addresses WHERE site_url = $1`, "https://example.com/"); got != 4 {
		t.Fatalf("first save stored %d email rows, want 4 (two addresses with two sources)", got)
	}
	if err := store.SaveEmailReport(ctx, "https://example.com/", report("c@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, store, `SELECT count(*) FROM email_addresses WHERE address = 'a@example.com'`); got != 0 {
		t.Fatalf("resave kept %d rows for a replaced address, want 0", got)
	}
	if got := count(t, store, `SELECT emails_found FROM email_sites WHERE url = $1`, "https://example.com/"); got != 1 {
		t.Fatalf("emails_found = %d, want 1", got)
	}

	if err := store.SaveEmailFailure(ctx, "https://down.example/", errors.New("queue is full")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, store, `SELECT count(*) FROM email_sites WHERE url = $1 AND outcome = 'error' AND error = 'queue is full'`, "https://down.example/"); got != 1 {
		t.Fatalf("failure rows = %d, want 1", got)
	}
	done, err := store.FinishedEmailSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !done["https://example.com/"] || !done["https://down.example/"] || len(done) != 2 {
		t.Fatalf("FinishedEmailSites() = %v, want both saved sites", done)
	}
}

func TestBatchJobQueriesReadRiverJobs(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	schemaName := store.pool.Config().ConnConfig.RuntimeParams["search_path"]
	driver := riverpgxv5.New(store.pool)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: schemaName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	client, err := river.NewClient(driver, &river.Config{Schema: schemaName})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(args river.JobArgs) int64 {
		t.Helper()
		result, err := client.Insert(ctx, args, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Job.ID
	}
	insert(batch.HTTPArgs{URL: "https://a.example/", Host: "a.example"})
	dead := insert(batch.HTTPArgs{URL: "https://b.example/", Host: "b.example"})
	failed := insert(batch.BrowserArgs{URL: "https://c.example/", Host: "c.example"})
	if _, err := client.JobCancel(ctx, dead); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE river_job SET state = 'discarded', finalized_at = now() WHERE id = $1`, failed); err != nil {
		t.Fatal(err)
	}

	rows, err := store.BatchJobCounts(ctx, batch.Kinds())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, row := range rows {
		got[row.Kind+" "+row.State] = row.Jobs
	}
	want := map[string]int64{batch.HTTPKind + " available": 1, batch.HTTPKind + " cancelled": 1, batch.BrowserKind + " discarded": 1}
	if !maps.Equal(got, want) {
		t.Errorf("BatchJobCounts() = %v, want %v", got, want)
	}
	for state, wantIDs := range map[string][]int64{"discarded": {failed}, "cancelled": {dead}, "completed": nil} {
		ids, err := store.BatchJobIDs(ctx, batch.Kinds(), state)
		if err != nil || !slices.Equal(ids, wantIDs) {
			t.Errorf("BatchJobIDs(%s) = %v, %v; want %v", state, ids, err, wantIDs)
		}
	}
}

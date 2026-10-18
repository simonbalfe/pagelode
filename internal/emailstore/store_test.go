package emailstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/simonbalfe/pagelode/internal/emails"
)

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
	if err := store.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSaveReplacesPreviousResultsAndMarksSitesDone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "nested", "emails.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.Save(ctx, "https://example.com/", report("a@example.com", "b@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, store, `SELECT count(*) FROM emails WHERE site_url = ?`, "https://example.com/"); got != 4 {
		t.Fatalf("first save stored %d email rows, want 4 (two addresses with two sources)", got)
	}
	if err := store.Save(ctx, "https://example.com/", report("c@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, store, `SELECT count(*) FROM emails WHERE address = 'a@example.com'`); got != 0 {
		t.Fatalf("resave kept %d rows for a replaced address, want 0", got)
	}
	if got := count(t, store, `SELECT emails_found FROM sites WHERE url = ?`, "https://example.com/"); got != 1 {
		t.Fatalf("emails_found = %d, want 1", got)
	}

	if err := store.SaveFailure(ctx, "https://down.example/", errors.New("queue is full")); err != nil {
		t.Fatal(err)
	}
	done, err := store.Done(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !done["https://example.com/"] || !done["https://down.example/"] || len(done) != 2 {
		t.Fatalf("Done() = %v, want both saved sites", done)
	}
}

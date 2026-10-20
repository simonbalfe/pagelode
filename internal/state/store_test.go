package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/memory"
	"github.com/simonbalfe/pagelode/page"
)

func TestStateSurvivesReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	session := page.Session{
		UserAgent: "Chrome/155",
		Cookies:   []page.Cookie{{Name: "cf_clearance", Value: "token", Domain: "example.com"}},
		ProxyURL:  "http://user-abc:secret@proxy.example:1000",
		Headers:   map[string]string{"accept-language": "en-GB"},
	}

	first := open(t, path)
	routes := memory.NewRoutes(time.Hour, nil)
	clearances := memory.NewClearances(time.Hour)
	restore(t, first, routes, clearances)
	routes.Record("Example.com", page.ProviderPatchright)
	clearances.Record("example.com", session)
	clearances.Record("forgotten.example", session)
	clearances.Forget("forgotten.example")
	if err := first.SaveGeneration("pl-identity", 3); err != nil {
		t.Fatalf("SaveGeneration() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second := open(t, path)
	t.Cleanup(func() { second.Close() })
	routes = memory.NewRoutes(time.Hour, nil)
	clearances = memory.NewClearances(time.Hour)
	restore(t, second, routes, clearances)

	if got, ok := routes.Preferred("example.com"); !ok || got != page.ProviderPatchright {
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
}

func TestLoadDropsExpiredEntries(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	t.Cleanup(func() { store.Close() })
	past := time.Now().Add(-time.Minute)
	if err := store.SaveRoute(memory.Route{Host: "old.example", Provider: page.ProviderPatchright, Expires: past}); err != nil {
		t.Fatalf("SaveRoute() error = %v", err)
	}
	routes, err := store.LoadRoutes(time.Now())
	if err != nil || len(routes) != 0 {
		t.Errorf("LoadRoutes() = %v, %v; want no expired routes", routes, err)
	}
}

func open(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	return store
}

func restore(t *testing.T, store *Store, routes *memory.Routes, clearances *memory.Clearances) {
	t.Helper()
	if err := routes.Restore(store); err != nil {
		t.Fatalf("routes.Restore() error = %v", err)
	}
	if err := clearances.Restore(store); err != nil {
		t.Fatalf("clearances.Restore() error = %v", err)
	}
}

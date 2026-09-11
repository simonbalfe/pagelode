package memory

import (
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestRoutes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	routes := NewRoutes(time.Minute, []string{"crunchbase.com"})
	routes.now = func() time.Time { return now }

	if got, ok := routes.Preferred("www.crunchbase.com"); !ok || got != page.ProviderPatchright {
		t.Fatalf("Preferred(protected) = %q, %v; want patchright, true", got, ok)
	}
	routes.Record("example.com", page.ProviderRod)
	if got, ok := routes.Preferred("example.com"); !ok || got != page.ProviderRod {
		t.Fatalf("Preferred(learned) = %q, %v; want rod, true", got, ok)
	}

	now = now.Add(2 * time.Minute)
	if _, ok := routes.Preferred("example.com"); ok {
		t.Fatal("Preferred(expired) ok = true, want false")
	}
}

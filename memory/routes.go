// Package memory remembers per-host fetch outcomes in process: which provider
// last succeeded (Routes) and which challenge-cleared sessions can be
// replayed (Clearances). Entries expire after a TTL. Callers that want them to
// survive restarts implement RouteStore and ClearanceStore and pass them to
// Restore; persistence failures are logged, never returned from writes.
package memory

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/page"
)

// Route is a persisted provider preference for Host, valid until Expires.
type Route struct {
	Host     string
	Provider page.Provider
	Expires  time.Time
}

// RouteStore persists routes. LoadRoutes should return only routes still
// valid at now; SaveRoute upserts by host.
type RouteStore interface {
	LoadRoutes(now time.Time) ([]Route, error)
	SaveRoute(Route) error
}

type entry struct {
	provider page.Provider
	expires  time.Time
}

// Routes caches the preferred provider per host. Hosts matching a protected
// suffix always prefer the Patchright browser. It is safe for concurrent use.
type Routes struct {
	mu        sync.Mutex
	ttl       time.Duration
	protected []string
	entries   map[string]entry
	store     RouteStore
	now       func() time.Time
}

// NewRoutes returns in-memory Routes whose entries live for ttl, forcing the
// browser for hosts equal to or under any protected domain suffix.
func NewRoutes(ttl time.Duration, protected []string) *Routes {
	return &Routes{
		ttl:       ttl,
		protected: append([]string(nil), protected...),
		entries:   make(map[string]entry),
		now:       time.Now,
	}
}

// Restore loads unexpired routes from store and writes later records through
// to it.
func (r *Routes) Restore(store RouteStore) error {
	routes, err := store.LoadRoutes(r.now())
	if err != nil {
		return fmt.Errorf("restore routes: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, route := range routes {
		r.entries[normalizeHost(route.Host)] = entry{provider: route.Provider, expires: route.Expires}
	}
	r.store = store
	return nil
}

// Preferred returns the provider to try first for host, if known and
// unexpired.
func (r *Routes) Preferred(host string) (page.Provider, bool) {
	host = normalizeHost(host)
	for _, suffix := range r.protected {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return page.ProviderPatchright, true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.entries[host]
	if !ok {
		return "", false
	}
	if r.now().After(value.expires) {
		delete(r.entries, host)
		return "", false
	}
	return value.provider, true
}

// Record remembers provider as the preference for host and writes it through
// to the store. ProviderTLS is never recorded.
func (r *Routes) Record(host string, provider page.Provider) {
	if provider == page.ProviderTLS {
		return
	}
	route := Route{Host: normalizeHost(host), Provider: provider, Expires: r.now().Add(r.ttl)}
	r.mu.Lock()
	r.entries[route.Host] = entry{provider: route.Provider, expires: route.Expires}
	store := r.store
	r.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.SaveRoute(route); err != nil {
		slog.Warn("route not persisted", "host", route.Host, "error", err)
	}
}

// Size drops expired entries and returns the number of live routes.
func (r *Routes) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for host, value := range r.entries {
		if now.After(value.expires) {
			delete(r.entries, host)
		}
	}
	return len(r.entries)
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

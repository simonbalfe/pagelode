package fetch

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Route struct {
	Host     string
	Provider Provider
	Expires  time.Time
}

type RouteStore interface {
	LoadRoutes(now time.Time) ([]Route, error)
	SaveRoute(Route) error
}

type entry struct {
	provider Provider
	expires  time.Time
}

type Routes struct {
	mu        sync.Mutex
	ttl       time.Duration
	protected []string
	entries   map[string]entry
	store     RouteStore
	now       func() time.Time
}

func NewRoutes(ttl time.Duration, protected []string) *Routes {
	return &Routes{
		ttl:       ttl,
		protected: append([]string(nil), protected...),
		entries:   make(map[string]entry),
		now:       time.Now,
	}
}

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

func (r *Routes) Preferred(host string) (Provider, bool) {
	host = normalizeHost(host)
	for _, suffix := range r.protected {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return ProviderPatchright, true
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

func (r *Routes) Record(host string, provider Provider) {
	if provider == ProviderTLS {
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

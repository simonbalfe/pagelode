package memory

import (
	"strings"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

type entry struct {
	provider page.Provider
	expires  time.Time
}

type Routes struct {
	mu        sync.Mutex
	ttl       time.Duration
	protected []string
	entries   map[string]entry
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

func (r *Routes) Record(host string, provider page.Provider) {
	if provider == page.ProviderTLS {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[normalizeHost(host)] = entry{provider: provider, expires: r.now().Add(r.ttl)}
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

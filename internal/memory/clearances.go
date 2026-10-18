package memory

import (
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

type clearance struct {
	session page.Session
	expires time.Time
}

type Clearances struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]clearance
	now     func() time.Time
}

func NewClearances(ttl time.Duration) *Clearances {
	return &Clearances{ttl: ttl, entries: make(map[string]clearance), now: time.Now}
}

func (c *Clearances) Get(host string) (page.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[normalizeHost(host)]
	if !ok {
		return page.Session{}, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, normalizeHost(host))
		return page.Session{}, false
	}
	return entry.session, true
}

func (c *Clearances) Record(host string, session page.Session) {
	if !session.Replayable() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[normalizeHost(host)] = clearance{session: session, expires: c.now().Add(c.ttl)}
}

func (c *Clearances) Forget(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, normalizeHost(host))
}

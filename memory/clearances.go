package memory

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/page"
)

// Clearance is a persisted replayable session for Host, valid until Expires.
type Clearance struct {
	Host    string
	Session page.Session
	Expires time.Time
}

// ClearanceStore persists clearances. LoadClearances should return only
// clearances still valid at now; SaveClearance upserts by host.
type ClearanceStore interface {
	LoadClearances(now time.Time) ([]Clearance, error)
	SaveClearance(Clearance) error
	DeleteClearance(host string) error
}

type clearance struct {
	session page.Session
	expires time.Time
}

// Clearances caches replayable challenge-cleared sessions per host. It is
// safe for concurrent use.
type Clearances struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]clearance
	store   ClearanceStore
	now     func() time.Time
}

// NewClearances returns in-memory Clearances whose entries live for ttl.
func NewClearances(ttl time.Duration) *Clearances {
	return &Clearances{ttl: ttl, entries: make(map[string]clearance), now: time.Now}
}

// Restore loads unexpired clearances from store and writes later changes
// through to it.
func (c *Clearances) Restore(store ClearanceStore) error {
	clearances, err := store.LoadClearances(c.now())
	if err != nil {
		return fmt.Errorf("restore clearances: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stored := range clearances {
		c.entries[normalizeHost(stored.Host)] = clearance{session: stored.Session, expires: stored.Expires}
	}
	c.store = store
	return nil
}

// Get returns the unexpired session cleared for host.
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

// Record stores session for host if it is replayable; others are ignored.
func (c *Clearances) Record(host string, session page.Session) {
	if !session.Replayable() {
		return
	}
	stored := Clearance{Host: normalizeHost(host), Session: session, Expires: c.now().Add(c.ttl)}
	c.mu.Lock()
	c.entries[stored.Host] = clearance{session: stored.Session, expires: stored.Expires}
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.SaveClearance(stored); err != nil {
		slog.Warn("clearance not persisted", "host", stored.Host, "error", err)
	}
}

// Forget removes host's clearance, for example after it stops working.
func (c *Clearances) Forget(host string) {
	host = normalizeHost(host)
	c.mu.Lock()
	delete(c.entries, host)
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.DeleteClearance(host); err != nil {
		slog.Warn("clearance deletion not persisted", "host", host, "error", err)
	}
}

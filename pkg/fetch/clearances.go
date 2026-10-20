package fetch

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Clearance struct {
	Host    string
	Session Session
	Expires time.Time
}

type ClearanceStore interface {
	LoadClearances(now time.Time) ([]Clearance, error)
	SaveClearance(Clearance) error
	DeleteClearance(host string) error
}

type clearance struct {
	session Session
	expires time.Time
}

type Clearances struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]clearance
	store   ClearanceStore
	now     func() time.Time
}

func NewClearances(ttl time.Duration) *Clearances {
	return &Clearances{ttl: ttl, entries: make(map[string]clearance), now: time.Now}
}

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

func (c *Clearances) Get(host string) (Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[normalizeHost(host)]
	if !ok {
		return Session{}, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, normalizeHost(host))
		return Session{}, false
	}
	return entry.session, true
}

func (c *Clearances) Record(host string, session Session) {
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

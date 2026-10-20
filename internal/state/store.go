// Package state persists learned routes, challenge clearances, and browser identity generations in SQLite across restarts.
package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/simonbalfe/pagelode/memory"
	"github.com/simonbalfe/pagelode/page"
)

const schema = `
CREATE TABLE IF NOT EXISTS routes (
	host TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	expires_ms INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS clearances (
	host TEXT PRIMARY KEY,
	session TEXT NOT NULL,
	expires_ms INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS identities (
	name TEXT PRIMARY KEY,
	generation INTEGER NOT NULL
) STRICT;
`

type Store struct {
	db *sql.DB
}

type storedSession struct {
	UserAgent string            `json:"user_agent,omitempty"`
	Cookies   []page.Cookie     `json:"cookies,omitempty"`
	ProxyURL  string            `json:"proxy_url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("state: create directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("state: open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("state: create schema: %w", err), db.Close())
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("state: restrict permissions: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) LoadRoutes(now time.Time) ([]memory.Route, error) {
	if _, err := s.db.Exec(`DELETE FROM routes WHERE expires_ms <= ?`, now.UnixMilli()); err != nil {
		return nil, fmt.Errorf("state: prune routes: %w", err)
	}
	rows, err := s.db.Query(`SELECT host, provider, expires_ms FROM routes`)
	if err != nil {
		return nil, fmt.Errorf("state: load routes: %w", err)
	}
	defer rows.Close()
	var routes []memory.Route
	for rows.Next() {
		var (
			route   memory.Route
			expires int64
		)
		if err := rows.Scan(&route.Host, &route.Provider, &expires); err != nil {
			return nil, fmt.Errorf("state: read route: %w", err)
		}
		route.Expires = time.UnixMilli(expires)
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: load routes: %w", err)
	}
	return routes, nil
}

func (s *Store) SaveRoute(route memory.Route) error {
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO routes (host, provider, expires_ms) VALUES (?, ?, ?)`, route.Host, string(route.Provider), route.Expires.UnixMilli()); err != nil {
		return fmt.Errorf("state: save route: %w", err)
	}
	return nil
}

func (s *Store) LoadClearances(now time.Time) ([]memory.Clearance, error) {
	if _, err := s.db.Exec(`DELETE FROM clearances WHERE expires_ms <= ?`, now.UnixMilli()); err != nil {
		return nil, fmt.Errorf("state: prune clearances: %w", err)
	}
	rows, err := s.db.Query(`SELECT host, session, expires_ms FROM clearances`)
	if err != nil {
		return nil, fmt.Errorf("state: load clearances: %w", err)
	}
	defer rows.Close()
	var clearances []memory.Clearance
	for rows.Next() {
		var (
			host    string
			encoded string
			expires int64
			stored  storedSession
		)
		if err := rows.Scan(&host, &encoded, &expires); err != nil {
			return nil, fmt.Errorf("state: read clearance: %w", err)
		}
		if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
			return nil, fmt.Errorf("state: decode clearance for %s: %w", host, err)
		}
		clearances = append(clearances, memory.Clearance{
			Host:    host,
			Session: page.Session{UserAgent: stored.UserAgent, Cookies: stored.Cookies, ProxyURL: stored.ProxyURL, Headers: stored.Headers},
			Expires: time.UnixMilli(expires),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: load clearances: %w", err)
	}
	return clearances, nil
}

func (s *Store) SaveClearance(clearance memory.Clearance) error {
	session := clearance.Session
	encoded, err := json.Marshal(storedSession{UserAgent: session.UserAgent, Cookies: session.Cookies, ProxyURL: session.ProxyURL, Headers: session.Headers})
	if err != nil {
		return fmt.Errorf("state: encode clearance: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO clearances (host, session, expires_ms) VALUES (?, ?, ?)`, clearance.Host, string(encoded), clearance.Expires.UnixMilli()); err != nil {
		return fmt.Errorf("state: save clearance: %w", err)
	}
	return nil
}

func (s *Store) DeleteClearance(host string) error {
	if _, err := s.db.Exec(`DELETE FROM clearances WHERE host = ?`, host); err != nil {
		return fmt.Errorf("state: delete clearance: %w", err)
	}
	return nil
}

func (s *Store) Generation(name string) (int, error) {
	var generation int
	err := s.db.QueryRow(`SELECT generation FROM identities WHERE name = ?`, name).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("state: load identity generation: %w", err)
	}
	return generation, nil
}

func (s *Store) SaveGeneration(name string, generation int) error {
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO identities (name, generation) VALUES (?, ?)`, name, generation); err != nil {
		return fmt.Errorf("state: save identity generation: %w", err)
	}
	return nil
}

package emailstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/simonbalfe/pagelode/pkg/emails"
)

const schema = `
CREATE TABLE IF NOT EXISTS sites (
	url TEXT PRIMARY KEY,
	outcome TEXT NOT NULL,
	error TEXT,
	pages_visited INTEGER NOT NULL,
	pages_failed INTEGER NOT NULL,
	emails_found INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	limited INTEGER NOT NULL,
	scraped_at TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS emails (
	site_url TEXT NOT NULL REFERENCES sites(url),
	address TEXT NOT NULL,
	source_url TEXT NOT NULL,
	found_in TEXT NOT NULL,
	PRIMARY KEY (site_url, address, source_url)
) STRICT;
CREATE INDEX IF NOT EXISTS emails_address_idx ON emails(address);
`

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open email database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create email schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Done(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url FROM sites`)
	if err != nil {
		return nil, fmt.Errorf("read finished sites: %w", err)
	}
	defer rows.Close()
	done := map[string]bool{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, fmt.Errorf("read finished site: %w", err)
		}
		done[url] = true
	}
	return done, rows.Err()
}

func (s *Store) Save(ctx context.Context, site string, report emails.Report) error {
	return s.save(ctx, site, report, "")
}

func (s *Store) SaveFailure(ctx context.Context, site string, cause error) error {
	return s.save(ctx, site, emails.Report{Outcome: "error"}, cause.Error())
}

func (s *Store) save(ctx context.Context, site string, report emails.Report, failure string) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer transaction.Rollback()
	var failureValue any
	if failure != "" {
		failureValue = failure
	}
	limited := 0
	if report.Summary.Limited {
		limited = 1
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT OR REPLACE INTO sites (url, outcome, error, pages_visited, pages_failed, emails_found, duration_ms, limited, scraped_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		site, report.Outcome, failureValue, report.Summary.PagesVisited, report.Summary.PagesFailed, len(report.Emails), report.Summary.DurationMS, limited, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("save site: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM emails WHERE site_url = ?`, site); err != nil {
		return fmt.Errorf("replace emails: %w", err)
	}
	for _, address := range report.Emails {
		for _, source := range address.Sources {
			if _, err := transaction.ExecContext(ctx,
				`INSERT OR IGNORE INTO emails (site_url, address, source_url, found_in) VALUES (?, ?, ?, ?)`,
				site, address.Address, source.URL, source.FoundIn,
			); err != nil {
				return fmt.Errorf("save email: %w", err)
			}
		}
	}
	return transaction.Commit()
}

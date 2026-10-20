package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/simonbalfe/pagelode/pkg/emails"
	"github.com/simonbalfe/pagelode/pkg/fetch"
)

//go:embed schema.sql
var schema string

type Store struct {
	pool    *pgxpool.Pool
	queries *Queries
}

type storedSession struct {
	UserAgent string            `json:"user_agent,omitempty"`
	Cookies   []fetch.Cookie    `json:"cookies,omitempty"`
	ProxyURL  string            `json:"proxy_url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

func Open(ctx context.Context, url string) (*Store, error) {
	return open(ctx, url, "pagelode")
}

func open(ctx context.Context, url, schemaName string) (*Store, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse database URL: %w", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("store: open database: %w", err)
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schemaName}.Sanitize()+";\n"+schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}
	return &Store{pool: pool, queries: New(pool)}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) LoadRoutes(now time.Time) ([]fetch.Route, error) {
	ctx := context.Background()
	if err := s.queries.DeleteExpiredRoutes(ctx, now); err != nil {
		return nil, fmt.Errorf("store: prune routes: %w", err)
	}
	rows, err := s.queries.ListRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: load routes: %w", err)
	}
	routes := make([]fetch.Route, 0, len(rows))
	for _, row := range rows {
		routes = append(routes, fetch.Route{Host: row.Host, Provider: fetch.Provider(row.Provider), Expires: row.ExpiresAt})
	}
	return routes, nil
}

func (s *Store) SaveRoute(route fetch.Route) error {
	if err := s.queries.UpsertRoute(context.Background(), UpsertRouteParams{Host: route.Host, Provider: string(route.Provider), ExpiresAt: route.Expires}); err != nil {
		return fmt.Errorf("store: save route: %w", err)
	}
	return nil
}

func (s *Store) LoadClearances(now time.Time) ([]fetch.Clearance, error) {
	ctx := context.Background()
	if err := s.queries.DeleteExpiredClearances(ctx, now); err != nil {
		return nil, fmt.Errorf("store: prune clearances: %w", err)
	}
	rows, err := s.queries.ListClearances(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: load clearances: %w", err)
	}
	clearances := make([]fetch.Clearance, 0, len(rows))
	for _, row := range rows {
		var stored storedSession
		if err := json.Unmarshal(row.Session, &stored); err != nil {
			return nil, fmt.Errorf("store: decode clearance for %s: %w", row.Host, err)
		}
		clearances = append(clearances, fetch.Clearance{
			Host:    row.Host,
			Session: fetch.Session{UserAgent: stored.UserAgent, Cookies: stored.Cookies, ProxyURL: stored.ProxyURL, Headers: stored.Headers},
			Expires: row.ExpiresAt,
		})
	}
	return clearances, nil
}

func (s *Store) SaveClearance(clearance fetch.Clearance) error {
	session := clearance.Session
	encoded, err := json.Marshal(storedSession{UserAgent: session.UserAgent, Cookies: session.Cookies, ProxyURL: session.ProxyURL, Headers: session.Headers})
	if err != nil {
		return fmt.Errorf("store: encode clearance: %w", err)
	}
	if err := s.queries.UpsertClearance(context.Background(), UpsertClearanceParams{Host: clearance.Host, Session: encoded, ExpiresAt: clearance.Expires}); err != nil {
		return fmt.Errorf("store: save clearance: %w", err)
	}
	return nil
}

func (s *Store) DeleteClearance(host string) error {
	if err := s.queries.DeleteClearance(context.Background(), host); err != nil {
		return fmt.Errorf("store: delete clearance: %w", err)
	}
	return nil
}

func (s *Store) Generation(name string) (int, error) {
	generation, err := s.queries.GetGeneration(context.Background(), name)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: load identity generation: %w", err)
	}
	return int(generation), nil
}

func (s *Store) SaveGeneration(name string, generation int) error {
	if err := s.queries.UpsertGeneration(context.Background(), UpsertGenerationParams{Name: name, Generation: int32(generation)}); err != nil {
		return fmt.Errorf("store: save identity generation: %w", err)
	}
	return nil
}

func (s *Store) FinishedEmailSites(ctx context.Context) (map[string]bool, error) {
	urls, err := s.queries.ListEmailSiteURLs(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: read finished sites: %w", err)
	}
	done := make(map[string]bool, len(urls))
	for _, url := range urls {
		done[url] = true
	}
	return done, nil
}

func (s *Store) SaveEmailReport(ctx context.Context, site string, report emails.Report) error {
	return s.saveEmails(ctx, site, report, pgtype.Text{})
}

func (s *Store) SaveEmailFailure(ctx context.Context, site string, cause error) error {
	return s.saveEmails(ctx, site, emails.Report{Outcome: "error"}, pgtype.Text{String: cause.Error(), Valid: true})
}

func (s *Store) saveEmails(ctx context.Context, site string, report emails.Report, failure pgtype.Text) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin email save: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	if err := queries.UpsertEmailSite(ctx, UpsertEmailSiteParams{
		Url:          site,
		Outcome:      report.Outcome,
		Error:        failure,
		PagesVisited: int32(report.Summary.PagesVisited),
		PagesFailed:  int32(report.Summary.PagesFailed),
		EmailsFound:  int32(len(report.Emails)),
		DurationMs:   report.Summary.DurationMS,
		Limited:      report.Summary.Limited,
	}); err != nil {
		return fmt.Errorf("store: save site: %w", err)
	}
	if err := queries.DeleteEmailAddresses(ctx, site); err != nil {
		return fmt.Errorf("store: replace emails: %w", err)
	}
	total := 0
	for _, address := range report.Emails {
		total += len(address.Sources)
	}
	found := InsertEmailAddressesParams{SiteUrl: site, Addresses: make([]string, 0, total), SourceUrls: make([]string, 0, total), FoundIns: make([]string, 0, total)}
	for _, address := range report.Emails {
		for _, source := range address.Sources {
			found.Addresses = append(found.Addresses, address.Address)
			found.SourceUrls = append(found.SourceUrls, source.URL)
			found.FoundIns = append(found.FoundIns, source.FoundIn)
		}
	}
	if len(found.Addresses) > 0 {
		if err := queries.InsertEmailAddresses(ctx, found); err != nil {
			return fmt.Errorf("store: save emails: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit email save: %w", err)
	}
	return nil
}

func (s *Store) BatchJobCounts(ctx context.Context, kinds []string) ([]CountBatchJobsRow, error) {
	rows, err := s.queries.CountBatchJobs(ctx, kinds)
	if err != nil {
		return nil, fmt.Errorf("store: count batch jobs: %w", err)
	}
	return rows, nil
}

func (s *Store) BatchJobIDs(ctx context.Context, kinds []string, state string) ([]int64, error) {
	ids, err := s.queries.ListBatchJobIDs(ctx, ListBatchJobIDsParams{Kinds: kinds, State: state})
	if err != nil {
		return nil, fmt.Errorf("store: list batch jobs: %w", err)
	}
	return ids, nil
}

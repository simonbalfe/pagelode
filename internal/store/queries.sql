-- name: DeleteExpiredRoutes :exec
DELETE FROM routes WHERE expires_at <= $1;

-- name: ListRoutes :many
SELECT host, provider, expires_at FROM routes;

-- name: UpsertRoute :exec
INSERT INTO routes (host, provider, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (host) DO UPDATE SET provider = excluded.provider, expires_at = excluded.expires_at;

-- name: DeleteExpiredClearances :exec
DELETE FROM clearances WHERE expires_at <= $1;

-- name: ListClearances :many
SELECT host, session, expires_at FROM clearances;

-- name: UpsertClearance :exec
INSERT INTO clearances (host, session, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (host) DO UPDATE SET session = excluded.session, expires_at = excluded.expires_at;

-- name: DeleteClearance :exec
DELETE FROM clearances WHERE host = $1;

-- name: GetGeneration :one
SELECT generation FROM generations WHERE name = $1;

-- name: UpsertGeneration :exec
INSERT INTO generations (name, generation) VALUES ($1, $2)
ON CONFLICT (name) DO UPDATE SET generation = excluded.generation;

-- name: ListEmailSiteURLs :many
SELECT url FROM email_sites;

-- name: UpsertEmailSite :exec
INSERT INTO email_sites (url, outcome, error, pages_visited, pages_failed, emails_found, duration_ms, limited, scraped_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (url) DO UPDATE SET
    outcome = excluded.outcome,
    error = excluded.error,
    pages_visited = excluded.pages_visited,
    pages_failed = excluded.pages_failed,
    emails_found = excluded.emails_found,
    duration_ms = excluded.duration_ms,
    limited = excluded.limited,
    scraped_at = excluded.scraped_at;

-- name: DeleteEmailAddresses :exec
DELETE FROM email_addresses WHERE site_url = $1;

-- name: InsertEmailAddresses :exec
INSERT INTO email_addresses (site_url, address, source_url, found_in)
SELECT @site_url::text, unnest(@addresses::text[]), unnest(@source_urls::text[]), unnest(@found_ins::text[])
ON CONFLICT DO NOTHING;

-- name: CountBatchJobs :many
SELECT kind, state::text AS state, count(*) AS jobs
FROM river_job
WHERE kind = ANY(@kinds::text[])
GROUP BY kind, state;

-- name: ListBatchJobIDs :many
SELECT id
FROM river_job
WHERE kind = ANY(@kinds::text[]) AND state = (@state::text)::river_job_state
ORDER BY id;

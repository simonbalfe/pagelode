CREATE TABLE IF NOT EXISTS routes (
    host text PRIMARY KEY,
    provider text NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS clearances (
    host text PRIMARY KEY,
    session jsonb NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS generations (
    name text PRIMARY KEY,
    generation integer NOT NULL
);

CREATE TABLE IF NOT EXISTS email_sites (
    url text PRIMARY KEY,
    outcome text NOT NULL,
    error text,
    pages_visited integer NOT NULL,
    pages_failed integer NOT NULL,
    emails_found integer NOT NULL,
    duration_ms bigint NOT NULL,
    limited boolean NOT NULL,
    scraped_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS email_addresses (
    site_url text NOT NULL REFERENCES email_sites (url) ON DELETE CASCADE,
    address text NOT NULL,
    source_url text NOT NULL,
    found_in text NOT NULL,
    PRIMARY KEY (site_url, address, source_url)
);

CREATE INDEX IF NOT EXISTS email_addresses_address_idx ON email_addresses (address);

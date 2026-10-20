package cli

import (
	"net/url"
	"regexp"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("PAGELODE_BROWSER_URL", "")
	t.Setenv("PAGELODE_BROWSER_SETTLE_MS", "")
	t.Setenv("PAGELODE_MAX_CONCURRENCY", "")
	t.Setenv("PAGELODE_DATABASE_URL", "")

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Address != ":8083" {
		t.Errorf("Load().Address = %q, want :8083", got.Address)
	}
	if len(got.BrowserURLs) != 1 || got.BrowserURLs[0] != "http://127.0.0.1:8090" {
		t.Errorf("Load().BrowserURLs = %q, want [http://127.0.0.1:8090]", got.BrowserURLs)
	}
	if got.DatabaseURL != "" {
		t.Errorf("Load().DatabaseURL = %q, want empty so state stays in memory", got.DatabaseURL)
	}
}

func TestLoadRejectsNonPostgresDatabaseURL(t *testing.T) {
	t.Setenv("PAGELODE_DATABASE_URL", "mysql://user@host/db")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("Load(PAGELODE_DATABASE_URL=mysql://...) error = nil, want scheme error")
	}
}

func TestLoadRejectsSettleOutOfRange(t *testing.T) {
	t.Setenv("PAGELODE_BROWSER_SETTLE_MS", "10001")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("Load(PAGELODE_BROWSER_SETTLE_MS=10001) error = nil, want range error")
	}
}

func TestLoadKeepsProxyTemplateAndPinsCapSolverSession(t *testing.T) {
	const template = "http://user:secret_country-US_session-fixed1_lifetime-30@proxy.example:3000"
	t.Setenv("PAGELODE_PROXY_URL", template)
	t.Setenv("PAGELODE_CAPSOLVER_PROXY_URL", "")
	sessionPassword := regexp.MustCompile(`^secret_country-US_session-[a-z0-9]{10}_lifetime-30$`)

	seen := make(map[string]bool)
	for range 3 {
		got, err := LoadConfig()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if got.ProxyURL != template {
			t.Fatalf("ProxyURL = %q, want the template %q; browser identities choose its session", got.ProxyURL, template)
		}
		parsed, err := url.Parse(got.CapSolverProxyURL)
		if err != nil {
			t.Fatalf("parse CapSolver proxy: %v", err)
		}
		password, _ := parsed.User.Password()
		if parsed.Host != "proxy.example:3000" || parsed.User.Username() != "user" || !sessionPassword.MatchString(password) {
			t.Fatalf("CapSolverProxyURL = %q", got.CapSolverProxyURL)
		}
		seen[password] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct CapSolver sessions across 3 runs, want 3", len(seen))
	}
}

func TestLoadAcceptsSessionPlaceholder(t *testing.T) {
	const template = "http://user-{session}:secret@proxy.example:3000"
	t.Setenv("PAGELODE_PROXY_URL", template)
	t.Setenv("PAGELODE_CAPSOLVER_PROXY_URL", "")

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProxyURL != template {
		t.Errorf("ProxyURL = %q, want %q", got.ProxyURL, template)
	}
	if !regexp.MustCompile(`^http://user-[a-z0-9]{10}:secret@proxy\.example:3000$`).MatchString(got.CapSolverProxyURL) {
		t.Errorf("CapSolverProxyURL = %q, want a concrete session", got.CapSolverProxyURL)
	}
}

func TestLoadKeepsProxyWithoutSession(t *testing.T) {
	const proxy = "http://user:secret@proxy.example:3000"
	t.Setenv("PAGELODE_PROXY_URL", proxy)
	t.Setenv("PAGELODE_CAPSOLVER_PROXY_URL", "")

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProxyURL != proxy || got.CapSolverProxyURL != proxy {
		t.Fatalf("ProxyURL = %q, CapSolverProxyURL = %q, want both unchanged %q", got.ProxyURL, got.CapSolverProxyURL, proxy)
	}
}

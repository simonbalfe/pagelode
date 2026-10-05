package config

import (
	"net/url"
	"regexp"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("PAGELODE_PATCHRIGHT_COMMAND", "")
	t.Setenv("PAGELODE_PATCHRIGHT_WORKER", "")
	t.Setenv("PAGELODE_BROWSER_CONCURRENCY", "")
	t.Setenv("PAGELODE_MAX_CONCURRENCY", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Address != ":8083" {
		t.Errorf("Load().Address = %q, want :8083", got.Address)
	}
	if !got.ChromedpEnabled {
		t.Error("Load().ChromedpEnabled = false, want true")
	}
	if got.PatchrightCommand != "bun" {
		t.Errorf("Load().PatchrightCommand = %q, want bun", got.PatchrightCommand)
	}
	if got.PatchrightWorker != "browser/src/worker.ts" {
		t.Errorf("Load().PatchrightWorker = %q, want browser/src/worker.ts", got.PatchrightWorker)
	}
}

func TestLoadRotatesProxySessionPerRun(t *testing.T) {
	t.Setenv("PAGELODE_PROXY_URL", "http://user:secret_country-US_session-fixed1_lifetime-30@proxy.example:3000")
	t.Setenv("PAGELODE_CAPSOLVER_PROXY_URL", "")
	sessionPassword := regexp.MustCompile(`^secret_country-US_session-[a-z0-9]{10}_lifetime-30$`)

	seen := make(map[string]bool)
	for range 3 {
		got, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if got.CapSolverProxyURL != got.ProxyURL {
			t.Fatalf("CapSolverProxyURL = %q, want shared %q so one run keeps one IP", got.CapSolverProxyURL, got.ProxyURL)
		}
		parsed, err := url.Parse(got.ProxyURL)
		if err != nil {
			t.Fatalf("parse rotated proxy: %v", err)
		}
		password, _ := parsed.User.Password()
		if parsed.Host != "proxy.example:3000" || parsed.User.Username() != "user" || !sessionPassword.MatchString(password) {
			t.Fatalf("rotated proxy = %q", got.ProxyURL)
		}
		seen[password] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct sessions across 3 runs, want 3", len(seen))
	}
}

func TestLoadKeepsProxyWithoutSession(t *testing.T) {
	const proxy = "http://user:secret@proxy.example:3000"
	t.Setenv("PAGELODE_PROXY_URL", proxy)
	t.Setenv("PAGELODE_CAPSOLVER_PROXY_URL", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProxyURL != proxy {
		t.Fatalf("ProxyURL = %q, want unchanged %q", got.ProxyURL, proxy)
	}
}

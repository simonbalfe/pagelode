package cli

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/pkg/browser"
)

type Config struct {
	Address           string
	BrowserURLs       []string
	BrowserToken      string
	BrowserSettle     time.Duration
	BrowserIdentities int
	BrowserNodeSlots  int
	CapSolverAPIKey   string
	CapSolverURL      string
	CapSolverProxyURL string
	DatabaseURL       string
	ProxyURL          string
	ProtectedDomains  []string
	MaxConcurrency    int
	MaxWaiting        int
	RequestTimeout    time.Duration
	RouteTTL          time.Duration
}

func LoadConfig() (Config, error) {
	port, err := integer("PORT", 8083, 1)
	if err != nil || port > 65535 {
		return Config{}, fmt.Errorf("config: PORT must be between 1 and 65535")
	}
	maxConcurrency, err := integer("PAGELODE_MAX_CONCURRENCY", 20, 1)
	if err != nil {
		return Config{}, err
	}
	maxWaiting, err := integer("PAGELODE_MAX_WAITING", 100, 0)
	if err != nil {
		return Config{}, err
	}
	settleMS, err := integer("PAGELODE_BROWSER_SETTLE_MS", 2000, 0)
	if err != nil || settleMS > 10_000 {
		return Config{}, fmt.Errorf("config: PAGELODE_BROWSER_SETTLE_MS must be between 0 and 10000")
	}
	browserIdentities, err := integer("PAGELODE_BROWSER_IDENTITIES", 4, 1)
	if err != nil {
		return Config{}, err
	}
	browserNodeSlots, err := integer("PAGELODE_BROWSER_NODE_SLOTS", 16, 1)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := duration("PAGELODE_REQUEST_TIMEOUT", 90*time.Second)
	if err != nil {
		return Config{}, err
	}
	routeTTL, err := duration("PAGELODE_ROUTE_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	proxyURL := os.Getenv("PAGELODE_PROXY_URL")
	if proxyURL != "" {
		if err := validateURL(browser.ProxyWithSession(proxyURL, "session"), "http", "https", "socks4", "socks5"); err != nil {
			return Config{}, fmt.Errorf("config: invalid PAGELODE_PROXY_URL: %w", err)
		}
	}
	capSolverProxyURL := value("PAGELODE_CAPSOLVER_PROXY_URL", proxyURL)
	if capSolverProxyURL != "" {
		if err := validateURL(browser.ProxyWithSession(capSolverProxyURL, "session"), "http", "https", "socks4", "socks5"); err != nil {
			return Config{}, fmt.Errorf("config: invalid PAGELODE_CAPSOLVER_PROXY_URL: %w", err)
		}
		if capSolverProxyURL, err = freshSession(capSolverProxyURL); err != nil {
			return Config{}, fmt.Errorf("config: PAGELODE_CAPSOLVER_PROXY_URL session: %w", err)
		}
	}
	capSolverURL := value("PAGELODE_CAPSOLVER_URL", "https://api.capsolver.com")
	if err := validateURL(capSolverURL, "http", "https"); err != nil {
		return Config{}, fmt.Errorf("config: invalid PAGELODE_CAPSOLVER_URL: %w", err)
	}

	browserURLs := list(value("PAGELODE_BROWSER_URL", "http://127.0.0.1:8090"))
	if len(browserURLs) == 0 {
		return Config{}, fmt.Errorf("config: PAGELODE_BROWSER_URL must list at least one URL")
	}
	for _, browserURL := range browserURLs {
		if err := validateURL(browserURL, "http", "https"); err != nil {
			return Config{}, fmt.Errorf("config: invalid PAGELODE_BROWSER_URL %q: %w", browserURL, err)
		}
	}
	databaseURL := os.Getenv("PAGELODE_DATABASE_URL")
	if databaseURL != "" {
		if err := validateURL(databaseURL, "postgres", "postgresql"); err != nil {
			return Config{}, fmt.Errorf("config: invalid PAGELODE_DATABASE_URL: %w", err)
		}
	}
	return Config{
		Address:           fmt.Sprintf(":%d", port),
		BrowserURLs:       browserURLs,
		BrowserToken:      os.Getenv("PAGELODE_BROWSER_TOKEN"),
		BrowserSettle:     time.Duration(settleMS) * time.Millisecond,
		BrowserIdentities: browserIdentities,
		BrowserNodeSlots:  browserNodeSlots,
		CapSolverAPIKey:   os.Getenv("CAPSOLVER_API_KEY"),
		CapSolverURL:      capSolverURL,
		CapSolverProxyURL: capSolverProxyURL,
		DatabaseURL:       databaseURL,
		ProxyURL:          proxyURL,
		ProtectedDomains:  domains(value("PAGELODE_PROTECTED_DOMAINS", "crunchbase.com")),
		MaxConcurrency:    maxConcurrency,
		MaxWaiting:        maxWaiting,
		RequestTimeout:    requestTimeout,
		RouteTTL:          routeTTL,
	}, nil
}

func freshSession(raw string) (string, error) {
	if !browser.StickyProxy(raw) {
		return raw, nil
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	id := make([]byte, 10)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	for index, current := range id {
		id[index] = alphabet[int(current)%len(alphabet)]
	}
	return browser.ProxyWithSession(raw, string(id)), nil
}

func integer(name string, fallback int, minimum int) (int, error) {
	raw := value(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum {
		return 0, fmt.Errorf("config: %s must be an integer of at least %d", name, minimum)
	}
	return parsed, nil
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	raw := value(name, fallback.String())
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("config: %s must be a positive duration", name)
	}
	return parsed, nil
}

func value(name string, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}

func domains(raw string) []string {
	result := list(raw)
	for index, current := range result {
		result[index] = strings.ToLower(current)
	}
	return result
}

func list(raw string) []string {
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, current := range values {
		if current = strings.TrimSpace(current); current != "" {
			result = append(result, current)
		}
	}
	return result
}

func validateURL(raw string, schemes ...string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	if parsed.Host == "" {
		return fmt.Errorf("URL must include a host")
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("URL uses unsupported scheme %q", parsed.Scheme)
}

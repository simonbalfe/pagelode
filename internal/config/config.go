package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address            string
	PatchrightCommand  string
	PatchrightWorker   string
	ProxyURL           string
	RodEnabled         bool
	ProtectedDomains   []string
	MaxConcurrency     int
	BrowserConcurrency int
	MaxWaiting         int
	RequestTimeout     time.Duration
	RouteTTL           time.Duration
}

func Load() (Config, error) {
	port, err := integer("PORT", 8083, 1)
	if err != nil || port > 65535 {
		return Config{}, fmt.Errorf("config: PORT must be between 1 and 65535")
	}
	maxConcurrency, err := integer("PAGELODE_MAX_CONCURRENCY", 20, 1)
	if err != nil {
		return Config{}, err
	}
	browserConcurrency, err := integer("PAGELODE_BROWSER_CONCURRENCY", 4, 1)
	if err != nil {
		return Config{}, err
	}
	if browserConcurrency > maxConcurrency {
		return Config{}, fmt.Errorf("config: browser concurrency cannot exceed maximum concurrency")
	}
	maxWaiting, err := integer("PAGELODE_MAX_WAITING", 100, 0)
	if err != nil {
		return Config{}, err
	}
	rodEnabled, err := boolean("PAGELODE_ROD_ENABLED", true)
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
		if err := validateURL(proxyURL, "http", "https", "socks4", "socks5"); err != nil {
			return Config{}, fmt.Errorf("config: invalid PAGELODE_PROXY_URL: %w", err)
		}
	}

	return Config{
		Address:            fmt.Sprintf(":%d", port),
		PatchrightCommand:  value("PAGELODE_PATCHRIGHT_COMMAND", "bun"),
		PatchrightWorker:   value("PAGELODE_PATCHRIGHT_WORKER", "browser/src/worker.ts"),
		ProxyURL:           proxyURL,
		RodEnabled:         rodEnabled,
		ProtectedDomains:   domains(value("PAGELODE_PROTECTED_DOMAINS", "crunchbase.com")),
		MaxConcurrency:     maxConcurrency,
		BrowserConcurrency: browserConcurrency,
		MaxWaiting:         maxWaiting,
		RequestTimeout:     requestTimeout,
		RouteTTL:           routeTTL,
	}, nil
}

func integer(name string, fallback int, minimum int) (int, error) {
	raw := value(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum {
		return 0, fmt.Errorf("config: %s must be an integer of at least %d", name, minimum)
	}
	return parsed, nil
}

func boolean(name string, fallback bool) (bool, error) {
	raw := value(name, strconv.FormatBool(fallback))
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean", name)
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
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, current := range values {
		current = strings.ToLower(strings.TrimSpace(current))
		if current != "" {
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

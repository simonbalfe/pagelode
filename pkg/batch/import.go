package batch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/pkg/queue"
)

type Importer interface {
	Import(ctx context.Context, entries []queue.Entry) (int64, error)
}

type ImportStats struct {
	Lines    int64
	Invalid  int64
	Inserted int64
}

const maxLineBytes = 64 << 10

func Import(ctx context.Context, q Importer, input io.Reader, batchSize int) (ImportStats, error) {
	if batchSize < 1 {
		return ImportStats{}, errors.New("batch: import batch size must be positive")
	}
	var stats ImportStats
	entries := make([]queue.Entry, 0, batchSize)
	flush := func() error {
		if len(entries) == 0 {
			return nil
		}
		inserted, err := q.Import(ctx, entries)
		if err != nil {
			return fmt.Errorf("batch: import: %w", err)
		}
		stats.Inserted += inserted
		entries = entries[:0]
		return nil
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		stats.Lines++
		entry, err := Normalize(line)
		if err != nil {
			stats.Invalid++
			continue
		}
		entries = append(entries, entry)
		if len(entries) == batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("batch: read input: %w", err)
	}
	return stats, flush()
}

func Normalize(raw string) (queue.Entry, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return queue.Entry{}, fmt.Errorf("batch: invalid URL %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" {
		return queue.Entry{}, fmt.Errorf("batch: %q is not an absolute HTTP(S) URL", raw)
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path == "" && parsed.Opaque == "" {
		parsed.Path = "/"
	}
	return queue.Entry{URL: parsed.String(), Host: parsed.Hostname()}, nil
}

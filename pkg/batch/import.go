package batch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/riverqueue/river"
)

type Entry struct {
	URL  string
	Host string
}

type ImportStats struct {
	Lines      int64
	Invalid    int64
	Inserted   int64
	Duplicates int64
}

const maxLineBytes = 64 << 10

func (q *Queue) Import(ctx context.Context, input io.Reader, batchSize int) (ImportStats, error) {
	if batchSize < 1 {
		return ImportStats{}, errors.New("batch: import batch size must be positive")
	}
	var stats ImportStats
	params := make([]river.InsertManyParams, 0, batchSize)
	inBatch := make(map[string]bool, batchSize)
	flush := func() error {
		if len(params) == 0 {
			return nil
		}
		results, err := q.client.InsertMany(ctx, params)
		if err != nil {
			return fmt.Errorf("batch: import: %w", err)
		}
		for _, result := range results {
			if result.UniqueSkippedAsDuplicate {
				stats.Duplicates++
			} else {
				stats.Inserted++
			}
		}
		params = params[:0]
		clear(inBatch)
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
		if inBatch[entry.URL] {
			stats.Duplicates++
			continue
		}
		inBatch[entry.URL] = true
		params = append(params, river.InsertManyParams{Args: HTTPArgs{URL: entry.URL, Host: entry.Host}})
		if len(params) == batchSize {
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

func Normalize(raw string) (Entry, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Entry{}, fmt.Errorf("batch: invalid URL %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" {
		return Entry{}, fmt.Errorf("batch: %q is not an absolute HTTP(S) URL", raw)
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path == "" && parsed.Opaque == "" {
		parsed.Path = "/"
	}
	return Entry{URL: parsed.String(), Host: parsed.Hostname()}, nil
}

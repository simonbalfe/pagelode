package batch

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/pkg/orchestrator"
	"github.com/simonbalfe/pagelode/pkg/queue"
)

const (
	flushInterval     = 30 * time.Second
	fileRecords       = 5000
	partSuffix        = ".part"
	fileExtension     = ".jsonl.gz"
	completeAttempts  = 3
	completeRetryWait = 2 * time.Second
)

type Record struct {
	ID        int64     `json:"id"`
	FetchedAt time.Time `json:"fetchedAt"`
	orchestrator.Result
}

type Writer struct {
	dir     string
	prefix  string
	q       Queue
	records chan pending
	flushes chan chan error
	done    chan struct{}

	mu  sync.Mutex
	err error
}

type pending struct {
	item   queue.Item
	result orchestrator.Result
}

func NewWriter(dir, owner string, q Queue) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("batch: create output directory: %w", err)
	}
	w := &Writer{dir: dir, prefix: "pages-" + fileSafe(owner), q: q, records: make(chan pending), flushes: make(chan chan error), done: make(chan struct{})}
	go w.loop()
	return w, nil
}

func (w *Writer) Add(ctx context.Context, item queue.Item, result orchestrator.Result) error {
	select {
	case w.records <- pending{item: item, result: result}:
		return nil
	case <-w.done:
		return w.failure()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) Flush(ctx context.Context) error {
	reply := make(chan error, 1)
	select {
	case w.flushes <- reply:
	case <-w.done:
		return w.failure()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) Close(ctx context.Context) error {
	close(w.records)
	select {
	case <-w.done:
		return w.failure()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *Writer) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

func (w *Writer) loop() {
	defer close(w.done)
	ticker := time.NewTicker(flushInterval / 3)
	defer ticker.Stop()
	var current *outputFile
	sequence := 0
	for {
		select {
		case record, ok := <-w.records:
			if !ok {
				if err := w.finish(current); err != nil {
					w.fail(err)
				}
				return
			}
			if current == nil {
				sequence++
				file, err := w.open(sequence)
				if err != nil {
					w.fail(err)
					return
				}
				current = file
			}
			if err := current.write(record); err != nil {
				w.fail(errors.Join(err, current.abandon()))
				return
			}
			if len(current.completions) >= fileRecords {
				if err := w.finish(current); err != nil {
					w.fail(err)
					return
				}
				current = nil
			}
		case reply := <-w.flushes:
			err := w.finish(current)
			current = nil
			reply <- err
			if err != nil {
				w.fail(err)
				return
			}
		case <-ticker.C:
			if current == nil || time.Since(current.opened) < flushInterval {
				continue
			}
			if err := w.finish(current); err != nil {
				w.fail(err)
				return
			}
			current = nil
		}
	}
}

func (w *Writer) open(sequence int) (*outputFile, error) {
	opened := time.Now()
	name := fmt.Sprintf("%s-%s-%06d%s", w.prefix, opened.UTC().Format("20060102T150405Z"), sequence, fileExtension)
	file, err := os.OpenFile(filepath.Join(w.dir, name+partSuffix), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("batch: open output file: %w", err)
	}
	buffered := bufio.NewWriterSize(file, 256<<10)
	compressed := gzip.NewWriter(buffered)
	return &outputFile{name: name, path: filepath.Join(w.dir, name), file: file, buffered: buffered, compressed: compressed, encoder: json.NewEncoder(compressed), opened: opened}, nil
}

func (w *Writer) finish(current *outputFile) error {
	if current == nil {
		return nil
	}
	if err := current.close(); err != nil {
		return err
	}
	var err error
	for attempt := range completeAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
		err = w.q.Complete(ctx, current.completions)
		cancel()
		if err == nil {
			return nil
		}
		if attempt < completeAttempts-1 {
			time.Sleep(completeRetryWait)
		}
	}
	return fmt.Errorf("batch: complete %d pages written to %s: %w", len(current.completions), current.name, err)
}

type outputFile struct {
	name        string
	path        string
	file        *os.File
	buffered    *bufio.Writer
	compressed  *gzip.Writer
	encoder     *json.Encoder
	opened      time.Time
	completions []queue.Completion
}

func (f *outputFile) write(record pending) error {
	if err := f.encoder.Encode(Record{ID: record.item.ID, FetchedAt: time.Now().UTC(), Result: record.result}); err != nil {
		return fmt.Errorf("batch: write %s: %w", f.name, err)
	}
	done := completion(record.item, queue.Done, record.result)
	done.ContentRef = f.name + ":" + strconv.Itoa(len(f.completions)+1)
	f.completions = append(f.completions, done)
	return nil
}

func (f *outputFile) close() error {
	if err := f.compressed.Close(); err != nil {
		return errors.Join(fmt.Errorf("batch: compress %s: %w", f.name, err), f.abandon())
	}
	if err := f.buffered.Flush(); err != nil {
		return errors.Join(fmt.Errorf("batch: flush %s: %w", f.name, err), f.abandon())
	}
	if err := f.file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("batch: sync %s: %w", f.name, err), f.abandon())
	}
	if err := f.file.Close(); err != nil {
		return fmt.Errorf("batch: close %s: %w", f.name, err)
	}
	if err := os.Rename(f.path+partSuffix, f.path); err != nil {
		return fmt.Errorf("batch: rename %s: %w", f.name, err)
	}
	return nil
}

func (f *outputFile) abandon() error {
	if err := f.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("batch: close %s: %w", f.name, err)
	}
	return nil
}

func fileSafe(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '-'
		}
	}, value)
}

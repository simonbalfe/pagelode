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

	"github.com/simonbalfe/pagelode/pkg/fetch"
)

const (
	flushInterval = 2 * time.Second
	quietPeriod   = 100 * time.Millisecond
	fileRecords   = 5000
	partSuffix    = ".part"
	fileExtension = ".jsonl.gz"
)

type Record struct {
	ID        int64     `json:"id"`
	FetchedAt time.Time `json:"fetchedAt"`
	fetch.Result
}

type Writer struct {
	dir     string
	prefix  string
	records chan pending
	done    chan struct{}

	mu  sync.Mutex
	err error
}

type pending struct {
	id     int64
	result fetch.Result
	reply  chan written
}

type written struct {
	ref string
	err error
}

func NewWriter(dir, owner string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("batch: create output directory: %w", err)
	}
	w := &Writer{dir: dir, prefix: "pages-" + fileSafe(owner), records: make(chan pending), done: make(chan struct{})}
	go w.loop()
	return w, nil
}

func (w *Writer) Add(ctx context.Context, id int64, result fetch.Result) (string, error) {
	reply := make(chan written, 1)
	select {
	case w.records <- pending{id: id, result: result, reply: reply}:
	case <-w.done:
		return "", w.failure()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	answer := <-reply
	return answer.ref, answer.err
}

func (w *Writer) Close() error {
	close(w.records)
	<-w.done
	return w.failure()
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
	ticker := time.NewTicker(quietPeriod / 2)
	defer ticker.Stop()
	var current *outputFile
	sequence := 0
	for {
		select {
		case record, ok := <-w.records:
			if !ok {
				if err := current.finish(); err != nil {
					w.fail(err)
				}
				return
			}
			if current == nil {
				sequence++
				file, err := w.open(sequence)
				if err != nil {
					record.reply <- written{err: err}
					w.fail(err)
					return
				}
				current = file
			}
			current.written = time.Now()
			if err := current.write(record); err != nil {
				current.reject(err)
				w.fail(errors.Join(err, current.abandon()))
				return
			}
			if len(current.waiting) >= fileRecords {
				if err := current.finish(); err != nil {
					w.fail(err)
					return
				}
				current = nil
			}
		case <-ticker.C:
			if current == nil || time.Since(current.opened) < flushInterval && time.Since(current.written) < quietPeriod {
				continue
			}
			if err := current.finish(); err != nil {
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

type outputFile struct {
	name       string
	path       string
	file       *os.File
	buffered   *bufio.Writer
	compressed *gzip.Writer
	encoder    *json.Encoder
	opened     time.Time
	written    time.Time
	waiting    []chan written
}

func (f *outputFile) write(record pending) error {
	f.waiting = append(f.waiting, record.reply)
	if err := f.encoder.Encode(Record{ID: record.id, FetchedAt: time.Now().UTC(), Result: record.result}); err != nil {
		return fmt.Errorf("batch: write %s: %w", f.name, err)
	}
	return nil
}

func (f *outputFile) finish() error {
	if f == nil {
		return nil
	}
	if err := f.close(); err != nil {
		f.reject(err)
		return err
	}
	for index, reply := range f.waiting {
		reply <- written{ref: f.name + ":" + strconv.Itoa(index+1)}
	}
	return nil
}

func (f *outputFile) reject(err error) {
	for _, reply := range f.waiting {
		reply <- written{err: err}
	}
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

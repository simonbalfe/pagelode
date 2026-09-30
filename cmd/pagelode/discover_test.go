package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/discovery"
)

func TestDiscoverCLIFromHAR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har")
	data := `{"log":{"entries":[{"request":{"method":"GET","url":"https://example.com/api"},"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"items\":[{\"id\":1}]}"}}}]}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	app := &application{discoverer: discovery.New(nil, nil, nil, nil, false)}
	var output bytes.Buffer
	if err := app.discover(config.Config{RequestTimeout: time.Second}, []string{"--har", path}, &output, &output); err != nil {
		t.Fatal(err)
	}
	var report discovery.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 {
		t.Errorf("endpoints = %+v, want 1", report.Endpoints)
	}
}

func TestDiscoverCLIVerbosity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har")
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[{"request":{"method":"GET","url":"https://example.com/api"},"response":{"content":{"mimeType":"application/json"}}}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	app := &application{discoverer: discovery.New(nil, nil, nil, nil, false)}
	for _, verbose := range []bool{false, true} {
		args := []string{"--har", path}
		if verbose {
			args = append([]string{"--verbose"}, args...)
		}
		var output bytes.Buffer
		if err := app.discover(config.Config{RequestTimeout: time.Second}, args, &output, &output); err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(output.Bytes(), []byte(`"evidence"`)); got != verbose {
			t.Errorf("verbose=%t evidence present=%t", verbose, got)
		}
	}
}

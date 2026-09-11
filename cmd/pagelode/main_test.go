package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunShowsUsageWithoutArguments(t *testing.T) {
	var output bytes.Buffer
	if err := run(nil, &output, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(output.String(), "pagelode <URL>") {
		t.Fatalf("run() output = %q, want usage", output.String())
	}
}

func TestRunShowsVersion(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version"}, &output, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := output.String(), "PageLode "+version+"\n"; got != want {
		t.Fatalf("run() output = %q, want %q", got, want)
	}
}

func TestAttemptDetail(t *testing.T) {
	if got := attemptDetail(nil); got != "" {
		t.Fatalf("attemptDetail(nil) = %q, want empty", got)
	}
}

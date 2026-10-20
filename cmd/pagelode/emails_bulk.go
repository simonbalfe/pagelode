package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/simonbalfe/pagelode/emails"
	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/emailstore"
)

const pagesLoadedInParallelPerSite = 4

type siteResult struct {
	site   string
	report emails.Report
	err    error
}

type emailsBulkFlags struct {
	database  *string
	workers   *int
	maxPages  *int
	maxEmails *int
	duration  *time.Duration
	render    *string
}

func defineEmailsBulkFlags(flags *flag.FlagSet) emailsBulkFlags {
	var options emailsBulkFlags
	options.database = flags.String("db", "data/emails.sqlite", "SQLite database `path` to create or extend")
	options.workers = flags.Int("workers", 32, "sites crawled in parallel (1–512)")
	options.maxPages = flags.Int("max-pages", 8, "maximum pages to visit per site (1–100)")
	options.maxEmails = flags.Int("max-emails", 50, "stop a site after this many unique addresses (1–1000)")
	options.duration = flags.Duration("max-duration", 20*time.Second, "crawl time budget per site (1s–2m)")
	options.render = flags.String("render", "never", "browser rendering: auto, never, or always")
	return options
}

func emailsBulk(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("emails-bulk", stderr)
	options := defineEmailsBulkFlags(flags)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one file of URLs or domains, one per line, or - for standard input; place options before it")
	}
	if *options.workers < 1 || *options.workers > 512 {
		return errors.New("workers must be between 1 and 512")
	}
	if *options.duration < time.Second || *options.duration > 2*time.Minute {
		return errors.New("max-duration must be between 1s and 2m")
	}
	template := emails.Request{MaxPages: *options.maxPages, MaxEmails: *options.maxEmails, MaxDurationMS: int(options.duration.Milliseconds()), Render: *options.render}
	probe := template
	probe.URL = "example.com"
	if _, err := probe.Validate(); err != nil {
		return err
	}
	targets, err := readTargets(flags.Arg(0))
	if err != nil {
		return err
	}
	store, err := emailstore.Open(*options.database)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done, err := store.Done(ctx)
	if err != nil {
		return err
	}
	pending := make([]string, 0, len(targets))
	for _, target := range targets {
		if !done[target] {
			pending = append(pending, target)
		}
	}
	fmt.Fprintf(stderr, "pagelode: %d sites, %d already done, %d to crawl with %d workers\n", len(targets), len(targets)-len(pending), len(pending), *options.workers)
	if len(pending) == 0 {
		return nil
	}

	configuration.MaxConcurrency = *options.workers * pagesLoadedInParallelPerSite
	configuration.MaxWaiting = *options.workers * pagesLoadedInParallelPerSite
	app, err := newApplication(configuration)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			fmt.Fprintln(stderr, "pagelode: close:", err)
		}
	}()

	jobs := make(chan string)
	results := make(chan siteResult)
	var crawlers sync.WaitGroup
	for range *options.workers {
		crawlers.Add(1)
		go func() {
			defer crawlers.Done()
			for site := range jobs {
				request := template
				request.URL = site
				report, err := app.emailFinder.Find(ctx, request)
				results <- siteResult{site: site, report: report, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, site := range pending {
			select {
			case jobs <- site:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		crawlers.Wait()
		close(results)
	}()

	var finished, withEmails, failed, addresses atomic.Int64
	started := time.Now()
	var saveError error
	for result := range results {
		if ctx.Err() != nil {
			continue
		}
		var err error
		if result.err != nil {
			failed.Add(1)
			err = store.SaveFailure(ctx, result.site, result.err)
		} else {
			if len(result.report.Emails) > 0 {
				withEmails.Add(1)
			}
			addresses.Add(int64(len(result.report.Emails)))
			err = store.Save(ctx, result.site, result.report)
		}
		if err != nil {
			saveError = err
			stop()
			continue
		}
		if count := finished.Add(1); count%100 == 0 || int(count) == len(pending) {
			fmt.Fprintf(stderr, "pagelode: %d/%d sites, %d with emails, %d addresses, %d errors, %s elapsed\n", count, len(pending), withEmails.Load(), addresses.Load(), failed.Load(), time.Since(started).Round(time.Second))
		}
	}
	if saveError != nil {
		return saveError
	}
	if ctx.Err() != nil {
		fmt.Fprintf(stdout, "interrupted after %d of %d sites; rerun the same command to resume\n", finished.Load(), len(pending))
		return nil
	}
	fmt.Fprintf(stdout, "%d sites crawled, %d with emails, %d addresses, %d errors, written to %s\n", finished.Load(), withEmails.Load(), addresses.Load(), failed.Load(), *options.database)
	return nil
}

func readTargets(path string) ([]string, error) {
	var input io.Reader = os.Stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open targets: %w", err)
		}
		defer file.Close()
		input = file
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	seen := map[string]bool{}
	targets := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		request, err := emails.Request{URL: line}.Validate()
		if err != nil || seen[request.URL] {
			continue
		}
		seen[request.URL] = true
		targets = append(targets, request.URL)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read targets: %w", err)
	}
	return targets, nil
}

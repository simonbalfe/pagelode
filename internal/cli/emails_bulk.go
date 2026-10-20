package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/emailstore"
	"github.com/simonbalfe/pagelode/pkg/emails"
)

const pagesLoadedInParallelPerSite = 4

type siteResult struct {
	site   string
	report emails.Report
	err    error
}

func emailsBulk(configuration config.Config, options emailsFlags, set map[string]bool, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return errors.New("--input reads the sites from a file; do not also pass a URL")
	}
	if set["profile"] || set["verbose"] {
		return errors.New("--profile and --verbose do not apply with --input")
	}
	if !set["max-pages"] {
		*options.maxPages = 8
	}
	if !set["max-emails"] {
		*options.maxEmails = 50
	}
	if !set["max-duration"] {
		*options.duration = 20 * time.Second
	}
	if !set["render"] {
		*options.render = "never"
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
	targets, err := readTargets(*options.input)
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/sitemap"
)

func (a *application) siteMap(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("pagelode map", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 500, "maximum URLs to return (1–5000)")
	duration := flags.Duration("max-duration", 30*time.Second, "time budget (1s–2m)")
	subdomains := flags.Bool("subdomains", false, "include subdomains of the site")
	jsonOutput := flags.Bool("json", false, "print the complete JSON report")
	var include, exclude []string
	flags.Func("include", "only URLs whose path matches this pattern, e.g. /blog/* (repeatable)", func(value string) error {
		include = append(include, value)
		return nil
	})
	flags.Func("exclude", "skip URLs whose path matches this pattern (repeatable)", func(value string) error {
		exclude = append(exclude, value)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one URL or domain; place options before the URL")
	}
	if *duration < time.Second || *duration > 2*time.Minute {
		return errors.New("max-duration must be between 1s and 2m")
	}
	ctx, cancel := context.WithTimeout(context.Background(), configuration.RequestTimeout)
	defer cancel()
	report, err := a.mapper.Map(ctx, sitemap.Request{URL: flags.Arg(0), Limit: *limit, MaxDurationMS: int(duration.Milliseconds()), Subdomains: *subdomains, Include: include, Exclude: exclude})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("write map: %w", err)
		}
	} else {
		for _, entry := range report.URLs {
			if _, err := fmt.Fprintln(stdout, entry.URL); err != nil {
				return fmt.Errorf("write map: %w", err)
			}
		}
	}
	if report.Outcome == "failed" {
		return errors.New("map found no URLs and could not load the site")
	}
	return nil
}

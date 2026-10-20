package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/simonbalfe/pagelode/emails"
	"github.com/simonbalfe/pagelode/internal/config"
)

type emailsFlags struct {
	maxPages  *int
	maxEmails *int
	duration  *time.Duration
	profile   *string
	verbose   *bool
	render    *string
}

func defineEmailsFlags(flags *flag.FlagSet) emailsFlags {
	var options emailsFlags
	options.maxPages = flags.Int("max-pages", 20, "maximum pages to visit (1–100)")
	options.maxEmails = flags.Int("max-emails", 100, "stop after this many unique addresses (1–1000)")
	options.duration = flags.Duration("max-duration", 30*time.Second, "crawl time budget (1s–2m)")
	options.profile = flags.String("profile", "", "`name` of a saved signed-in browser profile (see pagelode profile login)")
	options.verbose = flags.Bool("verbose", false, "include sources, page outcomes, and crawl details as JSON")
	options.render = flags.String("render", "auto", "browser rendering: auto, never, or always")
	return options
}

func (a *application) emails(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("emails", stderr)
	options := defineEmailsFlags(flags)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one URL or domain; place options before the URL")
	}
	if *options.duration < time.Second || *options.duration > 2*time.Minute {
		return errors.New("max-duration must be between 1s and 2m")
	}
	ctx, cancel := context.WithTimeout(context.Background(), configuration.RequestTimeout)
	defer cancel()
	report, err := a.emailFinder.Find(ctx, emails.Request{URL: flags.Arg(0), MaxPages: *options.maxPages, MaxEmails: *options.maxEmails, MaxDurationMS: int(options.duration.Milliseconds()), Profile: *options.profile, Render: *options.render})
	if err != nil {
		return err
	}
	if *options.verbose {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("write emails: %w", err)
		}
	} else {
		for _, address := range report.Emails {
			if _, err := fmt.Fprintln(stdout, address.Address); err != nil {
				return fmt.Errorf("write emails: %w", err)
			}
		}
	}
	if report.Outcome == "failed" {
		return errors.New("email search failed to load any pages")
	}
	return nil
}

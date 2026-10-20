package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/simonbalfe/pagelode/pkg/emails"
	"io"
	"time"
)

type emailsFlags struct {
	input     *string
	database  *string
	workers   *int
	maxPages  *int
	maxEmails *int
	duration  *time.Duration
	profile   *string
	verbose   *bool
	render    *string
}

func defineEmailsFlags(flags *flag.FlagSet, configuration Config) emailsFlags {
	var options emailsFlags
	options.input = flags.String("input", "", "`file` of URLs or domains, one per line, or - for standard input; crawls every site in parallel and writes to --database")
	options.database = flags.String("database", configuration.DatabaseURL, "Postgres `URL` that stores the --input results (PAGELODE_DATABASE_URL)")
	options.workers = flags.Int("workers", 32, "sites crawled in parallel with --input (1–512)")
	options.maxPages = flags.Int("max-pages", 20, "maximum pages to visit per site (1–100; 8 with --input)")
	options.maxEmails = flags.Int("max-emails", 100, "stop a site after this many unique addresses (1–1000; 50 with --input)")
	options.duration = flags.Duration("max-duration", 30*time.Second, "crawl time budget per site (1s–2m; 20s with --input)")
	options.profile = flags.String("profile", "", "`name` of a saved signed-in browser profile (see pagelode profile login)")
	options.verbose = flags.Bool("verbose", false, "include sources, page outcomes, and crawl details as JSON")
	options.render = flags.String("render", "auto", "browser rendering: auto, never, or always (never with --input)")
	return options
}

func emailsCommand(configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("emails", stderr)
	options := defineEmailsFlags(flags, configuration)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(entry *flag.Flag) { set[entry.Name] = true })
	if set["input"] {
		return emailsBulk(configuration, options, set, flags.Args(), stdout, stderr)
	}
	if set["database"] || set["workers"] {
		return errors.New("--database and --workers apply only with --input")
	}
	if flags.NArg() != 1 {
		return errors.New("provide one URL or domain, or --input <file|-> for many sites; place options before the URL")
	}
	if *options.duration < time.Second || *options.duration > 2*time.Minute {
		return errors.New("max-duration must be between 1s and 2m")
	}
	app, err := newApplication(context.Background(), configuration)
	if err != nil {
		return err
	}
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), configuration.RequestTimeout)
	defer cancel()
	report, err := app.emailFinder.Find(ctx, emails.Request{URL: flags.Arg(0), MaxPages: *options.maxPages, MaxEmails: *options.maxEmails, MaxDurationMS: int(options.duration.Milliseconds()), Profile: *options.profile, Render: *options.render})
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

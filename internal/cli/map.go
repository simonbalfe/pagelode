package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/simonbalfe/pagelode/pkg/sitemap"
	"io"
	"strings"
	"time"
)

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

type mapFlags struct {
	limit      *int
	duration   *time.Duration
	subdomains *bool
	depth      *int
	render     *string
	explore    *bool
	jsonOutput *bool
	include    *stringList
	exclude    *stringList
}

func defineMapFlags(flags *flag.FlagSet) mapFlags {
	options := mapFlags{include: new(stringList), exclude: new(stringList)}
	options.limit = flags.Int("limit", 500, "maximum URLs to return (1–5000)")
	options.duration = flags.Duration("max-duration", 30*time.Second, "time budget (1s–2m)")
	options.subdomains = flags.Bool("subdomains", false, "include subdomains of the site")
	options.depth = flags.Int("depth", 0, "link levels to follow from the start page, 1–5 (default 2; 1 reads only the start page)")
	options.render = flags.String("render", "never", "load pages in a browser: never, auto (only pages that need JavaScript), or always")
	options.explore = flags.Bool("explore", false, "scroll and click safe controls on the start page in a browser to reveal more links (needs --render auto or always)")
	options.jsonOutput = flags.Bool("json", false, "print the complete JSON report")
	flags.Var(options.include, "include", "only URLs whose path matches this `pattern`, e.g. /blog/* (repeatable)")
	flags.Var(options.exclude, "exclude", "skip URLs whose path matches this `pattern` (repeatable)")
	return options
}

func (a *application) siteMap(configuration Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("map", stderr)
	options := defineMapFlags(flags)
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
	report, err := a.mapper.Map(ctx, sitemap.Request{
		URL:           flags.Arg(0),
		Limit:         *options.limit,
		MaxDurationMS: int(options.duration.Milliseconds()),
		Subdomains:    *options.subdomains,
		Include:       *options.include,
		Exclude:       *options.exclude,
		Depth:         *options.depth,
		Render:        *options.render,
		Explore:       *options.explore,
	})
	if err != nil {
		return err
	}
	if *options.jsonOutput {
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

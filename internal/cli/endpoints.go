package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/pkg/discovery"
)

type discoverFlags struct {
	wait    *int
	profile *string
	verbose *bool
	stealth *bool
	har     *string
}

func defineDiscoverFlags(flags *flag.FlagSet) discoverFlags {
	var options discoverFlags
	options.wait = flags.Int("wait-ms", 1500, "longest wait for network activity after page load, 100–10000 ms; ends earlier when the page goes quiet")
	options.profile = flags.String("profile", "", "`name` of a saved signed-in browser profile (see pagelode profile login)")
	options.verbose = flags.Bool("verbose", false, "include request evidence, matching scores, and loading details")
	options.stealth = flags.Bool("stealth", false, "refuse to load unless the browser routes through PAGELODE_PROXY_URL")
	options.har = flags.String("har", "", "analyze a HAR `file` without opening a browser")
	return options
}

func (a *application) endpoints(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("map endpoints", stderr)
	options := defineDiscoverFlags(flags)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	request := discovery.Request{WaitMS: *options.wait, Verbose: *options.verbose, Profile: *options.profile, Stealth: *options.stealth}
	if *options.har != "" {
		if flags.NArg() > 1 {
			return errors.New("provide at most one page URL with --har")
		}
		har, err := readHAR(*options.har)
		if err != nil {
			return err
		}
		request.HAR = &har
		request.WaitMS = 0
		if flags.NArg() == 1 {
			request.URL = flags.Arg(0)
		}
	} else {
		if flags.NArg() != 1 {
			return errors.New("provide one URL or --har <file>")
		}
		request.URL = flags.Arg(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), configuration.RequestTimeout)
	defer cancel()
	report, err := a.discoverer.Discover(ctx, request)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write discovery: %w", err)
	}
	if report.Outcome != "ok" {
		return fmt.Errorf("discovery %s", report.Outcome)
	}
	return nil
}

func readHAR(path string) (discovery.HAR, error) {
	file, err := os.Open(path)
	if err != nil {
		return discovery.HAR{}, fmt.Errorf("open HAR: %w", err)
	}
	defer file.Close()
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return discovery.HAR{}, fmt.Errorf("read HAR: %w", err)
	}
	if len(data) > limit {
		return discovery.HAR{}, errors.New("HAR exceeds 4 MiB")
	}
	var har discovery.HAR
	if err := json.Unmarshal(data, &har); err != nil {
		return discovery.HAR{}, fmt.Errorf("decode HAR: %w", err)
	}
	return har, nil
}

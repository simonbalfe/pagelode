package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/simonbalfe/pagelode/internal/config"
)

const rootDescription = "PageLode extracts Markdown, maps sites, discovers page data endpoints, and finds emails."

type commandSpec struct {
	name     string
	summary  string
	long     string
	usage    []string
	examples []string
	flags    func(*flag.FlagSet)
	aliases  map[string]string
}

func commandSpecs() []commandSpec {
	return []commandSpec{
		{
			name:    "batch",
			summary: "Scrape millions of URLs from a shared Postgres queue",
			long: `Queues URLs in Postgres and scrapes them with any number of processes on any number of
machines. "run" loads each URL over HTTP first and sends only the pages that need a
browser to the browser stage, which spreads renders over every PAGELODE_BROWSER_URL
node. A full or unreachable browser puts the URL back for later instead of failing it.
Pages go to rolling gzipped JSONL files in --out; the queue records where each one is.

Subcommands:
  import <file|->   add URLs or domains, one per line; duplicates are ignored
  run               scrape until interrupted, or until empty with --until-empty
  status            count URLs per state
  retry             move URLs back, by default from failed to pending

The queue URL comes from --queue or PAGELODE_QUEUE_URL. The flags below are for run.`,
			usage: []string{"pagelode batch import [--queue URL] <file|->", "pagelode batch run [flags]", "pagelode batch status [--queue URL]", "pagelode batch retry [--state failed] [--to pending]"},
			examples: []string{
				"export PAGELODE_QUEUE_URL=postgres://user:pass@host/db",
				"pagelode batch import urls.txt",
				"pagelode batch run --http-workers 400 --out /data/pages",
				"PAGELODE_BROWSER_URL=http://b1:8090,http://b2:8090 pagelode batch run --until-empty",
				"pagelode batch status",
			},
			flags: func(flags *flag.FlagSet) { defineBatchRunFlags(flags, config.Config{}) },
		},
		{
			name:    "discover",
			summary: "Find the data endpoints behind a page",
			long: `Loads a page in the browser service, captures its network traffic, and prints a JSON
report of endpoint groups, request and response field types, authentication signals,
pagination candidates, and data sources. With --har it analyzes a saved capture and
opens no browser.`,
			usage: []string{"pagelode discover [flags] <URL>", "pagelode discover --har <capture.har> [flags] [<URL>]"},
			examples: []string{
				"pagelode discover https://example.com/listings",
				"pagelode discover --wait-ms 3000 --verbose https://example.com/listings",
				"pagelode discover --har capture.har",
				"pagelode discover --profile account https://example.com/dashboard",
			},
			flags: func(flags *flag.FlagSet) { defineDiscoverFlags(flags) },
		},
		{
			name:    "emails",
			summary: "Find published email addresses on a site",
			long: `Crawls a site, prioritizing contact, team, and directory pages, and prints one unique
email address per line. It reads full HTML and captured JSON, and decodes common
obfuscation. Addresses are syntax-checked, not verified for deliverability.`,
			usage: []string{"pagelode emails [flags] <URL>"},
			examples: []string{
				"pagelode emails example.com",
				"pagelode emails --max-pages 10 --max-duration 15s example.com",
				"pagelode emails --verbose example.com",
				"pagelode emails --profile account https://app.example.com",
			},
			flags: func(flags *flag.FlagSet) { defineEmailsFlags(flags) },
		},
		{
			name:    "emails-bulk",
			summary: "Find email addresses for many sites and store them in SQLite",
			long: `Reads one URL or domain per line from a file, or from standard input with -, and crawls
the sites in parallel. Results go to a SQLite database. Sites already in the database
are skipped, so an interrupted run resumes when you run it again.`,
			usage: []string{"pagelode emails-bulk [flags] <file|->"},
			examples: []string{
				"pagelode emails-bulk sites.txt",
				"pagelode emails-bulk --workers 64 --db out/emails.sqlite sites.txt",
				"cat sites.txt | pagelode emails-bulk -",
			},
			flags: func(flags *flag.FlagSet) { defineEmailsBulkFlags(flags) },
		},
		{
			name:    "extract",
			summary: "Extract a page as clean Markdown (the default command)",
			long: `Loads one page through the fetch waterfall, which tries browser-like HTTP first and
uses the browser service only when the page needs it, and prints clean Markdown.
"pagelode <URL>" is shorthand for "pagelode extract <URL>".`,
			usage: []string{"pagelode extract [flags] <URL>"},
			examples: []string{
				"pagelode example.com",
				"pagelode extract https://example.com/article",
				"pagelode --json example.com",
			},
			flags:   func(flags *flag.FlagSet) { defineExtractFlags(flags) },
			aliases: map[string]string{"json": "j"},
		},
		{
			name:    "map",
			summary: "List a site's pages, page templates, and script endpoints",
			long: `Reads robots.txt, sitemaps (including .xml.gz), and page links breadth-first from the
start page to list the URLs of one site. Every URL is listed, but only a sample of each
URL shape is fetched for more links, near-duplicate pages are not expanded, and pages
robots.txt disallows are never fetched. --json adds the source of each URL, path
templates such as /blog/*, and endpoint paths found in scripts.`,
			usage: []string{"pagelode map [flags] <URL>"},
			examples: []string{
				"pagelode map example.com",
				"pagelode map --depth 3 --limit 2000 example.com",
				"pagelode map --include '/blog/*' --exclude '/blog/tag/*' example.com",
				"pagelode map --render auto --explore https://app.example.com",
				"pagelode map --json example.com",
			},
			flags: func(flags *flag.FlagSet) { defineMapFlags(flags) },
		},
		{
			name:    "profile",
			summary: "Manage saved signed-in browser profiles",
			long: `login opens a visible browser session on the browser service at the URL. Sign in, then
press Enter in the terminal to save the profile under <name> and close the session.
Use it later with --profile <name> on discover or emails.`,
			usage: []string{"pagelode profile login <name> <URL>"},
			examples: []string{
				"pagelode profile login account https://example.com/login",
				"pagelode discover --profile account example.com",
			},
		},
		{
			name:    "serve",
			summary: "Run the HTTP API",
			long: `Starts the HTTP API on PORT (default 8083) with POST /extract, /map, /discover, and
/emails, and GET /healthz.`,
			usage:    []string{"pagelode serve"},
			examples: []string{"PORT=8083 pagelode serve"},
		},
		{
			name:    "version",
			summary: "Print the version",
			usage:   []string{"pagelode version"},
		},
	}
}

func findCommand(name string) (commandSpec, bool) {
	for _, spec := range commandSpecs() {
		if spec.name == name {
			return spec, true
		}
	}
	return commandSpec{}, false
}

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	command := "pagelode"
	if name != "" {
		command += " " + name
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		fmt.Fprintf(output, "Run '%s --help' for usage.\n", command)
	}
	return flags
}

func asksForHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-h" || arg == "--help" || arg == "-help" {
			return true
		}
	}
	return false
}

func handleHelp(args []string, stdout io.Writer) (bool, error) {
	switch args[0] {
	case "help":
		return true, writeHelp(stdout, args[1:])
	case "-h", "--help", "-help":
		writeRootHelp(stdout)
		return true, nil
	}
	if spec, ok := findCommand(args[0]); ok {
		if asksForHelp(args[1:]) {
			writeCommandHelp(stdout, spec)
			return true, nil
		}
		return false, nil
	}
	if strings.HasPrefix(args[0], "-") && asksForHelp(args) {
		writeRootHelp(stdout)
		return true, nil
	}
	return false, nil
}

func writeHelp(output io.Writer, topic []string) error {
	if len(topic) == 0 {
		writeRootHelp(output)
		return nil
	}
	if topic[0] == "help" {
		writeHelpCommandHelp(output)
		return nil
	}
	spec, ok := findCommand(topic[0])
	if !ok {
		return fmt.Errorf("unknown help topic %q; run 'pagelode help' for the list of commands", topic[0])
	}
	writeCommandHelp(output, spec)
	return nil
}

func writeRootHelp(output io.Writer) {
	fmt.Fprintln(output, rootDescription)
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  pagelode <URL> [flags]")
	fmt.Fprintln(output, "  pagelode [command]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Examples:")
	for _, example := range []string{"pagelode example.com", "pagelode --json example.com", "pagelode map example.com", "pagelode emails example.com"} {
		fmt.Fprintf(output, "  %s\n", example)
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Available Commands:")
	names := make([][2]string, 0, 10)
	for _, spec := range commandSpecs() {
		names = append(names, [2]string{spec.name, spec.summary})
	}
	names = append(names, [2]string{"help", "Help about any command"})
	sort.Slice(names, func(i, j int) bool { return names[i][0] < names[j][0] })
	width := 0
	for _, entry := range names {
		width = max(width, len(entry[0]))
	}
	for _, entry := range names {
		fmt.Fprintf(output, "  %-*s  %s\n", width, entry[0], entry[1])
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	root := flag.NewFlagSet("pagelode", flag.ContinueOnError)
	defineExtractFlags(root)
	writeFlagLines(output, root, map[string]string{"json": "j"}, flagLine{short: "h", long: "help", usage: "help for pagelode"}, flagLine{short: "v", long: "version", usage: "print the version"})
	fmt.Fprintln(output)
	fmt.Fprintln(output, `Use "pagelode [command] --help" for more information about a command.`)
	fmt.Fprintln(output, "Settings come from environment variables; see docs/configuration.md.")
}

func writeCommandHelp(output io.Writer, spec commandSpec) {
	if spec.long != "" {
		fmt.Fprintln(output, spec.long)
		fmt.Fprintln(output)
	} else {
		fmt.Fprintln(output, spec.summary)
		fmt.Fprintln(output)
	}
	fmt.Fprintln(output, "Usage:")
	for _, usage := range spec.usage {
		fmt.Fprintf(output, "  %s\n", usage)
	}
	if len(spec.examples) > 0 {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Examples:")
		for _, example := range spec.examples {
			fmt.Fprintf(output, "  %s\n", example)
		}
	}
	if spec.flags != nil {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Flags:")
		flags := flag.NewFlagSet(spec.name, flag.ContinueOnError)
		spec.flags(flags)
		writeFlagLines(output, flags, spec.aliases, flagLine{short: "h", long: "help", usage: "help for " + spec.name})
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, `Use "pagelode help" to list every command.`)
}

func writeHelpCommandHelp(output io.Writer) {
	fmt.Fprintln(output, "Help provides help for any command in the application.")
	fmt.Fprintln(output, "Type pagelode help [command] for full details.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  pagelode help [command]")
}

type flagLine struct {
	short string
	long  string
	kind  string
	usage string
}

func writeFlagLines(output io.Writer, flags *flag.FlagSet, aliases map[string]string, extra ...flagLine) {
	hidden := map[string]bool{}
	for _, alias := range aliases {
		hidden[alias] = true
	}
	lines := append([]flagLine{}, extra...)
	flags.VisitAll(func(entry *flag.Flag) {
		if hidden[entry.Name] {
			return
		}
		kind, usage := flag.UnquoteUsage(entry)
		if !zeroDefault(entry.DefValue) {
			defaultValue := entry.DefValue
			if kind == "string" {
				defaultValue = strconv.Quote(defaultValue)
			}
			usage += fmt.Sprintf(" (default %s)", defaultValue)
		}
		lines = append(lines, flagLine{short: aliases[entry.Name], long: entry.Name, kind: kind, usage: usage})
	})
	slices.SortFunc(lines, func(a, b flagLine) int { return strings.Compare(a.long, b.long) })

	left := make([]string, len(lines))
	width := 0
	for i, line := range lines {
		prefix := "    "
		if line.short != "" {
			prefix = "-" + line.short + ", "
		}
		left[i] = prefix + "--" + line.long
		if line.kind != "" {
			left[i] += " " + line.kind
		}
		width = max(width, len(left[i]))
	}
	for i, line := range lines {
		fmt.Fprintf(output, "  %-*s   %s\n", width, left[i], line.usage)
	}
}

func zeroDefault(value string) bool {
	switch value {
	case "", "0", "false", "0s":
		return true
	}
	return false
}

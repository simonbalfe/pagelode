package cli

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestRunShowsUsageWithoutArguments(t *testing.T) {
	var output bytes.Buffer
	if err := Run(nil, &output, &output); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "pagelode <URL>") {
		t.Fatalf("Run() output = %q, want usage", output.String())
	}
}

func TestRunShowsVersion(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"version"}, &output, &output); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := output.String(), "PageLode "+version+"\n"; got != want {
		t.Fatalf("Run() output = %q, want %q", got, want)
	}
}

func TestAttemptDetail(t *testing.T) {
	if got := attemptDetail(nil); got != "" {
		t.Fatalf("attemptDetail(nil) = %q, want empty", got)
	}
}

func TestRootHelpListsEveryCommand(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"--help"}, &output, &output); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, name := range []string{"emails", "extract", "help", "map", "profile", "serve", "version"} {
		if !strings.Contains(output.String(), "\n  "+name+" ") {
			t.Errorf("root help does not list %q:\n%s", name, output.String())
		}
	}
	if !strings.Contains(output.String(), `Use "pagelode [command] --help"`) {
		t.Errorf("root help has no pointer to command help:\n%s", output.String())
	}
}

func TestCommandHelpShowsFlagsWithoutLoadingConfiguration(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	var output bytes.Buffer
	if err := Run([]string{"map", "--help"}, &output, &output); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, want := range []string{"Usage:\n  pagelode map [flags] <URL>", "Examples:", "--depth int", "--render string", `(default "never")`, "--max-duration duration", "(default 30s)", "-h, --help"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("map help is missing %q:\n%s", want, output.String())
		}
	}
}

func TestHelpCommandMatchesCommandHelpFlag(t *testing.T) {
	for _, spec := range commandSpecs() {
		var viaHelp, viaFlag bytes.Buffer
		words := strings.Fields(spec.name)
		if err := Run(append([]string{"help"}, words...), &viaHelp, &viaHelp); err != nil {
			t.Fatalf("help %s: %v", spec.name, err)
		}
		if err := Run(append(words, "--help"), &viaFlag, &viaFlag); err != nil {
			t.Fatalf("%s --help: %v", spec.name, err)
		}
		if viaHelp.String() != viaFlag.String() {
			t.Errorf("help %s and %s --help differ", spec.name, spec.name)
		}
	}
}

func TestCommandHelpDocumentsEveryDefinedFlag(t *testing.T) {
	for _, spec := range commandSpecs() {
		if spec.flags == nil {
			continue
		}
		var output bytes.Buffer
		writeCommandHelp(&output, spec)
		flags := newFlagSet(spec.name, &output)
		spec.flags(flags)
		hidden := map[string]bool{}
		for _, alias := range spec.aliases {
			hidden[alias] = true
		}
		flags.VisitAll(func(entry *flag.Flag) {
			if !hidden[entry.Name] && !strings.Contains(output.String(), "--"+entry.Name) {
				t.Errorf("%s help does not document --%s", spec.name, entry.Name)
			}
		})
	}
}

func TestHelpRejectsUnknownTopics(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"help", "bogus"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), `unknown help topic "bogus"`) {
		t.Fatalf("Run() error = %v, want unknown help topic", err)
	}
}

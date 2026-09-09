// Command vanityrig finds vanity .onion addresses.
//
// It always checks feasibility before searching — committing hours of compute
// to an unachievable pattern is the most expensive mistake it can prevent —
// and asks for confirmation before it starts. When the caller
// hasn't said which match mode they want, it compares all three (prefix,
// suffix, anywhere) instead of guessing one.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// version is set at build time via -ldflags "-X main.version=...". Release
// binaries get their real tag; anything built with plain `go build` or
// `go install` stays "dev" rather than lying about which release it is.
var version = "dev"

const usage = `vanityrig - find a vanity .onion address

Usage:
  vanityrig                                   guided prompts — just type answers
  vanityrig <word> [word...] [flags]          the same thing, one command

Tells you the real cost first, then asks before it starts searching. Pass
-match to pick a position yourself; otherwise it compares prefix, suffix and
anywhere and asks which you want.

Examples:
  vanityrig
  vanityrig vanityrig
  vanityrig vanityrig -match anywhere
  vanityrig vanityrig ritrigvan -stop-after 3

Flags:
  -match string     where the word may appear: prefix, suffix, anywhere (default: ask)
  -threads int      CPU threads to use (default: all cores)
  -out string       where to save found keys (default ~/.vanityrig/keys)
  -stop-after int   stop once this many matches are found (default 0, never)
  -rate float       assumed combined keys/sec, used for the estimate (default 22.2M)
  -budget duration  longest search you'd accept, for alternatives (default 24h)
  -check            show the estimate and exit, don't offer to search
  -y                skip every prompt and start immediately (recommended mode, if -match is unset)
  -plain            print plain lines instead of the live dashboard
  -version          print the version and exit
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		// A real terminal with nothing typed yet is someone who wants to be
		// guided, not someone who forgot a flag — walk them through it instead
		// of dumping usage text. Anything non-interactive (a script, a pipe)
		// has no one to answer prompts, so it gets the usage text and exits.
		if isInputTerminal() {
			return runWizard()
		}
		fmt.Print(usage)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return 0
	}
	if args[0] == "-version" || args[0] == "--version" {
		fmt.Println("vanityrig", version)
		return 0
	}

	fs := flag.NewFlagSet("vanityrig", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	mode := fs.String("match", "", "prefix | suffix | anywhere (default: ask)")
	rate := fs.Float64("rate", 22.2e6, "assumed combined keys/sec, for the estimate")
	budget := fs.Duration("budget", 24*time.Hour, "acceptable search length")
	threads := fs.Int("threads", 0, "cpu threads (0 = all)")
	out := fs.String("out", "", "output directory")
	stopAfter := fs.Int("stop-after", 0, "stop after N matches")
	plain := fs.Bool("plain", false, "plain output instead of the dashboard")
	enginePath := fs.String("mkp224o", "", "path to the mkp224o binary")
	checkOnly := fs.Bool("check", false, "show the estimate and exit")
	yes := fs.Bool("y", false, "skip every prompt and start immediately")

	patterns, rest := splitPatterns(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	patterns = append(patterns, fs.Args()...)

	if len(patterns) == 0 {
		fmt.Fprint(os.Stderr, "vanityrig needs at least one word to search for\n\n"+usage)
		return 2
	}
	if *rate <= 0 {
		fmt.Fprintln(os.Stderr, "error: -rate must be greater than zero")
		return 2
	}

	// Malformed input (bad characters, over-length) is a typo, so it stops here —
	// there is no meaningful estimate for a pattern that cannot be parsed.
	//
	// A well-formed but unsatisfiable pattern is NOT stopped here: this is an
	// informational report, and refusing to inform contradicts the whole point of
	// it. It reports honestly (probability zero, "never") and lets the user
	// decide: informed consent, not gatekeeping.
	var malformed bool
	for _, p := range patterns {
		if err := vanity.PreflightSyntax(p); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			malformed = true
		}
	}
	if malformed {
		return 2
	}

	if *mode == "" {
		return runCompare(patterns, *rate, *threads, *out, *stopAfter, *plain, *enginePath, *checkOnly, *yes)
	}

	m, err := vanity.ParseMatchMode(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	est := vanity.NewEstimate(patterns, m, *rate)
	vanity.WriteReport(os.Stdout, est, nil, *budget)
	fmt.Println()

	if est.Probability <= 0 {
		return 1
	}
	if *checkOnly {
		return 0
	}
	if !*yes {
		if !isInputTerminal() {
			fmt.Println("Not running non-interactively without -y.")
			return 0
		}
		if !askConfirmStart(est.Verdict) {
			fmt.Println("Not starting.")
			return 0
		}
	}

	return startSearch(patterns, m, *threads, *out, *stopAfter, *plain, *enginePath)
}

// runWizard is what a bare `vanityrig` runs into a real terminal: one
// continuous full-screen form (word, save location, mode, confirm) rather
// than a sequence of separate prompts — see runWizardForm.
func runWizard() int {
	patterns, out, mode, ok := runWizardForm()
	if !ok {
		fmt.Println("Not starting.")
		return 0
	}
	return startSearch(patterns, mode, 0, out, 0, false, "")
}

// runCompare handles the common case: the caller gave a word but no -match,
// so it shows what each position actually costs and asks which one to run,
// instead of silently assuming prefix.
func runCompare(patterns []string, rate float64, threads int, out string, stopAfter int, plain bool, enginePath string, checkOnly, yes bool) int {
	cmp := vanity.CompareModes(patterns, rate)

	if cmp.Best == "" {
		vanity.WriteModeComparison(os.Stdout, cmp)
		fmt.Println("None of the match modes can ever produce this exact word. Try a different word.")
		return 1
	}
	if checkOnly {
		vanity.WriteModeComparison(os.Stdout, cmp)
		return 0
	}

	chosen := cmp.Best
	if !yes {
		if !isInputTerminal() {
			vanity.WriteModeComparison(os.Stdout, cmp)
			fmt.Println("Not running non-interactively without -y.")
			return 0
		}
		m, ok := askModeAndConfirm(cmp)
		if !ok {
			fmt.Println("Not starting.")
			return 0
		}
		chosen = m
	}

	return startSearch(patterns, chosen, threads, out, stopAfter, plain, enginePath)
}

// isInputTerminal reports whether stdin is an interactive terminal, so a
// confirmation prompt is never silently guessed at in a script or pipeline.
func isInputTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

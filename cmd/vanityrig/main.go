// Command vanityrig is a distributed vanity .onion address search tool.
//
// This build implements the Feasibility Advisor (PROJECT.md §4a) — the first
// thing the tool ships, because committing hours of compute to an unachievable
// pattern is the most expensive mistake it can prevent.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rishibaghel25/vanityrig/internal/vanity"
)

const usage = `vanityrig - vanity .onion address tooling

Usage:
  vanityrig search   <pattern> [pattern...]   run a search with a live dashboard
  vanityrig estimate <pattern> [pattern...]   check how long it would take first

Get started:
  vanityrig estimate borderx     see what it will cost
  vanityrig search   borderx     go and find it

Both accept -match prefix|suffix|anywhere (default prefix).
Run 'vanityrig search -h' or 'vanityrig estimate -h' for the full flag list.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "estimate":
		os.Exit(runEstimate(os.Args[2:]))
	case "search":
		os.Exit(runSearch(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runEstimate(args []string) int {
	fs := flag.NewFlagSet("estimate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	mode := fs.String("match", "prefix", "prefix | suffix | anywhere")
	rate := fs.Float64("rate", 22.2e6, "combined keys/sec")
	budget := fs.Duration("budget", 24*time.Hour, "acceptable search length")

	// Allow flags before or after the patterns.
	var patterns []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		patterns = append(patterns, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	patterns = append(patterns, fs.Args()...)

	if len(patterns) == 0 {
		fmt.Fprint(os.Stderr, "estimate needs at least one pattern\n\n"+usage)
		return 2
	}

	m, err := vanity.ParseMatchMode(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if *rate <= 0 {
		fmt.Fprintln(os.Stderr, "error: -rate must be greater than zero")
		return 2
	}

	// Malformed input (bad characters, over-length) is a typo, so it stops here —
	// there is no meaningful estimate for a pattern that cannot be parsed.
	//
	// A well-formed but unsatisfiable pattern is NOT stopped: estimate is an
	// informational command, and refusing to inform contradicts the whole point of
	// the advisor. It reports honestly (probability zero, "never") and lets the
	// user decide. Gating belongs at search start, behind an explicit override —
	// see PROJECT.md §4a: informed consent, not gatekeeping.
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

	est := vanity.NewEstimate(patterns, m, *rate)

	// Always lay out the options, including the one that was asked for, so the
	// user compares trade-offs rather than being handed a correction. Skipped only
	// when the request is already quick, where there is nothing to weigh up.
	var alts []vanity.Alternative
	if est.Verdict != vanity.VerdictTrivial {
		alts = vanity.Options(patterns[0], m, *rate, *budget)
	}

	vanity.WriteReport(os.Stdout, est, alts, *budget)
	fmt.Println()

	if est.Probability <= 0 {
		return 1
	}
	return 0
}

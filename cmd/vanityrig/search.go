package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rishibaghel25/vanityrig/internal/runner"
	"github.com/rishibaghel25/vanityrig/internal/tui"
	"github.com/rishibaghel25/vanityrig/internal/vanity"
)

const searchUsage = `vanityrig search - run a vanity address search

Usage:
  vanityrig search <pattern> [pattern...] [flags]

Flags:
  -match string   where the pattern may appear: prefix, suffix, anywhere (default "prefix")
  -threads int    CPU threads to use (default: all cores)
  -out string     where to save found keys (default ~/.vanityrig/keys)
  -stop-after int stop once this many matches are found (default 0, never)
  -plain          print plain lines instead of the live dashboard

Examples:
  vanityrig search borderx
  vanityrig search borderland -match anywhere
  vanityrig search borderx bordery -stop-after 3
`

func runSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	mode := fs.String("match", "prefix", "prefix | suffix | anywhere")
	threads := fs.Int("threads", 0, "cpu threads (0 = all)")
	out := fs.String("out", "", "output directory")
	stopAfter := fs.Int("stop-after", 0, "stop after N matches")
	plain := fs.Bool("plain", false, "plain output instead of the dashboard")
	enginePath := fs.String("mkp224o", "", "path to the mkp224o binary")

	patterns, rest := splitPatterns(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	patterns = append(patterns, fs.Args()...)

	if len(patterns) == 0 {
		fmt.Fprint(os.Stderr, "search needs at least one pattern\n\n"+searchUsage)
		return 2
	}

	m, err := vanity.ParseMatchMode(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	// Validate before doing anything else, so an impossible pattern is explained
	// here rather than discovered after the search has been left running.
	for _, p := range patterns {
		if err := vanity.Validate(p, m); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			fmt.Fprintf(os.Stderr, "\nRun 'vanityrig estimate %s -match %s' to see the options.\n", p, m)
			return 1
		}
	}

	if *out == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: cannot determine home directory; pass -out")
			return 2
		}
		*out = filepath.Join(home, ".vanityrig", "keys")
	}

	r, err := runner.New(runner.Config{
		Patterns:   patterns,
		Mode:       m,
		Threads:    *threads,
		OutputDir:  *out,
		EnginePath: *enginePath,
		StopAfter:  *stopAfter,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	// Warn before starting when the search is a very long one. Starting a
	// multi-year search silently would be the tool's worst failure mode.
	pre := vanity.NewEstimate(patterns, m, estimateRateFor(r.EngineName()))
	if pre.Verdict == vanity.VerdictHard || pre.Verdict == vanity.VerdictInfeasible {
		fmt.Fprintf(os.Stderr, "note: this is a long search (typically %s at this engine's speed).\n",
			roughDuration(pre))
		fmt.Fprintf(os.Stderr, "      run 'vanityrig estimate %s -match %s' to compare faster options.\n\n",
			patterns[0], m)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl-C must stop the search cleanly and save progress, whether or not the
	// dashboard is running.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	if *plain || !isTerminal() {
		runPlain(r, ctx)
	} else {
		p := tea.NewProgram(tui.New(r, cancel), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			// A dashboard failure must not take the search down with it; fall
			// back to plain output rather than exiting.
			fmt.Fprintln(os.Stderr, "dashboard unavailable:", err)
			runPlain(r, ctx)
		}
		cancel()
	}

	<-r.Done()
	if err := <-errCh; err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	printSummary(r.Snapshot())
	return 0
}

// splitPatterns takes leading non-flag arguments as patterns, so flags may come
// before or after them.
func splitPatterns(args []string) (patterns, rest []string) {
	rest = args
	for len(rest) > 0 && (len(rest[0]) == 0 || rest[0][0] != '-') {
		patterns = append(patterns, rest[0])
		rest = rest[1:]
	}
	return patterns, rest
}

// estimateRateFor is a rough throughput used only for the pre-flight warning,
// before any real measurement exists. The dashboard switches to measured rates
// as soon as the first sample lands.
func estimateRateFor(engineName string) float64 {
	if engineName == "mkp224o" {
		return 18.5e6
	}
	return 100e3
}

func roughDuration(e vanity.Estimate) string {
	if vanity.Saturated(e.P50) {
		return fmt.Sprintf("%.0f years", e.YearsFor(0.5))
	}
	d := e.P50
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	default:
		return fmt.Sprintf("%.1f years", d.Hours()/24/365.25)
	}
}

// runPlain is the no-dashboard path: used with -plain, and automatically when
// output is not a terminal so that logs and pipes stay readable.
func runPlain(r *runner.Runner, ctx context.Context) {
	snap := r.Snapshot()
	fmt.Printf("searching for %v (%s match) using %s\n", snap.Patterns, snap.Mode, snap.EngineName)
	fmt.Printf("saving keys to %s\n\n", snap.OutputDir)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	seen := 0

	for {
		select {
		case <-r.Done():
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s := r.Snapshot()
			fmt.Printf("[%s] %s keys tried, %s/sec\n",
				time.Now().Format("15:04:05"),
				humanCount(s.KeysTried), humanCount(s.KeysPerSec))
			for ; seen < len(s.Matches); seen++ {
				fmt.Printf("FOUND %s.onion -> %s\n", s.Matches[seen].Address, s.Matches[seen].Dir)
			}
		}
	}
}

func printSummary(s runner.Snapshot) {
	fmt.Println()
	if len(s.Matches) == 0 {
		fmt.Printf("No matches yet. Tried %s keys over %s.\n",
			humanCount(s.KeysTried), roughElapsed(s.Elapsed))
		fmt.Println("Progress is saved — running the same search again continues from here.")
		return
	}
	fmt.Printf("Found %d address(es):\n", len(s.Matches))
	for _, m := range s.Matches {
		fmt.Printf("  %s.onion\n    %s\n", m.Address, m.Dir)
	}
	fmt.Println()
	fmt.Println("The hs_ed25519_secret_key file in each directory IS the address —")
	fmt.Println("anyone holding it can serve that site. Keep it private.")
}

func humanCount(f float64) string {
	switch {
	case f <= 0:
		return "0"
	case f < 1e6:
		return fmt.Sprintf("%.0f", f)
	case f < 1e9:
		return fmt.Sprintf("%.2fM", f/1e6)
	case f < 1e12:
		return fmt.Sprintf("%.2fB", f/1e9)
	default:
		return fmt.Sprintf("%.2fT", f/1e12)
	}
}

func roughElapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1fh", d.Hours())
	}
}

// isTerminal reports whether stdout is an interactive terminal. The dashboard is
// meaningless when piped to a file, so it is skipped automatically rather than
// filling logs with escape codes.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

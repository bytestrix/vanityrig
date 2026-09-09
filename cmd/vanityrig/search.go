package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bytestrix/vanityrig/internal/runner"
	"github.com/bytestrix/vanityrig/internal/tui"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// startSearch runs the search itself, after the feasibility report has already
// been shown and confirmed (see run in main.go). It validates the pattern
// again defensively, then drives either the live dashboard or plain output.
func startSearch(patterns []string, m vanity.MatchMode, threads int, out string, stopAfter int, plain bool, enginePath string) int {
	// Validate before doing anything else, so an impossible pattern is explained
	// here rather than discovered after the search has been left running.
	for _, p := range patterns {
		if err := vanity.Validate(p, m); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	if out == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: cannot determine home directory; pass -out")
			return 2
		}
		out = filepath.Join(home, ".vanityrig", "keys")
	}

	r, err := runner.New(runner.Config{
		Patterns:   patterns,
		Mode:       m,
		Threads:    threads,
		OutputDir:  out,
		EnginePath: enginePath,
		StopAfter:  stopAfter,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
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

	if plain || !isTerminal() {
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

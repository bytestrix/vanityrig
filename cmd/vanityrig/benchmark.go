package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/bytestrix/vanityrig/internal/engine"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// runBenchmark measures real, observed keys/sec for the engine this machine
// would actually use — the same throughput samples the live dashboard
// consumes, not a synthetic op-count — by running it for a fixed duration
// against a pattern long enough that a real match essentially never
// interrupts the timing. It never writes a key or touches -out.
func runBenchmark(mode vanity.MatchMode, threads int, dur time.Duration, enginePath string) int {
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	eng, note := engine.Select(mode, enginePath)
	if ok, reason := eng.Available(); !ok {
		fmt.Fprintf(os.Stderr, "error: %s engine unavailable: %s\n", eng.Name(), reason)
		return 1
	}

	tmp, err := os.MkdirTemp("", "vanityrig-benchmark")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	fmt.Printf("Benchmarking %s (%s) — %s mode, %d threads, %s...\n", eng.Name(), note, mode, threads, dur)

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	events, err := eng.Run(ctx, engine.Config{
		// Long enough that a real match essentially never interrupts timing —
		// this measures raw generation+check speed, not luck.
		Patterns:   []string{"zzzzzzzzzzzzzzzzzzzz"},
		Mode:       mode,
		Threads:    threads,
		OutputDir:  tmp,
		BinaryPath: enginePath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	var total float64
	var samples int
	for ev := range events {
		switch ev.Kind {
		case engine.EventSample:
			total += ev.KeysPerSec
			samples++
			fmt.Printf("  sample: %s/sec\n", humanCountCLI(ev.KeysPerSec))
		case engine.EventError:
			fmt.Fprintln(os.Stderr, "warning:", ev.Err)
		}
	}

	if samples == 0 {
		fmt.Fprintln(os.Stderr, "error: no throughput samples arrived — try a longer -benchmark duration")
		return 1
	}
	avg := total / float64(samples)
	fmt.Printf("\nAverage: %s keys/sec across %d threads (%s keys/sec/thread)\n",
		humanCountCLI(avg), threads, humanCountCLI(avg/float64(threads)))
	return 0
}

func humanCountCLI(f float64) string {
	switch {
	case f <= 0:
		return "0"
	case f < 1000:
		return fmt.Sprintf("%.0f", f)
	case f < 1e6:
		return fmt.Sprintf("%.1fK", f/1e3)
	case f < 1e9:
		return fmt.Sprintf("%.2fM", f/1e6)
	default:
		return fmt.Sprintf("%.2fB", f/1e9)
	}
}

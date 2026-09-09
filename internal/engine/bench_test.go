package engine

import (
	"context"
	"testing"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// benchThroughput measures real observed keys/sec (the same samples the live
// dashboard consumes), not a synthetic op-count — the two pure-Go engines
// have different per-candidate costs (one full independent scalar mult vs
// one cheap point addition plus an amortised batch inversion), so comparing
// them at all only means something measured end to end, the way a user
// actually experiences it.
func benchThroughput(b *testing.B, eng Engine, mode vanity.MatchMode) {
	cfg := Config{
		// Long enough that a real match essentially never interrupts timing.
		Patterns:  []string{"zzzzzzzzzzzzzzzzzzzz"},
		Mode:      mode,
		Threads:   1,
		OutputDir: b.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, err := eng.Run(ctx, cfg)
	if err != nil {
		b.Fatal(err)
	}
	var total float64
	var samples int
	for ev := range events {
		if ev.Kind == EventSample {
			total += ev.KeysPerSec
			samples++
		}
	}
	if samples == 0 {
		b.Fatal("no throughput samples arrived")
	}
	b.ReportMetric(total/float64(samples), "keys/sec/thread")
}

func BenchmarkNativeThroughputPrefix(b *testing.B) { benchThroughput(b, Native{}, vanity.MatchPrefix) }
func BenchmarkSymmetryThroughputPrefix(b *testing.B) {
	benchThroughput(b, Symmetry{}, vanity.MatchPrefix)
}

func BenchmarkNativeThroughputAnywhere(b *testing.B) {
	benchThroughput(b, Native{}, vanity.MatchAnywhere)
}
func BenchmarkSymmetryThroughputAnywhere(b *testing.B) {
	benchThroughput(b, Symmetry{}, vanity.MatchAnywhere)
}

func BenchmarkMkp224oThroughputPrefix(b *testing.B) {
	if ok, _ := (Mkp224o{}).Available(); !ok {
		b.Skip("mkp224o not installed on this machine")
	}
	benchThroughput(b, Mkp224o{}, vanity.MatchPrefix)
}

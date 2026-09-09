package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

func skipWithoutMkp224o(t *testing.T) string {
	t.Helper()
	bin, err := Mkp224o{}.Locate()
	if err != nil {
		t.Skip("mkp224o not installed on this machine")
	}
	return bin
}

func TestStatLineParsing(t *testing.T) {
	// The exact format emitted by mkp224o, captured from a real run.
	line := ">calc/sec:18832402.516944, succ/sec:0.000000, rest/sec:0.000000, elapsed:90.171354sec"
	m := statLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("failed to parse a real mkp224o progress line: %s", line)
	}
	if m[1] != "18832402.516944" {
		t.Errorf("rate = %q, want 18832402.516944", m[1])
	}
	if m[2] != "90.171354" {
		t.Errorf("elapsed = %q, want 90.171354", m[2])
	}
}

func TestStatLineIgnoresOtherOutput(t *testing.T) {
	for _, line := range []string{
		"set workdir: /home/x/borderland_vanity/",
		`filter "600" is not valid base32 string`,
		"",
		"borderx6srnnpcnl24ppsnmzcdqch5pwnx45lp3lxo6inbb3m4jadcid.onion",
	} {
		if statLine.MatchString(line) {
			t.Errorf("line should not parse as a stat line: %q", line)
		}
	}
}

// mkp224o cannot match suffixes or substrings, and must say so plainly rather
// than starting a search that silently only looks at prefixes.
func TestMkp224oRefusesNonPrefixModes(t *testing.T) {
	for _, mode := range []vanity.MatchMode{vanity.MatchSuffix, vanity.MatchAnywhere} {
		_, err := Mkp224o{}.Run(context.Background(), Config{
			Patterns:  []string{"borderlad"},
			Mode:      mode,
			OutputDir: t.TempDir(),
		})
		if err == nil {
			t.Errorf("%s mode: expected a refusal, got none", mode)
			continue
		}
		if !strings.Contains(err.Error(), "native") {
			t.Errorf("%s mode: error should point at the native engine, got: %v", mode, err)
		}
	}
}

func TestEngineSelection(t *testing.T) {
	mkAvailable, _ := Mkp224o{}.Available()

	e, note := Select(vanity.MatchPrefix, "")
	if mkAvailable {
		if e.Name() != "mkp224o" {
			t.Errorf("prefix search picked %s, want mkp224o when it is installed", e.Name())
		}
	} else if e.Name() != "native" {
		t.Errorf("prefix search picked %s, want native fallback", e.Name())
	}
	if note == "" {
		t.Error("engine choice must come with an explanation for the user")
	}

	// Non-prefix modes must always land on the native engine, whatever is
	// installed, and must say why.
	for _, mode := range []vanity.MatchMode{vanity.MatchSuffix, vanity.MatchAnywhere} {
		e, note := Select(mode, "")
		if e.Name() != "native" {
			t.Errorf("%s mode picked %s, want native", mode, e.Name())
		}
		if !strings.Contains(note, "mkp224o") {
			t.Errorf("%s mode: note should explain why mkp224o is not used, got %q", mode, note)
		}
	}
}

// End-to-end against the real binary: a two-character prefix is found almost
// immediately, exercising process start, stat parsing, match detection and
// shutdown.
func TestMkp224oEndToEnd(t *testing.T) {
	skipWithoutMkp224o(t)

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := Mkp224o{}.Run(ctx, Config{
		Patterns:  []string{"ab"},
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	var match *Match
	for ev := range events {
		switch ev.Kind {
		case EventMatch:
			m := ev.Match
			match = &m
			cancel()
		case EventError:
			t.Errorf("engine error: %v", ev.Err)
		}
		if match != nil {
			break
		}
	}

	if match == nil {
		t.Fatal("no match found within the timeout")
	}
	if !strings.HasPrefix(match.Address, "ab") {
		t.Errorf("address %q does not start with the pattern", match.Address)
	}
	// mkp224o writes the key files itself; confirm the directory we reported is
	// the one that actually holds them.
	if _, err := os.Stat(filepath.Join(match.Dir, "hs_ed25519_secret_key")); err != nil {
		t.Errorf("reported directory has no secret key: %v", err)
	}
}

// Throughput reporting is tested separately from match detection: a short
// pattern is found before the first stat line is even emitted, so a combined test
// would race. This uses a pattern too long to hit, so only samples arrive.
func TestMkp224oReportsThroughput(t *testing.T) {
	skipWithoutMkp224o(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	events, err := Mkp224o{}.Run(ctx, Config{
		Patterns:  []string{"zzzzzzzz"}, // 40 bits: will not be found here
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for ev := range events {
		if ev.Kind != EventSample {
			continue
		}
		if ev.KeysPerSec <= 0 {
			t.Errorf("throughput reported as %v, want positive", ev.KeysPerSec)
		}
		if ev.Interval <= 0 {
			t.Errorf("interval %v must be positive so totals can be integrated", ev.Interval)
		}
		// Sanity-check the magnitude: a real mkp224o run is millions per second,
		// so a tiny value would mean the rate and elapsed fields got transposed.
		if ev.KeysPerSec < 1000 {
			t.Errorf("throughput %v is implausibly low; check field parsing", ev.KeysPerSec)
		}
		cancel()
		return
	}
	t.Fatal("mkp224o never reported throughput")
}

func TestMkp224oStopsOnCancel(t *testing.T) {
	skipWithoutMkp224o(t)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := Mkp224o{}.Run(ctx, Config{
		Patterns:  []string{"zzzzzzzz"},
		Mode:      vanity.MatchPrefix,
		Threads:   1,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("mkp224o did not shut down within 10s of cancellation")
	}
}

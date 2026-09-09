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

func TestMatcherModes(t *testing.T) {
	const addr = "borderx6srnnpcnl24ppsnmzcdqch5pwnx45lp3lxo6inbb3m4jadcid"

	cases := []struct {
		pattern string
		mode    vanity.MatchMode
		want    bool
	}{
		{"borderx", vanity.MatchPrefix, true},
		{"bordery", vanity.MatchPrefix, false},
		{"jadcid", vanity.MatchSuffix, true},
		{"borderx", vanity.MatchSuffix, false},
		{"nmzcdq", vanity.MatchAnywhere, true},
		{"borderx", vanity.MatchAnywhere, true}, // a prefix is also "anywhere"
		{"zzzzzz", vanity.MatchAnywhere, false},
	}
	for _, c := range cases {
		m, err := newMatcher([]string{c.pattern}, c.mode)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.pattern, c.mode, err)
		}
		if got := m.matches(addr); got != c.want {
			t.Errorf("%q in %s mode = %v, want %v", c.pattern, c.mode, got, c.want)
		}
	}
}

func TestMatcherIsAnOrAcrossPatterns(t *testing.T) {
	m, err := newMatcher([]string{"zzz", "borderx", "qqq"}, vanity.MatchPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !m.matches("borderx6srnnpcnl24ppsnmzcdqch5pwnx45lp3lxo6inbb3m4jadcid") {
		t.Error("any one pattern matching should be enough")
	}
}

func TestMatcherRejectsEmptyAndBadMode(t *testing.T) {
	if _, err := newMatcher(nil, vanity.MatchPrefix); err == nil {
		t.Error("expected an error with no patterns")
	}
	if _, err := newMatcher([]string{"border"}, vanity.MatchMode("sideways")); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}

// A short pattern finds matches quickly, which makes it a usable end-to-end test
// of the whole pipeline: generate, match, persist, report.
func TestNativeEngineFindsAndSavesAMatch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := Native{}.Run(ctx, Config{
		Patterns:  []string{"ab"}, // 10 bits: found in milliseconds
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	var match *Match
	for ev := range events {
		if ev.Kind == EventMatch {
			m := ev.Match
			match = &m
			cancel()
			break
		}
		if ev.Kind == EventError {
			t.Fatalf("engine error: %v", ev.Err)
		}
	}
	if match == nil {
		t.Fatal("no match found within the timeout")
	}

	if !strings.HasPrefix(match.Address, "ab") {
		t.Errorf("address %q does not start with the pattern", match.Address)
	}
	if len(match.Address) != vanity.AddressLen {
		t.Errorf("address is %d chars, want %d", len(match.Address), vanity.AddressLen)
	}

	// The key must be on disk before the match is announced, so that a crash
	// immediately after the event cannot lose it.
	for _, f := range []string{"hostname", "hs_ed25519_public_key", "hs_ed25519_secret_key"} {
		if _, err := os.Stat(filepath.Join(match.Dir, f)); err != nil {
			t.Errorf("expected %s to exist alongside the match: %v", f, err)
		}
	}
	host, err := os.ReadFile(filepath.Join(match.Dir, "hostname"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(host)); got != match.Address+".onion" {
		t.Errorf("hostname file says %q, event said %q", got, match.Address)
	}
}

func TestNativeEngineReportsThroughput(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := Native{}.Run(ctx, Config{
		Patterns:  []string{"zzzzzzzz"}, // 40 bits: will not be found, so we only see samples
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	for ev := range events {
		if ev.Kind == EventSample {
			if ev.KeysPerSec <= 0 {
				t.Errorf("throughput reported as %v, want a positive rate", ev.KeysPerSec)
			}
			if ev.Interval <= 0 {
				t.Errorf("sample interval %v must be positive so totals can be integrated", ev.Interval)
			}
			cancel()
			return
		}
	}
	t.Fatal("engine never reported throughput")
}

func TestNativeEngineRejectsImpossiblePattern(t *testing.T) {
	_, err := Native{}.Run(context.Background(), Config{
		Patterns:  []string{"border"}, // cannot be a suffix
		Mode:      vanity.MatchSuffix,
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected the engine to refuse a pattern that can never match")
	}
	if !strings.Contains(err.Error(), "suffix") {
		t.Errorf("error should explain the suffix rule, got: %v", err)
	}
}

func TestNativeEngineRejectsMissingOutputDir(t *testing.T) {
	if _, err := (Native{}).Run(context.Background(), Config{
		Patterns: []string{"ab"},
		Mode:     vanity.MatchPrefix,
	}); err == nil {
		t.Error("expected an error when no output directory is set")
	}
}

// Cancelling must stop the workers and close the channel, or the caller hangs.
func TestNativeEngineStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events, err := Native{}.Run(ctx, Config{
		Patterns:  []string{"zzzzzzzz"},
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not shut down within 5s of cancellation")
	}
}

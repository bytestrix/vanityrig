package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rishibaghel25/vanityrig/internal/vanity"
)

func TestRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no patterns", Config{Mode: vanity.MatchPrefix, OutputDir: t.TempDir()}},
		{"no output dir", Config{Patterns: []string{"ab"}, Mode: vanity.MatchPrefix}},
		{"impossible pattern", Config{Patterns: []string{"border"}, Mode: vanity.MatchSuffix, OutputDir: t.TempDir()}},
	}
	for _, c := range cases {
		if _, err := New(c.cfg); err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
}

func TestFindsMatchAndStopsAtTarget(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{
		Patterns:  []string{"ab"},
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: dir,
		StopAfter: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	snap := r.Snapshot()
	if len(snap.Matches) < 1 {
		t.Fatal("expected at least one match")
	}
	if !strings.HasPrefix(snap.Matches[0].Address, "ab") {
		t.Errorf("address %q does not match the pattern", snap.Matches[0].Address)
	}
	if snap.Running {
		t.Error("runner should not report itself running after finishing")
	}
	if !snap.Stopped {
		t.Error("runner should report itself stopped")
	}

	// The key files must be on disk where the snapshot says they are.
	for _, f := range []string{"hostname", "hs_ed25519_secret_key"} {
		if _, err := os.Stat(filepath.Join(snap.Matches[0].Dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// The durable counter is the whole reason this package exists: stopping and
// restarting a search must continue the total, not silently reset it.
func TestProgressSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Patterns:  []string{"zzzzzzzz"}, // never found; we only accumulate work
		Mode:      vanity.MatchPrefix,
		Threads:   2,
		OutputDir: dir,
	}

	r1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Second)
	_ = r1.Run(ctx1)
	cancel1()

	first := r1.Snapshot().KeysTried
	if first <= 0 {
		t.Fatal("first run recorded no work")
	}

	r2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resumed := r2.Snapshot().KeysTried; resumed < first {
		t.Errorf("restart lost progress: had %.0f, resumed at %.0f", first, resumed)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	_ = r2.Run(ctx2)
	cancel2()

	if total := r2.Snapshot().KeysTried; total <= first {
		t.Errorf("second run did not add to the total: %.0f then %.0f", first, total)
	}
}

// Resuming must not credit work from a different search.
func TestProgressIsNotSharedBetweenDifferentSearches(t *testing.T) {
	dir := t.TempDir()

	r1, err := New(Config{Patterns: []string{"zzzzzzzz"}, Mode: vanity.MatchPrefix, Threads: 2, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = r1.Run(ctx)
	cancel()
	if r1.Snapshot().KeysTried <= 0 {
		t.Fatal("no work recorded")
	}

	// Same directory, different pattern: the counter must start from zero.
	r2, err := New(Config{Patterns: []string{"yyyyyyyy"}, Mode: vanity.MatchPrefix, Threads: 2, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Snapshot().KeysTried; got != 0 {
		t.Errorf("a different pattern inherited %.0f keys of progress", got)
	}
}

func TestStateFileIsPrivateAndValid(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{Patterns: []string{"zzzzzzzz"}, Mode: vanity.MatchPrefix, Threads: 1, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = r.Run(ctx)
	cancel()

	path := filepath.Join(dir, "session.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no state file written: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode %04o, want 0600", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		t.Errorf("state file is not valid JSON: %v", err)
	}
	if st.KeysTried <= 0 {
		t.Error("state file recorded no work")
	}
	// No temp file should be left behind by the atomic write.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("atomic write left a .tmp file behind")
	}
}

func TestSnapshotCarriesAUsableEstimate(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{Patterns: []string{"borderx"}, Mode: vanity.MatchPrefix, Threads: 2, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = r.Run(ctx)
	cancel()

	snap := r.Snapshot()
	if snap.Estimate.Bits != 35 {
		t.Errorf("estimate says %.1f bits for a 7-char prefix, want 35", snap.Estimate.Bits)
	}
	if snap.Estimate.KeysPerSec <= 0 {
		t.Error("estimate should use a measured rate once the search has run")
	}
}

func TestDoneChannelClosesOnFinish(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{Patterns: []string{"zzzzzzzz"}, Mode: vanity.MatchPrefix, Threads: 1, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	select {
	case <-r.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done was never closed")
	}
}

// Suffix and anywhere searches must actually run, since the native engine is the
// only backend that supports them.
func TestNonPrefixModesRun(t *testing.T) {
	for _, tc := range []struct {
		mode    vanity.MatchMode
		pattern string
	}{
		{vanity.MatchSuffix, "ad"},
		{vanity.MatchAnywhere, "ab"},
	} {
		dir := t.TempDir()
		r, err := New(Config{
			Patterns:  []string{tc.pattern},
			Mode:      tc.mode,
			Threads:   2,
			OutputDir: dir,
			StopAfter: 1,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if r.EngineName() != "native" {
			t.Errorf("%s mode picked %s, want the native engine", tc.mode, r.EngineName())
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err = r.Run(ctx)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}

		snap := r.Snapshot()
		if len(snap.Matches) == 0 {
			t.Fatalf("%s: no match found", tc.mode)
		}
		addr := snap.Matches[0].Address
		switch tc.mode {
		case vanity.MatchSuffix:
			if !strings.HasSuffix(addr, tc.pattern) {
				t.Errorf("suffix search returned %q, which does not end in %q", addr, tc.pattern)
			}
		case vanity.MatchAnywhere:
			if !strings.Contains(addr, tc.pattern) {
				t.Errorf("anywhere search returned %q, which does not contain %q", addr, tc.pattern)
			}
		}
	}
}

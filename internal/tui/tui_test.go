package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bytestrix/vanityrig/internal/runner"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

func init() {
	// Render without ANSI colour so assertions match plain text.
	lipgloss.SetColorProfile(0)
}

func newTestRunner(t *testing.T, patterns []string, mode vanity.MatchMode) *runner.Runner {
	t.Helper()
	r, err := runner.New(runner.Config{
		Patterns:  patterns,
		Mode:      mode,
		Threads:   1,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The first frame must already answer "what is happening", because it is what a
// user sees before any sample has arrived.
func TestViewIsInformativeBeforeAnySample(t *testing.T) {
	m := New(newTestRunner(t, []string{"borderx"}, vanity.MatchPrefix), func() {})
	out := m.View()

	for _, want := range []string{"VanityRig", "borderx", "at the start", "status", "no matches yet", "press q"} {
		if !strings.Contains(out, want) {
			t.Errorf("first frame is missing %q:\n%s", want, out)
		}
	}
}

// Before any speed measurement exists the time estimate has nothing to divide by
// and degenerates to "forever". Showing that on the opening frame of a search
// that will actually take minutes is alarming and false.
func TestOpeningFrameDoesNotClaimTheSearchIsHopeless(t *testing.T) {
	m := New(newTestRunner(t, []string{"borderx"}, vanity.MatchPrefix), func() {})
	out := m.View()

	if strings.Contains(out, "longer than a lifetime") {
		t.Errorf("first frame claims the search is hopeless before measuring anything:\n%s", out)
	}
	if !strings.Contains(out, "measuring") {
		t.Errorf("first frame should say it is still measuring:\n%s", out)
	}
}

// Every common exit key must work. A user who wants out should not have to guess
// which key this particular program chose.
func TestAllCommonQuitKeysWork(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		stopped := false
		m := New(newTestRunner(t, []string{"borderx"}, vanity.MatchPrefix), func() { stopped = true })

		_, cmd := m.Update(tea.KeyMsg{Type: keyTypeFor(key), Runes: runesFor(key)})
		if cmd == nil {
			t.Errorf("%q did not produce a quit command", key)
		}
		if !stopped {
			t.Errorf("%q did not stop the search", key)
		}
	}
}

func keyTypeFor(key string) tea.KeyType {
	switch key {
	case "esc":
		return tea.KeyEsc
	case "ctrl+c":
		return tea.KeyCtrlC
	default:
		return tea.KeyRunes
	}
}

func runesFor(key string) []rune {
	if key == "q" {
		return []rune{'q'}
	}
	return nil
}

// Unknown keys must be ignored rather than quitting or crashing.
func TestUnrelatedKeysDoNotQuit(t *testing.T) {
	stopped := false
	m := New(newTestRunner(t, []string{"borderx"}, vanity.MatchPrefix), func() { stopped = true })
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}); cmd != nil {
		t.Error("an unrelated key produced a command")
	}
	if stopped {
		t.Error("an unrelated key stopped the search")
	}
}

// Every match mode has its own engine-selection note (mkp224o found, not
// found, or can't do this mode at all) and each is a different length — a
// layout check against only one mode can pass while the other two overflow.
// This caught exactly that: the prefix case fit, but the "mkp224o not
// installed" and "mkp224o cannot do %s matching" notes did not.
func TestNarrowTerminalDoesNotBreakLayout(t *testing.T) {
	// "borderx" isn't a legal suffix (addresses must end in one of ad/id/qd/yd),
	// so each mode needs its own valid pattern here.
	patterns := map[vanity.MatchMode][]string{
		vanity.MatchPrefix:   {"borderx"},
		vanity.MatchSuffix:   {"borderad"},
		vanity.MatchAnywhere: {"borderx"},
	}
	for _, mode := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		m := New(newTestRunner(t, patterns[mode], mode), func() {})
		for _, w := range []int{40, 60, 80, 120, 300} {
			updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
			out := updated.View()
			if out == "" {
				t.Fatalf("mode %s, width %d rendered nothing", mode, w)
			}
			for _, line := range strings.Split(out, "\n") {
				// Measure rendered width, not bytes: box-drawing glyphs are multi-byte
				// and ANSI colour codes take no columns at all, so len() would flag
				// perfectly good lines.
				if got := lipgloss.Width(line); got > w {
					t.Errorf("mode %s, width %d produced a %d-column line, which will wrap: %q",
						mode, w, got, line)
				}
			}
		}
	}
}

// A found address is the whole point of the screen and must be unmissable, along
// with where it was saved and the warning about the secret key.
func TestFoundMatchIsProminent(t *testing.T) {
	r := newTestRunner(t, []string{"ab"}, vanity.MatchPrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() { _ = r.Run(ctx) }()
	deadline := time.After(30 * time.Second)
	for {
		if len(r.Snapshot().Matches) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no match found in time")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	// Wait for Run to actually return (it now blocks until the engine's own
	// goroutines have fully stopped) before the test ends and t.TempDir's
	// cleanup runs — otherwise the engine can still be writing when cleanup
	// starts, which showed up as an intermittent "directory not empty".
	<-r.Done()

	m := New(r, func() {})
	updated, _ := m.Update(tickMsg(time.Now()))
	out := updated.View()

	if !strings.Contains(out, "FOUND") {
		t.Errorf("a found address must be announced clearly:\n%s", out)
	}
	if !strings.Contains(out, ".onion") {
		t.Errorf("the address itself must be shown:\n%s", out)
	}
	if !strings.Contains(out, "saved to") {
		t.Errorf("the user needs to be told where the key was saved:\n%s", out)
	}
	if !strings.Contains(out, "hs_ed25519_secret_key") {
		t.Errorf("the secret key warning must appear with a match:\n%s", out)
	}
}

func TestProgressBarStaysInBounds(t *testing.T) {
	for _, frac := range []float64{-1, 0, 0.5, 1, 2} {
		out := progressBar(frac, 20, 13)
		filled := strings.Count(out, "█")
		empty := strings.Count(out, "░")
		if filled+empty != 20 {
			t.Errorf("frac %v produced %d cells, want 20", frac, filled+empty)
		}
		if filled < 0 || filled > 20 {
			t.Errorf("frac %v produced %d filled cells", frac, filled)
		}
	}
}

func TestExpNegIsSafe(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 1},
		{1e9, 0}, // enormous exponent must not overflow
		{-5, 1},  // negative input clamps rather than exploding
	}
	for _, c := range cases {
		if got := expNeg(c.in); got != c.want {
			t.Errorf("expNeg(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if v := expNeg(0.7); v <= 0 || v >= 1 {
		t.Errorf("expNeg(0.7) = %v, want a value strictly between 0 and 1", v)
	}
}

func TestHumanCountScales(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5K"},
		{2.5e6, "2.50M"},
		{3.2e9, "3.20B"},
		{1.1e12, "1.10T"},
	}
	for _, c := range cases {
		if got := humanCount(c.in); got != c.want {
			t.Errorf("humanCount(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPlacementWording(t *testing.T) {
	cases := map[vanity.MatchMode]string{
		vanity.MatchPrefix:   "at the start",
		vanity.MatchSuffix:   "at the end",
		vanity.MatchAnywhere: "anywhere in",
	}
	for mode, want := range cases {
		if got := placement(mode); got != want {
			t.Errorf("placement(%s) = %q, want %q", mode, got, want)
		}
	}
}

// A stopped search must say so rather than continuing to look like it is working.
func TestStoppedSearchSaysSo(t *testing.T) {
	r := newTestRunner(t, []string{"zzzzzzzz"}, vanity.MatchPrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	m := New(r, func() {})
	updated, _ := m.Update(tickMsg(time.Now()))
	out := updated.View()

	if !strings.Contains(out, "stopped") {
		t.Errorf("a finished search must be labelled stopped:\n%s", out)
	}
}

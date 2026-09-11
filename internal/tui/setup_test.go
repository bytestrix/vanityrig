package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func press(s *Setup, keys ...string) {
	for _, k := range keys {
		s.Update(key(k))
	}
}

func typeText(s *Setup, text string) {
	for _, r := range text {
		s.Update(key(string(r)))
	}
}

// Both sections must be visible on the very first frame — the whole point of
// this screen is that nothing is hidden behind a sequence of prompts.
func TestBothPanelsVisibleOnFirstFrame(t *testing.T) {
	s := NewSetup(SetupConfig{})
	out := s.View()

	for _, want := range []string{"Settings", "Log / Status", "Word(s)", "Save to", "Match mode", "CPU threads", "GPU"} {
		if !strings.Contains(out, want) {
			t.Errorf("first frame is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "coming soon") {
		t.Error("GPU should be shown as not-yet-available, not silently absent or interactive")
	}
}

func TestTypingUpdatesLivePreview(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "borderx")

	out := s.View()
	if !strings.Contains(out, "prefix") || !strings.Contains(out, "anywhere") {
		t.Errorf("expected a live mode comparison once a word is typed:\n%s", out)
	}
	if strings.Contains(out, "Type a word above") {
		t.Error("placeholder preview text should be gone once a word is typed")
	}
}

func TestModeFollowsRecommendationUntilManuallyChanged(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "borderx")
	if s.mode != vanity.MatchAnywhere {
		t.Fatalf("expected the mode to auto-follow the recommendation (anywhere), got %q", s.mode)
	}

	press(s, "tab", "tab", "left") // move focus to Match mode, then change it
	if !s.modeIsFixed {
		t.Fatal("changing the mode manually should stop it from following the recommendation")
	}
	fixed := s.mode

	typeText(s, "x") // further edits to the word must not override the manual choice
	if s.mode != fixed {
		t.Errorf("mode changed after being manually fixed: got %q, want %q", s.mode, fixed)
	}
}

func TestTabCyclesFocusThroughAllFields(t *testing.T) {
	s := NewSetup(SetupConfig{})
	if s.focus != fieldWord {
		t.Fatalf("initial focus = %v, want fieldWord", s.focus)
	}
	for i, want := range []setupField{fieldOutDir, fieldMode, fieldThreads, fieldWord} {
		press(s, "tab")
		if s.focus != want {
			t.Errorf("after %d tab(s): focus = %v, want %v", i+1, s.focus, want)
		}
	}
}

func TestThreadsPercentStaysInBounds(t *testing.T) {
	s := NewSetup(SetupConfig{})
	press(s, "tab", "tab", "tab") // focus CPU threads
	for i := 0; i < 20; i++ {
		press(s, "left")
	}
	if s.percent < 10 {
		t.Errorf("percent should floor at 10, got %d", s.percent)
	}
	for i := 0; i < 20; i++ {
		press(s, "right")
	}
	if s.percent > 100 {
		t.Errorf("percent should cap at 100, got %d", s.percent)
	}
}

func TestEnterWithNoWordDoesNotStart(t *testing.T) {
	s := NewSetup(SetupConfig{})
	press(s, "enter")
	if s.phase != phaseSetup {
		t.Fatal("must not start a search with no word entered")
	}
	if s.startErr == "" {
		t.Error("expected an inline error explaining why it didn't start")
	}
}

// Picking an impossible mode for the given word (e.g. a suffix that breaks
// the trailing-character rule) must be caught before a runner is even
// constructed — the same safety net the non-interactive path already has.
func TestEnterWithImpossibleModeDoesNotStart(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "test") // "test" cannot be a valid suffix
	press(s, "tab", "tab")
	// cycle from the recommended mode to suffix specifically
	for s.mode != vanity.MatchSuffix {
		press(s, "right")
	}
	press(s, "enter")

	if s.phase != phaseSetup {
		t.Fatal("must not start a search with a mode that can never match")
	}
	if s.run != nil {
		t.Error("no runner should have been constructed")
	}
	if !strings.Contains(s.startErr, "suffix") && !strings.Contains(s.startErr, "\"d\"") {
		t.Errorf("error should explain the suffix rule, got %q", s.startErr)
	}
}

// The actual happy path: a valid word and an achievable mode must start a
// real search, after which the settings freeze and the log panel switches
// from the cost preview to live progress.
func TestEnterWithValidInputStartsAndFreezesSettings(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "ab")
	press(s, "tab")
	typeText(s, t.TempDir())
	press(s, "enter")

	if s.phase != phaseRunning {
		t.Fatalf("expected phaseRunning after a valid start, got %v", s.phase)
	}
	if s.run == nil {
		t.Fatal("expected a runner to have been constructed")
	}
	if s.word.Focused() || s.outDir.Focused() {
		t.Error("text inputs must be blurred once the search has started")
	}

	out := s.View()
	if strings.Contains(out, "Type a word above") {
		t.Error("log panel should show live progress, not the pre-start preview, once running")
	}
	if !strings.Contains(out, "status") {
		t.Errorf("expected live status in the log panel:\n%s", out)
	}

	s.cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-s.run.Done():
			return
		case <-deadline:
			t.Fatal("search did not stop after cancel")
		default:
			s.Update(tickMsg(time.Now()))
		}
	}
}

// A caller with -match already decided (an explicit flag) should see that
// choice honoured rather than silently overridden by the recommendation.
func TestExplicitModeIsNotOverriddenByRecommendation(t *testing.T) {
	s := NewSetup(SetupConfig{Words: []string{"borderx"}, Mode: vanity.MatchPrefix})
	if !s.modeIsFixed {
		t.Fatal("an explicitly configured mode must start fixed")
	}
	if s.mode != vanity.MatchPrefix {
		t.Fatalf("mode = %q, want the explicitly configured prefix", s.mode)
	}
}

func TestSnapshotBeforeStartReportsNotStarted(t *testing.T) {
	s := NewSetup(SetupConfig{})
	if _, started := s.Snapshot(); started {
		t.Error("Snapshot should report not-started before Enter is pressed")
	}
}

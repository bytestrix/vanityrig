package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// typeText types into whichever field currently has focus. Typing only
// reaches a text field while it's in edit mode (entered via Enter or a
// click), so this enters edit mode first if it isn't already active —
// mirroring what a real user does before typing.
func typeText(s *Setup, text string) {
	if !s.editing {
		s.Update(key("enter"))
	}
	for _, r := range text {
		s.Update(key(string(r)))
	}
}

// Both sections must be visible on the very first frame — the whole point of
// this screen is that nothing is hidden behind a sequence of prompts.
func TestBothPanelsVisibleOnFirstFrame(t *testing.T) {
	s := NewSetup(SetupConfig{})
	out := s.View()

	for _, want := range []string{
		"Configuration", "Resources", "Statistics", "Progress", "Logs",
		"Word(s)", "Save to", "Match mode", "CPU share", "GPU",
	} {
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
	if strings.Contains(out, "Type a word to see cost") {
		t.Error("placeholder preview text should be gone once a word is typed")
	}
}

func TestModeFollowsRecommendationUntilManuallyChanged(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "borderx")
	press(s, "enter") // finish editing the word field
	if s.mode != vanity.MatchAnywhere {
		t.Fatalf("expected the mode to auto-follow the recommendation (anywhere), got %q", s.mode)
	}

	press(s, "tab", "tab", "left") // move focus to Match mode, then change it
	if !s.modeIsFixed {
		t.Fatal("changing the mode manually should stop it from following the recommendation")
	}
	fixed := s.mode

	press(s, "shift+tab", "shift+tab") // back to Word(s)
	typeText(s, "x")                   // further edits to the word must not override the manual choice
	press(s, "enter")
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
	press(s, "s") // s starts (or stops) a search regardless of which field has focus
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
	press(s, "enter", "tab", "tab")
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
	press(s, "enter", "tab") // finish editing Word(s), move to Save to
	typeText(s, t.TempDir())
	press(s, "enter") // finish editing Save to
	press(s, "s")     // start

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
	if strings.Contains(out, "Type a word to see cost") {
		t.Error("statistics panel should show live progress, not the pre-start preview, once running")
	}
	if !strings.Contains(out, "Keys Tested") {
		t.Errorf("expected live statistics once running:\n%s", out)
	}
	if !strings.Contains(out, "[INFO") {
		t.Errorf("expected the log panel to have real event lines once started:\n%s", out)
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

// A hand-tuned width calculation is exactly where an off-by-a-few-columns
// bug hides — this project has already shipped one (the CI-caught engine
// note overflow) and this feature shipped two more during development (the
// header's version/URL aside and the resources panel's help line both
// overflowed their border before being fixed). Sweep a wide range of widths,
// in both the single-column and two-column layouts, with a deliberately long
// word and output path to stress the free-text lines, and assert nothing
// ever exceeds its terminal width.
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	longWord := "borderlandborderlandborderland"
	longPath := t.TempDir() + "/a/very/long/output/directory/for/keys"

	for _, width := range []int{50, 60, 80, 100, 120, 145, 200} {
		for _, running := range []bool{false, true} {
			s := NewSetup(SetupConfig{Words: []string{longWord}, OutDir: longPath})
			s.Update(tea.WindowSizeMsg{Width: width, Height: 50})
			if running {
				press(s, "s")
				if s.phase != phaseRunning {
					t.Fatalf("width %d: expected the search to start", width)
				}
				s.Update(tickMsg(time.Now()))
			}

			out := s.View()
			for _, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Errorf("width %d (running=%v): a %d-column line overflows: %q",
						width, running, got, line)
				}
			}
			if running {
				s.cancel()
			}
		}
	}
}

// handleMouse hardcodes which row is which field. If renderConfig's row
// order ever changes, clicking would silently start targeting the wrong
// field instead of failing loudly — so pin the two together here instead of
// trusting they stay in sync by construction.
func TestMouseRowsMatchRenderedFields(t *testing.T) {
	// Every width from just below the two-column threshold up through a wide
	// terminal — this is exactly the class of bug already found once
	// (rows shifting because a field silently wrapped at one particular
	// width), so a single sample width isn't enough to trust it stays fixed.
	for _, width := range []int{twoColumnMinWidth - 1, twoColumnMinWidth, 115, 120, 130, 145} {
		s := NewSetup(SetupConfig{})
		s.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		lines := strings.Split(s.View(), "\n")

		want := map[int]string{
			0: "Word(s)",
			1: "Save to",
			2: "Match mode",
			3: "CPU share",
		}
		for offset, label := range want {
			row := fieldsFirstRow + offset
			if row >= len(lines) {
				t.Fatalf("width %d: row %d (for %q) is beyond the rendered output (%d lines)", width, row, label, len(lines))
			}
			if !strings.Contains(lines[row], label) {
				t.Errorf("width %d: row %d: expected %q, got %q", width, row, label, lines[row])
			}
		}
	}
}

func TestClickFocusesAndActsOnField(t *testing.T) {
	s := NewSetup(SetupConfig{Words: []string{"borderx"}})
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	// A left-click anywhere on the Match mode row focuses it and cycles the
	// value forward, exactly as pressing right-arrow after tabbing there
	// would — see TestMouseClickCyclesModeAndRendersIt for the full cycle.
	before := s.mode
	click := tea.MouseMsg{X: 20, Y: fieldsFirstRow + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	s.Update(click)

	if s.focus != fieldMode {
		t.Errorf("click on Match mode row should focus it, got focus=%v", s.focus)
	}
	if !s.modeIsFixed {
		t.Error("clicking to change the mode should fix it, same as an arrow key would")
	}
	if s.mode == before {
		t.Error("left-click on Match mode should cycle it")
	}

	// Right-click on CPU share (offset 3) should lower the percentage.
	beforePercent := s.percent
	s.Update(tea.MouseMsg{X: 20, Y: fieldsFirstRow + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if s.focus != fieldThreads {
		t.Errorf("click on CPU share row should focus it, got focus=%v", s.focus)
	}
	if s.percent >= beforePercent {
		t.Errorf("right-click on CPU share should lower it: before=%d after=%d", beforePercent, s.percent)
	}
}

// The whole point of exact hit-testing is that clicking "suffix" selects
// suffix regardless of what was selected before — a row-level cycle would
// get this right only by coincidence. Verify each option's computed span
// against the actual rendered text before trusting the span math, then
// click within each and confirm it selects exactly that mode.
// Match mode is a single cycling value ("‹ Anywhere ›"), the same shape as
// every other field in the panel — not a row of three inline options — so a
// click anywhere on the row (left or right button) cycles it, exactly like
// CPU share. The rendered row must actually show the current mode,
// capitalized, so this checks the real output rather than only the state.
func TestMouseClickCyclesModeAndRendersIt(t *testing.T) {
	s := NewSetup(SetupConfig{Words: []string{"borderx"}, Mode: vanity.MatchPrefix})
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	modeLine := strings.Split(s.View(), "\n")[fieldsFirstRow+2]
	if !strings.Contains(modeLine, "Prefix") {
		t.Fatalf("expected the current mode capitalized in the row, got %q", modeLine)
	}

	s.Update(tea.MouseMsg{X: 30, Y: fieldsFirstRow + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if s.mode != vanity.MatchSuffix {
		t.Errorf("left-click should cycle prefix -> suffix, got %q", s.mode)
	}
	if !s.modeIsFixed {
		t.Error("clicking to change the mode should fix it, same as an arrow key would")
	}

	s.Update(tea.MouseMsg{X: 30, Y: fieldsFirstRow + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if s.mode != vanity.MatchPrefix {
		t.Errorf("right-click should cycle back suffix -> prefix, got %q", s.mode)
	}
}

func TestMouseIgnoredOnceRunning(t *testing.T) {
	s := NewSetup(SetupConfig{Words: []string{"ab"}, OutDir: t.TempDir()})
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	press(s, "s")
	if s.phase != phaseRunning {
		t.Fatal("expected the search to start")
	}
	before := s.mode

	s.Update(tea.MouseMsg{X: 20, Y: fieldsFirstRow + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if s.mode != before {
		t.Error("clicking after the search has started must not change settings")
	}
	s.cancel()
}

// The default log level (info) hides routine STATS throughput lines but
// keeps INFO and MATCH; "v" cycles to warn (which hides INFO too) and then
// to debug (which shows everything, STATS included).
func TestLogLevelFilterCyclesAndHidesStats(t *testing.T) {
	s := NewSetup(SetupConfig{})
	s.appendLog("INFO", "search started")
	s.appendLog("STATS", "1.0M keys tested | 1.0M/sec")

	out := s.View()
	if !strings.Contains(out, "search started") {
		t.Errorf("expected the INFO line to be visible by default:\n%s", out)
	}
	if strings.Contains(out, "keys tested") {
		t.Errorf("expected the STATS line to be hidden at the default (info) level:\n%s", out)
	}

	press(s, "v") // info -> warn
	if s.logLevel != logWarn {
		t.Fatalf("expected logLevel to advance to warn, got %v", s.logLevel)
	}
	out = s.View()
	if strings.Contains(out, "search started") {
		t.Errorf("expected the INFO line to be hidden at the warn level:\n%s", out)
	}

	press(s, "v") // warn -> debug
	if s.logLevel != logDebug {
		t.Fatalf("expected logLevel to advance to debug, got %v", s.logLevel)
	}
	out = s.View()
	if !strings.Contains(out, "keys tested") {
		t.Errorf("expected the STATS line to be visible at the debug level:\n%s", out)
	}

	press(s, "v") // debug -> info (wraps)
	if s.logLevel != logInfo {
		t.Fatalf("expected logLevel to wrap back to info, got %v", s.logLevel)
	}
}

// A MATCH line must stay visible no matter how strict the filter is — the
// whole point of running the search is to see this.
func TestMatchLogLineIgnoresLevelFilter(t *testing.T) {
	s := NewSetup(SetupConfig{})
	s.appendLog("MATCH", "borderxabc.onion")
	s.logLevel = logWarn

	if !strings.Contains(s.View(), "borderxabc.onion") {
		t.Error("a MATCH line must remain visible even at the strictest log filter")
	}
}

// The Statistics panel should show the difficulty as bits of work (2^N) both
// before a search starts (per mode, in the cost preview) and once it's
// running (for the mode actually in use) — the same number report.go already
// prints for -check, just surfaced live in the dashboard too.
func TestDifficultyIsShownInStatistics(t *testing.T) {
	s := NewSetup(SetupConfig{})
	typeText(s, "borderx")
	press(s, "enter")

	out := s.View()
	if !strings.Contains(out, "2^") {
		t.Errorf("expected a 2^N difficulty figure in the pre-start preview:\n%s", out)
	}
}

func TestGPUStatusPanelIsHonestAboutNotExisting(t *testing.T) {
	s := NewSetup(SetupConfig{})
	out := s.View()
	if !strings.Contains(out, "GPU Status") {
		t.Errorf("expected a GPU Status panel:\n%s", out)
	}
	if !strings.Contains(out, "coming soon") {
		t.Errorf("GPU status must say plainly that it doesn't exist yet, not show fake numbers:\n%s", out)
	}
	for _, fake := range []string{"NVIDIA", "RTX", "CUDA 12", "%GPU"} {
		if strings.Contains(out, fake) {
			t.Errorf("must not show a fabricated GPU detail %q:\n%s", fake, out)
		}
	}
}

// Package tui also provides Setup: a single combined screen for configuring
// and then watching a search, rather than a sequence of separate prompt
// screens. A "Settings" panel (word, save location, match mode, CPU share)
// and a "Log / Status" panel are both visible from the very first frame;
// starting the search freezes the settings panel in place and the log panel
// switches from a live cost preview to live progress, without ever leaving
// this screen.
package tui

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bytestrix/vanityrig/internal/runner"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// setupField identifies which settings control has focus.
type setupField int

const (
	fieldWord setupField = iota
	fieldOutDir
	fieldMode
	fieldThreads
	fieldCount
)

// setupPhase is where the screen is in its lifecycle.
type setupPhase int

const (
	phaseSetup setupPhase = iota
	phaseRunning
	phaseFinished
)

var (
	stPanelTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stFocused    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stFieldLabel = lipgloss.NewStyle().Foreground(colDim)
	panelBorder  = lipgloss.RoundedBorder()
)

// SetupConfig seeds the screen with whatever was already decided on the
// command line, so `vanityrig word` doesn't ask a question it already has
// the answer to — those fields just start pre-filled and still editable.
type SetupConfig struct {
	Words      []string
	OutDir     string
	Mode       vanity.MatchMode // "" means "ask / follow the recommendation"
	Threads    int              // 0 means "all cores"
	Rate       float64
	StopAfter  int
	EnginePath string
}

// Setup is the combined settings+log dashboard.
type Setup struct {
	word   textinput.Model
	outDir textinput.Model

	mode        vanity.MatchMode
	modeIsFixed bool // set once the user (or -match) has chosen explicitly

	percent int // 1-100, share of logical CPUs to use
	stop    int // StopAfter, carried through unchanged

	rate       float64
	enginePath string

	focus setupField
	phase setupPhase

	width, height int
	quitting      bool

	run      *runner.Runner
	cancel   func()
	snap     runner.Snapshot
	finished bool
	startErr string
}

// NewSetup builds the combined screen, pre-filled from cfg.
func NewSetup(cfg SetupConfig) *Setup {
	word := textinput.New()
	word.Placeholder = "e.g. myproject"
	word.Prompt = ""
	word.SetValue(strings.Join(cfg.Words, " "))

	out := textinput.New()
	out.Placeholder = defaultOutputDirHint()
	out.Prompt = ""
	out.SetValue(cfg.OutDir)

	percent := 100
	if cfg.Threads > 0 {
		percent = clampPercent(int(math.Round(float64(cfg.Threads) / float64(runtime.NumCPU()) * 100)))
	}

	rate := cfg.Rate
	if rate <= 0 {
		rate = 22.2e6
	}

	s := &Setup{
		word:        word,
		outDir:      out,
		mode:        cfg.Mode,
		modeIsFixed: cfg.Mode != "",
		percent:     percent,
		stop:        cfg.StopAfter,
		rate:        rate,
		enginePath:  cfg.EnginePath,
		focus:       fieldWord,
		width:       80,
	}
	if s.mode == "" {
		s.mode = vanity.MatchAnywhere // placeholder until the first recompute picks a real recommendation
	}
	s.word.Focus()
	s.recomputeMode()
	return s
}

func defaultOutputDirHint() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.vanityrig/keys"
	}
	return filepath.Join(home, ".vanityrig", "keys")
}

func clampPercent(p int) int {
	if p < 10 {
		return 10
	}
	if p > 100 {
		return 100
	}
	return p
}

// patterns returns the words currently typed, split on whitespace.
func (s *Setup) patterns() []string { return strings.Fields(s.word.Value()) }

// recomputeMode follows the recommended mode as the word changes, until the
// user (or an explicit -match) has pinned one down.
func (s *Setup) recomputeMode() {
	if s.modeIsFixed {
		return
	}
	pats := s.patterns()
	if len(pats) == 0 {
		return
	}
	cmp := vanity.CompareModes(pats, s.rate)
	if cmp.Best != "" {
		s.mode = cmp.Best
	}
}

// Outcome is what the caller does once the screen is done: either the search
// already ran to completion/cancellation inside this model, or the user quit
// before ever starting one.
type Outcome struct {
	Started bool
	Snap    runner.Snapshot
}

func (s *Setup) Init() tea.Cmd { return tick() }

func (s *Setup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		return s, nil

	case tickMsg:
		if s.phase == phaseRunning {
			s.snap = s.run.Snapshot()
			select {
			case <-s.run.Done():
				s.finished = true
				s.phase = phaseFinished
			default:
			}
		}
		return s, tick()

	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *Setup) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if s.phase != phaseSetup {
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			s.quitting = true
			if s.cancel != nil {
				s.cancel()
			}
			return s, tea.Quit
		}
		return s, nil
	}

	switch msg.String() {
	case "ctrl+c", "esc":
		s.quitting = true
		return s, tea.Quit

	case "tab", "shift+tab", "down", "up":
		s.blurCurrent()
		if msg.String() == "shift+tab" || msg.String() == "up" {
			s.focus = (s.focus - 1 + fieldCount) % fieldCount
		} else {
			s.focus = (s.focus + 1) % fieldCount
		}
		s.focusCurrent()
		return s, nil

	case "left", "right":
		switch s.focus {
		case fieldMode:
			s.modeIsFixed = true
			s.mode = cycleMode(s.mode, msg.String() == "right")
		case fieldThreads:
			delta := 10
			if msg.String() == "left" {
				delta = -10
			}
			s.percent = clampPercent(s.percent + delta)
		default:
			return s.routeToFocused(msg)
		}
		return s, nil

	case "enter":
		return s.tryStart()
	}

	return s.routeToFocused(msg)
}

func (s *Setup) routeToFocused(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch s.focus {
	case fieldWord:
		s.word, cmd = s.word.Update(msg)
		s.recomputeMode()
	case fieldOutDir:
		s.outDir, cmd = s.outDir.Update(msg)
	}
	return s, cmd
}

func (s *Setup) blurCurrent() {
	switch s.focus {
	case fieldWord:
		s.word.Blur()
	case fieldOutDir:
		s.outDir.Blur()
	}
}

func (s *Setup) focusCurrent() {
	switch s.focus {
	case fieldWord:
		s.word.Focus()
	case fieldOutDir:
		s.outDir.Focus()
	}
}

func cycleMode(cur vanity.MatchMode, forward bool) vanity.MatchMode {
	order := []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere}
	i := 0
	for idx, m := range order {
		if m == cur {
			i = idx
		}
	}
	if forward {
		i = (i + 1) % len(order)
	} else {
		i = (i - 1 + len(order)) % len(order)
	}
	return order[i]
}

func (s *Setup) tryStart() (tea.Model, tea.Cmd) {
	pats := s.patterns()
	if len(pats) == 0 {
		s.startErr = "enter a word to search for"
		return s, nil
	}
	for _, p := range pats {
		if err := vanity.PreflightSyntax(p); err != nil {
			s.startErr = err.Error()
			return s, nil
		}
	}
	if err := vanity.Validate(pats[0], s.mode); err != nil {
		s.startErr = err.Error()
		return s, nil
	}
	s.startErr = ""

	threads := int(math.Round(float64(runtime.NumCPU()) * float64(s.percent) / 100))
	if threads < 1 {
		threads = 1
	}

	r, err := runner.New(runner.Config{
		Patterns:   pats,
		Mode:       s.mode,
		Threads:    threads,
		OutputDir:  s.outDir.Value(),
		EnginePath: s.enginePath,
		StopAfter:  s.stop,
	})
	if err != nil {
		s.startErr = err.Error()
		return s, nil
	}

	s.blurCurrent()
	s.run = r
	s.phase = phaseRunning
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { _ = r.Run(ctx) }()
	s.snap = r.Snapshot()
	return s, nil
}

// Snapshot exposes the runner's state once a search has started, so the
// caller can print a final summary after the program exits.
func (s *Setup) Snapshot() (runner.Snapshot, bool) {
	if s.run == nil {
		return runner.Snapshot{}, false
	}
	return s.run.Snapshot(), true
}

func (s *Setup) View() string {
	width := s.width
	if width <= 0 {
		width = 80
	}
	if width > 100 {
		width = 100
	}

	var b strings.Builder
	b.WriteString(s.renderSettings(width))
	b.WriteString("\n")
	b.WriteString(s.renderLog(width))
	b.WriteString("\n")
	b.WriteString(stHint.Render("  " + s.footer()))
	b.WriteString("\n")
	return b.String()
}

func (s *Setup) footer() string {
	if s.phase != phaseSetup {
		return "press q to stop"
	}
	return "tab move · ←/→ change · enter start · q quit"
}

func panel(title string, width int, body string) string {
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	style := lipgloss.NewStyle().
		Border(panelBorder).
		BorderForeground(colDim).
		Padding(0, 1).
		Width(inner)
	return style.Render(stPanelTitle.Render(title) + "\n" + body)
}

func (s *Setup) renderSettings(width int) string {
	editable := s.phase == phaseSetup
	row := func(label, value string, focused bool) string {
		l := stFieldLabel.Render(pad(label, 14))
		if focused && editable {
			l = stFocused.Render(pad("▸ "+label, 14))
		}
		return "  " + l + " " + value + "\n"
	}

	wordView := s.word.View()
	if !editable {
		wordView = stValue.Render(strings.Join(s.patterns(), ", "))
	}
	outView := s.outDir.View()
	if !editable {
		out := s.outDir.Value()
		if out == "" {
			out = defaultOutputDirHint()
		}
		outView = stValue.Render(out)
	}

	var b strings.Builder
	b.WriteString(row("Word(s)", wordView, s.focus == fieldWord))
	b.WriteString(row("Save to", outView, s.focus == fieldOutDir))
	b.WriteString(row("Match mode", modeRow(s.mode, s.focus == fieldMode && editable), s.focus == fieldMode))
	b.WriteString(row("CPU threads", cpuRow(s.percent), s.focus == fieldThreads))
	b.WriteString("  " + stFieldLabel.Render(pad("GPU", 14)) + " " + stLabel.Render("coming soon") + "\n")

	if s.startErr != "" {
		b.WriteString("\n  " + stErr.Render("! "+s.startErr))
	}

	return panel("Settings", width, strings.TrimRight(b.String(), "\n"))
}

func modeRow(cur vanity.MatchMode, focused bool) string {
	var parts []string
	for _, m := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		mark := "( )"
		style := stLabel
		if m == cur {
			mark = "(•)"
			style = stValue
		}
		parts = append(parts, style.Render(mark+" "+string(m)))
	}
	line := strings.Join(parts, "  ")
	if focused {
		line += stHint.Render("  ←/→")
	}
	return line
}

func cpuRow(percent int) string {
	const barWidth = 20
	filled := int(float64(barWidth) * float64(percent) / 100)
	bar := lipgloss.NewStyle().Foreground(colAccent).Render(strings.Repeat("█", filled)) +
		stLabel.Render(strings.Repeat("░", barWidth-filled))
	cores := int(math.Round(float64(runtime.NumCPU()) * float64(percent) / 100))
	if cores < 1 {
		cores = 1
	}
	return fmt.Sprintf("%s  %3d%%  (%d/%d cores)", bar, percent, cores, runtime.NumCPU())
}

func (s *Setup) renderLog(width int) string {
	switch s.phase {
	case phaseSetup:
		return panel("Log / Status", width, s.renderPreview())
	default:
		return panel("Log / Status", width, s.renderProgress())
	}
}

// renderPreview shows the same prefix/suffix/anywhere cost comparison the
// non-interactive -check output prints, live-updated as the word changes,
// so the settings panel's mode selector isn't a blind choice.
func (s *Setup) renderPreview() string {
	pats := s.patterns()
	if len(pats) == 0 {
		return stLabel.Render("Type a word above to see cost estimates for each mode.")
	}
	cmp := vanity.CompareModes(pats, s.rate)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %-10s %-14s %s\n", "mode", "typical time", ""))
	for _, m := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		e := cmp.Estimates[m]
		timing := humanDur(e.P50)
		note := ""
		switch {
		case e.Probability <= 0:
			timing = "impossible"
		case m == cmp.Best:
			note = stGood.Render("← recommended")
		}
		marker := "  "
		if m == s.mode {
			marker = stFocused.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-10s %-14s %s\n", marker, m, timing, note))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Setup) renderProgress() string {
	snap := s.snap
	labelWidth := 13

	status := stGood.Render("● running")
	if s.finished || snap.Stopped {
		status = stWarn.Render("■ stopped")
	} else if snap.KeysPerSec <= 0 {
		status = stWarn.Render("● starting…")
	}

	var b strings.Builder
	b.WriteString(row2("status", status, labelWidth))
	b.WriteString(row2("speed", rate(snap.KeysPerSec), labelWidth))
	b.WriteString(row2("keys tried", humanCount(snap.KeysTried), labelWidth))
	if snap.Estimate.Probability > 0 {
		chance := 1 - expNeg(snap.Estimate.Probability*snap.KeysTried)
		b.WriteString(row2("odds so far", fmt.Sprintf("%.1f%%", chance*100), labelWidth))
	}
	b.WriteString("\n")

	if len(snap.Matches) == 0 {
		b.WriteString(stLabel.Render("no matches yet"))
	} else {
		var lines []string
		lines = append(lines, stGood.Render(fmt.Sprintf("FOUND %d", len(snap.Matches))))
		for _, mt := range snap.Matches {
			lines = append(lines, stValue.Render(mt.Address+".onion"))
			lines = append(lines, stLabel.Render("saved to "+mt.Dir))
		}
		b.WriteString(stMatchBox.Render(strings.Join(lines, "\n")))
	}
	return strings.TrimRight(b.String(), "\n")
}

func row2(label, value string, labelWidth int) string {
	return fmt.Sprintf("%s %s\n", stLabel.Render(pad(label, labelWidth)), value)
}

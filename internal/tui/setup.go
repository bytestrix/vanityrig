// Package tui also provides Setup: a single combined dashboard for
// configuring and then watching a search, rather than a sequence of
// separate prompt screens. Configuration, Resources, Statistics, Progress
// and Logs are all visible from the very first frame; starting the search
// freezes the configuration panel in place while the others switch from a
// pre-start preview to live data, without ever leaving this screen.
package tui

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

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

// Section accent colours — each panel gets its own, so the dashboard reads as
// distinct sections rather than one undifferentiated block.
var (
	colConfig    = lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#60a5fa"}
	colResources = colGood
	colStats     = colWarn
	colProgress  = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#c084fc"}
	colLogs      = lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#22d3ee"}

	stPanelTitle = lipgloss.NewStyle().Bold(true)
	stFocused    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stFieldLabel = lipgloss.NewStyle().Foreground(colDim)
	panelBorder  = lipgloss.RoundedBorder()
)

const repoURL = "https://github.com/bytestrix/vanityrig"

// twoColumnMinWidth is the narrowest total width the two-column layout is
// allowed at. It isn't a taste choice: below it, the configuration column's
// share of the width leaves less room than the "Match mode" row's content
// needs (measured at 56 columns focused, +4 for border/padding = 60), so
// that row silently word-wraps and shifts every row below it down by one —
// TestMouseRowsMatchRenderedFields caught this once already. At the 58%
// config share used below, 60/0.58 ≈ 105 is where wrapping starts; 110
// leaves a small margin above that without pushing the threshold past what
// a common ~110-120 column terminal window actually is.
const twoColumnMinWidth = 110

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
	Version    string // shown in the header; "dev" if empty
}

// Setup is the combined settings+dashboard screen.
type Setup struct {
	word   textinput.Model
	outDir textinput.Model

	mode        vanity.MatchMode
	modeIsFixed bool // set once the user (or -match) has chosen explicitly

	percent int // 1-100, share of logical CPUs to use
	stop    int // StopAfter, carried through unchanged

	rate       float64
	enginePath string
	version    string

	focus   setupField
	editing bool // true while the focused text field is actively receiving keystrokes
	phase   setupPhase

	width, height int
	quitting      bool

	run           *runner.Runner
	cancel        func()
	snap          runner.Snapshot
	finished      bool
	startErr      string
	logLines      []string
	lastStatLog   time.Time
	matchesLogged int
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

	version := cfg.Version
	if version == "" {
		version = "dev"
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
		version:     version,
		focus:       fieldWord,
		width:       100,
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
	cmp := vanity.CompareModes(pats, s.effectiveRate())
	if cmp.Best != "" {
		s.mode = cmp.Best
	}
}

// effectiveRate is s.rate (the assumed full-CPU throughput) scaled by the
// configured CPU share, so every estimate shown actually reflects the
// thread count the search will run with — using the unscaled rate
// everywhere, regardless of what CPU share was configured, was a real bug:
// turning the slider down changed nothing on screen.
func (s *Setup) effectiveRate() float64 {
	return s.rate * float64(s.percent) / 100
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
			s.logProgress()
			select {
			case <-s.run.Done():
				s.finished = true
				s.phase = phaseFinished
				s.appendLog("INFO", "search finished")
			default:
			}
		}
		return s, tick()

	case tea.KeyMsg:
		return s.handleKey(msg)

	case tea.MouseMsg:
		return s.handleMouse(tea.MouseEvent(msg))
	}
	return s, nil
}

// fieldsFirstRow is the row (0-based, matching tea.MouseEvent.Y) of the first
// configuration field — two header lines, a blank line, the panel's top
// border, and its title line. TestMouseRowsMatchRenderedFields checks this
// against the real rendered output, so a layout change that moves this
// can't silently break clicking without a test noticing.
const fieldsFirstRow = 5

// handleMouse lets the configuration panel be driven with the mouse as well
// as the keyboard: a click focuses the row under the cursor, and for the
// cycling fields (Match mode, CPU share) also nudges the value the same way
// an arrow key would — left-click forward, right-click back.
func (s *Setup) handleMouse(m tea.MouseEvent) (tea.Model, tea.Cmd) {
	if s.phase != phaseSetup || m.Action != tea.MouseActionPress {
		return s, nil
	}
	if m.Button != tea.MouseButtonLeft && m.Button != tea.MouseButtonRight {
		return s, nil
	}

	var target setupField
	switch m.Y - fieldsFirstRow {
	case 0:
		target = fieldWord
	case 1:
		target = fieldOutDir
	case 2:
		target = fieldMode
	case 3:
		target = fieldThreads
	default:
		return s, nil
	}

	s.blurCurrent()
	s.editing = false
	s.focus = target

	forward := m.Button == tea.MouseButtonLeft
	var cmd tea.Cmd
	switch target {
	case fieldWord, fieldOutDir:
		// A click on a text field is naturally "start editing it", the same
		// as pressing Enter there — unlike Match mode/CPU share, there's no
		// useful non-editing action a click could take on it instead.
		s.editing = true
		cmd = s.focusCurrent()
	case fieldMode:
		s.modeIsFixed = true
		s.mode = cycleMode(s.mode, forward)
	case fieldThreads:
		delta := 10
		if !forward {
			delta = -10
		}
		s.percent = clampPercent(s.percent + delta)
	}
	return s, cmd
}

// handleKey implements two distinct interaction modes, matching the
// reference layout's own footer ("[Enter] Edit" as separate from
// navigation): while s.editing is true, every keystroke goes to the
// focused text field — including letters that double as shortcuts, like
// "s" or "q" — since nothing is more broken than being unable to type a
// word containing those letters. Enter/Esc leave edit mode. Everywhere
// else, single-letter shortcuts (start/stop, quit) are live.
func (s *Setup) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if s.editing {
		if key == "enter" || key == "esc" {
			s.editing = false
			s.blurCurrent()
			return s, nil
		}
		return s.routeToFocused(msg)
	}

	switch key {
	case "ctrl+c", "q":
		s.quitting = true
		if s.cancel != nil {
			s.cancel() // stop cleanly rather than leaving the engine running past exit
		}
		return s, tea.Quit

	case "s":
		switch s.phase {
		case phaseSetup:
			return s.tryStart()
		case phaseRunning:
			s.stopSearch()
		case phaseFinished:
			s.rearm()
		}
		return s, nil

	case "l":
		s.logLines = nil
		return s, nil
	}

	if s.phase != phaseSetup {
		return s, nil // navigation/editing only make sense before or between searches
	}

	switch key {
	case "esc":
		s.quitting = true
		return s, tea.Quit

	case "tab", "down":
		s.focus = (s.focus + 1) % fieldCount
		return s, nil

	case "shift+tab", "up":
		s.focus = (s.focus - 1 + fieldCount) % fieldCount
		return s, nil

	case "left", "right":
		switch s.focus {
		case fieldMode:
			s.modeIsFixed = true
			s.mode = cycleMode(s.mode, key == "right")
		case fieldThreads:
			delta := 10
			if key == "left" {
				delta = -10
			}
			s.percent = clampPercent(s.percent + delta)
		}
		return s, nil

	case "enter":
		if s.focus == fieldWord || s.focus == fieldOutDir {
			s.editing = true
			return s, s.focusCurrent()
		}
		return s.tryStart()
	}

	return s, nil
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

func (s *Setup) focusCurrent() tea.Cmd {
	switch s.focus {
	case fieldWord:
		return s.word.Focus()
	case fieldOutDir:
		return s.outDir.Focus()
	}
	return nil
}

// stopSearch cancels a running search without quitting the program — Done()
// firing on the next tick transitions the phase to phaseFinished the same
// way a natural stop condition would, so the dashboard's final state (logs,
// stats, matches so far) stays on screen for review instead of the only way
// to stop being to quit and lose that view.
func (s *Setup) stopSearch() {
	if s.cancel != nil {
		s.cancel()
	}
}

// rearm returns to the setup phase after a stop, so the configuration can be
// reviewed or changed and the search started again. runner.New resumes from
// the persisted cumulative count automatically when the pattern and mode
// are unchanged, so restarting doesn't lose prior progress.
func (s *Setup) rearm() {
	s.phase = phaseSetup
	s.finished = false
	s.appendLog("INFO", "ready to search again — press s to start")
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

	outDir := s.outDir.Value()
	if outDir == "" {
		outDir = defaultOutputDirHint()
	}

	r, err := runner.New(runner.Config{
		Patterns:   pats,
		Mode:       s.mode,
		Threads:    threads,
		OutputDir:  outDir,
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

	s.appendLog("INFO", fmt.Sprintf("VanityRig %s started", s.version))
	s.appendLog("INFO", fmt.Sprintf("target %q (%s, %d threads)", strings.Join(pats, ", "), s.mode, threads))
	s.appendLog("INFO", "output folder: "+outDir)
	s.appendLog("INFO", "starting key generation…")
	return s, nil
}

// logProgress appends a periodic throughput line and an immediate line for
// every new match, so the log reads like an actual event history rather
// than a snapshot that happens to redraw.
func (s *Setup) logProgress() {
	for ; s.matchesLogged < len(s.snap.Matches); s.matchesLogged++ {
		m := s.snap.Matches[s.matchesLogged]
		s.appendLog("MATCH", m.Address+".onion")
	}

	const statInterval = 8 * time.Second
	if s.snap.KeysPerSec <= 0 {
		return
	}
	if !s.lastStatLog.IsZero() && time.Since(s.lastStatLog) < statInterval {
		return
	}
	s.lastStatLog = time.Now()
	s.appendLog("STATS", fmt.Sprintf("%s keys tested | %s/sec", humanCount(s.snap.KeysTried), humanCount(s.snap.KeysPerSec)))
}

func (s *Setup) appendLog(tag, msg string) {
	const maxLines = 8
	line := fmt.Sprintf("[%s] [%-5s] %s", time.Now().Format("15:04:05"), tag, msg)
	s.logLines = append(s.logLines, line)
	if len(s.logLines) > maxLines {
		s.logLines = s.logLines[len(s.logLines)-maxLines:]
	}
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
		width = 100
	}
	if width > 140 {
		width = 140
	}

	var b strings.Builder
	b.WriteString(s.renderHeader(width))
	b.WriteString("\n\n")

	if width >= twoColumnMinWidth {
		rightW := width - width*58/100
		resources := s.renderResources(rightW)
		stats := s.renderStats(rightW)
		right := lipgloss.JoinVertical(lipgloss.Left, resources, stats)

		// Match the configuration panel's height to the combined right
		// column, so their bottom borders line up instead of the shorter
		// one trailing off into blank space — the "why doesn't this line
		// up" complaint a plain lipgloss.JoinHorizontal leaves on the table.
		rightLines := strings.Count(resources, "\n") + strings.Count(stats, "\n") + 2
		const panelOverhead = 3 // top border + title line + bottom border
		left := s.renderConfig(width*58/100-1, rightLines-panelOverhead)

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
	} else {
		b.WriteString(s.renderConfig(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderResources(width))
		b.WriteString("\n")
		b.WriteString(s.renderStats(width))
	}
	b.WriteString("\n")
	b.WriteString(s.renderProgressBar(width))
	b.WriteString("\n")
	b.WriteString(s.renderLogs(width))
	b.WriteString("\n")
	b.WriteString(s.renderFooter(width))
	b.WriteString("\n")
	return b.String()
}

func (s *Setup) renderHeader(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colGood).Render("Vanity") +
		lipgloss.NewStyle().Bold(true).Foreground(colConfig).Render("Rig")
	tagline := stLabel.Render("CPU-powered vanity address generator") + "\n" +
		stLabel.Render("Find your dream address. Bruteforce it.")
	left := title + "  " + strings.SplitN(tagline, "\n", 2)[0] + "\n      " + strings.SplitN(tagline, "\n", 2)[1]

	right := stLabel.Render("v"+s.version) + "\n" + stLabel.Render(repoURL)
	rightBlock := lipgloss.NewStyle().Align(lipgloss.Right).Render(right)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(rightBlock)

	// The version/URL aside is dropped rather than overflowed or wrapped on a
	// terminal too narrow to fit both — same principle the rest of this
	// dashboard uses for asides, and the only safe option here since
	// JoinHorizontal has no truncating mode of its own.
	if gap := width - leftW - rightW; gap >= 1 {
		spacer := lipgloss.NewStyle().Width(gap).Render("")
		return lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, rightBlock)
	}
	return left
}

// footer returns the keybinding hints, shortest first, so a narrow terminal
// can drop the later (less essential) ones instead of overflowing.
func (s *Setup) footer() []string {
	if s.editing {
		return []string{"enter/esc done editing"}
	}

	switch s.phase {
	case phaseRunning:
		return []string{"s stop", "q quit", "l clear log"}
	case phaseFinished:
		return []string{"s restart", "q quit", "l clear log"}
	default:
		return []string{"s start", "enter edit", "q quit", "←/→ change", "↑↓/tab move"}
	}
}

func (s *Setup) renderFooter(width int) string {
	status := stGood.Render("● Running…")
	if s.phase == phaseSetup {
		status = stLabel.Render("● Ready")
	} else if s.finished {
		status = stWarn.Render("■ Stopped")
	}

	// Add hints one at a time only while they still fit, rather than
	// rendering the full set and hoping it happens to be short enough.
	var kept []string
	left := "  "
	for _, h := range s.footer() {
		sep := ""
		if len(kept) > 0 {
			sep = "  ·  "
		}
		candidate := left + sep + h
		if lipgloss.Width(candidate) > width {
			break
		}
		left = candidate
		kept = append(kept, h)
	}
	var styledLeft string
	for i, h := range kept {
		if i == 0 {
			styledLeft = "  " + stHint.Render(h)
		} else {
			styledLeft += stLabel.Render("  ·  ") + stHint.Render(h)
		}
	}

	// Same rule as the header: a right-aligned aside that doesn't fit gets
	// dropped rather than pushed past the terminal's actual width.
	if gap := width - lipgloss.Width(styledLeft) - lipgloss.Width(status); gap >= 1 {
		return styledLeft + lipgloss.NewStyle().Width(gap).Render("") + status
	}
	return styledLeft
}

// panel wraps body in a titled, coloured border. minBodyLines pads the body
// with blank lines (inside the border, not after it) up to that count — used
// to match the configuration panel's height to the resources+statistics
// column beside it, so the two columns' bottom borders align instead of one
// trailing off into blank space below a shorter box.
func panel(title string, width, minBodyLines int, body string, accent lipgloss.AdaptiveColor) string {
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	lines := strings.Split(body, "\n")
	for len(lines) < minBodyLines {
		lines = append(lines, "")
	}
	body = strings.Join(lines, "\n")

	style := lipgloss.NewStyle().
		Border(panelBorder).
		BorderForeground(accent).
		Padding(0, 1).
		Width(inner)
	titled := stPanelTitle.Foreground(accent).Render(title)
	return style.Render(titled + "\n" + body)
}

func (s *Setup) renderConfig(width, minBodyLines int) string {
	editable := s.phase == phaseSetup
	row := func(label, value string, focused bool) string {
		l := stFieldLabel.Render(pad(label, 14))
		if focused && editable {
			l = stFocused.Render(pad("▸ "+label, 14))
		}
		return "  " + l + " " + value + "\n"
	}

	// textinput's placeholder renders as a single character when Width is
	// left at its zero value — despite the docs saying 0 means "unlimited" —
	// so it must be set explicitly, and reset on every render to track the
	// panel's actual available width rather than a guessed constant.
	fieldWidth := width - 20
	if fieldWidth < 10 {
		fieldWidth = 10
	}
	s.word.Width = fieldWidth
	s.outDir.Width = fieldWidth

	// The live textinput view (with its blinking cursor) only makes sense
	// while actively editing that field — otherwise a tabbed-to-but-not-yet-
	// entered field would show a cursor even though typing a letter right
	// now triggers a global shortcut (s/q/l), not text entry, which would be
	// misleading. So navigating shows a static rendering of the value, and
	// only Enter (or a click) on the field switches it to the live view.
	wordEditing := editable && s.editing && s.focus == fieldWord
	outEditing := editable && s.editing && s.focus == fieldOutDir

	var wordView string
	switch {
	case wordEditing:
		wordView = s.word.View()
	case s.word.Value() != "":
		wordView = stValue.Render(strings.Join(s.patterns(), ", "))
	default:
		wordView = stLabel.Render(s.word.Placeholder)
	}

	var outView string
	if outEditing {
		outView = s.outDir.View()
	} else {
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
	b.WriteString(row("CPU share", cpuRow(s.percent), s.focus == fieldThreads))
	b.WriteString("  " + stFieldLabel.Render(pad("GPU", 14)) + " " + stLabel.Render("coming soon") + "\n")

	if s.startErr != "" {
		b.WriteString("\n  " + stErr.Render("! "+s.startErr))
	}

	return panel("⚙ Configuration", width, minBodyLines, strings.TrimRight(b.String(), "\n"), colConfig)
}

// modeRow shows the current match mode as a single cycling value — "‹
// Anywhere ›" — the same shape every other configuration field uses,
// rather than the earlier design that laid out all three modes inline as a
// row of options. The full prefix/suffix/anywhere comparison already has a
// home (the Statistics panel), so this field doesn't need to duplicate it;
// it just needs to say what's currently chosen and that arrow keys change
// it, consistent with CPU share and everything else in this panel.
func modeRow(cur vanity.MatchMode, focused bool) string {
	value := stValue.Render(capitalize(string(cur)))
	line := "‹ " + value + " ›"
	if focused {
		line += stHint.Render("  ←/→")
	}
	return line
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// cpuRow's core count intentionally isn't repeated here — the Resources
// panel's own note line already says it — so this row stays short enough
// that Match mode, not this one, is what decides how narrow the two-column
// layout can safely go.
func cpuRow(percent int) string {
	const barWidth = 14
	filled := int(float64(barWidth) * float64(percent) / 100)
	bar := lipgloss.NewStyle().Foreground(colConfig).Render(strings.Repeat("█", filled)) +
		stLabel.Render(strings.Repeat("░", barWidth-filled))
	return fmt.Sprintf("%s  %3d%%", bar, percent)
}

// renderResources shows the CPU share actually configured (never invented
// OS telemetry we don't measure) and marks GPU plainly as unavailable,
// rather than a fake utilization number for hardware nothing here uses.
func (s *Setup) renderResources(width int) string {
	const barWidth = 14
	percent := s.percent
	if s.phase == phaseSetup {
		percent = 0 // nothing is actually running yet
	}
	filled := int(float64(barWidth) * float64(percent) / 100)
	cpuBar := lipgloss.NewStyle().Foreground(colResources).Render(strings.Repeat("█", filled)) +
		stLabel.Render(strings.Repeat("░", barWidth-filled))
	cores := int(math.Round(float64(runtime.NumCPU()) * float64(s.percent) / 100))
	if cores < 1 {
		cores = 1
	}

	gpuBar := stLabel.Render(strings.Repeat("░", barWidth))

	// This line's a sentence, not a fixed-format row, so it's the one most
	// likely to overflow a narrow panel — truncate it to what the panel can
	// actually hold rather than trust the string is always short enough.
	inner := width - 4
	note := fmt.Sprintf("using %d of %d cores · GPU not available yet", cores, runtime.NumCPU())
	note = truncateTo(note, inner-4)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %-4s %s  %3d%%\n", "CPU", cpuBar, percent))
	b.WriteString(fmt.Sprintf("  %-4s %s  %s\n", "GPU", gpuBar, stLabel.Render("n/a")))
	b.WriteString(stLabel.Render("  " + note))
	return panel("📊 Resources", width, 0, strings.TrimRight(b.String(), "\n"), colResources)
}

func (s *Setup) renderStats(width int) string {
	if s.phase == phaseSetup {
		return panel("📈 Statistics", width, 0, s.renderPreview(), colStats)
	}
	return panel("📈 Statistics", width, 0, s.renderLiveStats(), colStats)
}

// renderPreview shows the same prefix/suffix/anywhere cost comparison the
// non-interactive -check output prints, live-updated as the word changes,
// so the configuration panel's mode selector isn't a blind choice.
func (s *Setup) renderPreview() string {
	pats := s.patterns()
	if len(pats) == 0 {
		return stLabel.Render("Type a word to see cost\nestimates for each mode.")
	}
	cmp := vanity.CompareModes(pats, s.effectiveRate())

	var b strings.Builder
	for _, m := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		e := cmp.Estimates[m]
		timing := humanDur(e.P50)
		note := ""
		switch {
		case e.Probability <= 0:
			timing = "impossible"
		case m == cmp.Best:
			note = stGood.Render("← best")
		}
		marker := "  "
		if m == s.mode {
			marker = stFocused.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-10s %-12s %s\n", marker, m, timing, note))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Setup) renderLiveStats() string {
	snap := s.snap
	labelWidth := 15

	eta := "—"
	if snap.Estimate.Probability > 0 && snap.KeysPerSec > 0 && !vanity.Saturated(snap.Estimate.P50) {
		eta = humanDur(snap.Estimate.P50)
	}

	var b strings.Builder
	b.WriteString(row2("Keys Tested", humanCount(snap.KeysTried), labelWidth))
	b.WriteString(row2("Hash Rate", rate(snap.KeysPerSec), labelWidth))
	b.WriteString(row2("Elapsed", roughElapsed(snap.SessionTime), labelWidth))
	b.WriteString(row2("Est. Time (50%)", eta, labelWidth))
	b.WriteString(row2("Matches Found", fmt.Sprintf("%d", len(snap.Matches)), labelWidth))
	return strings.TrimRight(b.String(), "\n")
}

func roughElapsed(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func (s *Setup) renderProgressBar(width int) string {
	plain := "Type a word above and press enter to begin."
	frac := 0.0
	if s.phase == phaseSetup {
		// Reinforces Statistics' per-mode breakdown with the one number that
		// actually matters once a mode is chosen: how long *this* search,
		// as currently configured, is expected to take — live as you type
		// or change the mode, not just once you've already committed to it.
		if pats := s.patterns(); len(pats) > 0 {
			est := vanity.NewEstimate(pats, s.mode, s.effectiveRate())
			eta := "impossible in this mode"
			if est.Probability > 0 {
				eta = "typically " + humanDur(est.P50)
			}
			plain = fmt.Sprintf("Ready to search %s for %q — %s. Press enter to begin.",
				s.mode, strings.Join(pats, ", "), eta)
		}
	} else {
		if s.snap.Estimate.Probability > 0 {
			frac = 1 - expNeg(s.snap.Estimate.Probability*s.snap.KeysTried)
		}
		plain = fmt.Sprintf("Searching for %s with %q", s.mode, strings.Join(s.patterns(), ", "))
	}
	// Truncate the plain text before styling it — truncating an
	// already-ANSI-styled string risks cutting mid-escape-sequence.
	label := stLabel.Render(truncateTo(plain, width-4))

	barWidth := width - 12
	if barWidth < 10 {
		barWidth = 10
	}
	filled := int(frac * float64(barWidth))
	bar := lipgloss.NewStyle().Foreground(colProgress).Render(strings.Repeat("█", filled)) +
		stLabel.Render(strings.Repeat("░", barWidth-filled))

	body := label + "\n" + bar + fmt.Sprintf("  %3.0f%%", frac*100)
	return panel("◆ Progress", width, 0, body, colProgress)
}

func (s *Setup) renderLogs(width int) string {
	var body string
	if len(s.logLines) == 0 {
		body = stLabel.Render("Nothing logged yet — start a search to see live events here.")
	} else {
		var lines []string
		for _, l := range s.logLines {
			lines = append(lines, styleLogLine(l))
		}
		body = strings.Join(lines, "\n")
	}
	return panel("📜 Logs", width, 0, body, colLogs)
}

// styleLogLine colours the [TAG] token so MATCH and errors stand out from
// routine INFO/STATS lines at a glance.
func styleLogLine(line string) string {
	first := strings.Index(line, "]")
	if first < 0 || first+1 >= len(line) {
		return stLabel.Render(line)
	}
	rest := line[first+1:]
	tagStart := strings.Index(rest, "[")
	if tagStart < 0 {
		return stLabel.Render(line)
	}
	tagEnd := strings.Index(rest[tagStart:], "]")
	if tagEnd < 0 {
		return stLabel.Render(line)
	}
	tagStart += first + 1
	tagEnd += tagStart
	tag := line[tagStart : tagEnd+1]

	style := stLabel
	switch {
	case strings.Contains(tag, "MATCH"):
		style = stGood
	case strings.Contains(tag, "STATS"):
		style = stFocused
	}
	return stLabel.Render(line[:tagStart]) + style.Render(tag) + stLabel.Render(line[tagEnd+1:])
}

func row2(label, value string, labelWidth int) string {
	return fmt.Sprintf("  %s %s\n", stLabel.Render(pad(label, labelWidth)), value)
}

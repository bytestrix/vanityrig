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
	"github.com/charmbracelet/bubbles/viewport"
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
	colConfig    = lipgloss.AdaptiveColor{Light: "#0284c7", Dark: "#38bdf8"}
	colResources = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34d399"}
	colStats     = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fbbf24"}
	colProgress  = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#c084fc"}
	colLogs      = lipgloss.AdaptiveColor{Light: "#0284c7", Dark: "#38bdf8"}
	colBarEmpty  = lipgloss.AdaptiveColor{Light: "#334155", Dark: "#1e293b"}

	stPanelTitle = lipgloss.NewStyle().Bold(true)
	stFocused    = lipgloss.NewStyle().Foreground(colConfig).Bold(true)
	stFieldLabel = lipgloss.NewStyle().Foreground(colDim)
	stBarEmpty   = lipgloss.NewStyle().Foreground(colBarEmpty)
	panelBorder  = lipgloss.RoundedBorder()
)

// spinnerFrames is the Braille animation shown in the footer while a search runs.
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const repoURL = "https://github.com/bytestrix/vanityrig"

const twoColumnMinWidth = 100

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
	logLines      []logEntry
	logLevel      logLevel
	lastStatLog   time.Time
	matchesLogged int
	errorsLogged  int

	startedAt    time.Time // when the current/most recent search started, for Session Info
	speedHistory []float64 // recent KeysPerSec samples, for the Speed History sparkline

	// UI state for scroll and animation.
	spinTick int // incremented each tick to drive the Braille spinner
	viewport viewport.Model
	vpReady  bool // true once the first WindowSizeMsg has been processed
}

// logLevel filters which log lines are shown, cycled with "v". MATCH lines
// bypass the filter entirely — a search finding what it was asked to find is
// never something to hide behind a verbosity setting.
type logLevel int

const (
	logInfo  logLevel = iota // default: lifecycle events + matches, no raw throughput spam
	logWarn                  // only problems + matches
	logDebug                 // everything, including periodic throughput samples
)

func (l logLevel) String() string {
	switch l {
	case logWarn:
		return "warn"
	case logDebug:
		return "debug"
	default:
		return "info"
	}
}

func (l logLevel) next() logLevel { return (l + 1) % 3 }

// logEntry is one line in the Logs panel, tagged with a severity so the
// panel can filter without re-parsing rendered text.
type logEntry struct {
	tag   string
	text  string
	level logLevel
}

// levelOf classifies a log tag: STATS is routine telemetry (debug-tier,
// hidden by default), WARN is an actual problem, everything else (INFO,
// MATCH) is normal operational info.
func levelOf(tag string) logLevel {
	switch tag {
	case "STATS":
		return logDebug
	case "WARN":
		return logWarn
	default:
		return logInfo
	}
}

// visible reports whether an entry should show under filter f — MATCH always
// does, everything else needs to be at or above f's severity (info < warn <
// debug, i.e. a stricter filter hides more).
func (e logEntry) visible(f logLevel) bool {
	if e.tag == "MATCH" {
		return true
	}
	switch f {
	case logDebug:
		return true
	case logWarn:
		return e.level == logWarn
	default:
		return e.level != logDebug
	}
}

func fmtWithCommas(n float64) string {
	in := fmt.Sprintf("%.0f", n)
	out := make([]byte, 0, len(in)+(len(in)-1)/3)
	for i, c := range []byte(in) {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// NewSetup builds the combined screen, pre-filled from cfg.
func NewSetup(cfg SetupConfig) *Setup {
	w := textinput.New()
	w.Placeholder = "e.g. sat0shi or dream, word"
	w.CharLimit = 64

	if len(cfg.Words) > 0 {
		w.SetValue(strings.Join(cfg.Words, ", "))
	}

	o := textinput.New()
	o.Placeholder = defaultOutputDirHint()
	o.CharLimit = 256
	if cfg.OutDir != "" {
		o.SetValue(cfg.OutDir)
	}

	percent := 100
	if cfg.Threads > 0 && runtime.NumCPU() > 0 {
		percent = clampPercent(int(math.Round(float64(cfg.Threads) * 100 / float64(runtime.NumCPU()))))
	}

	mode := cfg.Mode
	modeIsFixed := cfg.Mode != ""

	rate := cfg.Rate
	if rate <= 0 {
		rate = defaultBenchRate()
	}

	ver := cfg.Version
	if ver == "" {
		ver = "dev"
	}

	s := &Setup{
		word:        w,
		outDir:      o,
		mode:        mode,
		modeIsFixed: modeIsFixed,
		percent:     percent,
		stop:        cfg.StopAfter,
		rate:        rate,
		enginePath:  cfg.EnginePath,
		version:     ver,
		focus:       fieldWord,
		logLevel:    logInfo,
	}
	s.word.Focus()
	s.recomputeMode()
	return s
}

// abbreviateHome shortens a path under the user's home directory to a "~/"
// form for display — the Configuration column is narrow by design (most of
// the screen is telemetry, not settings), so the full expanded path easily
// overflowed it. The functional value (what's actually passed to the
// runner) is untouched; this only affects what's shown on screen.
func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func defaultOutputDirHint() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.vanityrig/keys"
	}
	return filepath.Join(home, ".vanityrig", "keys")
}

func defaultBenchRate() float64 { return 22.2e6 }

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
func (s *Setup) patterns() []string {
	v := strings.TrimSpace(s.word.Value())
	if v == "" {
		return nil
	}
	raw := strings.Split(v, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

// recomputeMode follows the recommended mode as the word changes, until the
// user (or an explicit -match) has pinned one down.
func (s *Setup) recomputeMode() {
	if s.modeIsFixed {
		return
	}
	pats := s.patterns()
	if len(pats) == 0 {
		s.mode = vanity.MatchPrefix
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
		// Viewport height: reserve 2 lines for the header, 1 blank spacer,
		// 1 footer line, and 1 trailing newline = 5 lines total.
		vpHeight := s.height - 5
		if vpHeight < 5 {
			vpHeight = 5
		}
		vpWidth := msg.Width
		if vpWidth > 240 {
			vpWidth = 240
		}
		if !s.vpReady {
			s.viewport = viewport.New(vpWidth, vpHeight)
			s.vpReady = true
		} else {
			s.viewport.Width = vpWidth
			s.viewport.Height = vpHeight
		}
		s.viewport.SetContent(s.renderBody(vpWidth))
		return s, nil

	case tickMsg:
		if s.phase == phaseRunning {
			s.snap = s.run.Snapshot()
			s.logProgress()
			s.recordSpeedSample()
			select {
			case <-s.run.Done():
				s.finished = true
				s.phase = phaseFinished
				s.appendLog("INFO", "search finished")
			default:
			}
		}
		s.spinTick++
		if s.vpReady {
			w := s.viewport.Width
			if w <= 0 {
				w = s.width
			}
			if w > 240 {
				w = 240
			}
			s.viewport.SetContent(s.renderBody(w))
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

	case "v":
		s.logLevel = s.logLevel.next()
		return s, nil

	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		// These scroll keys work in every phase.
		if s.vpReady {
			var cmd tea.Cmd
			s.viewport, cmd = s.viewport.Update(msg)
			return s, cmd
		}
		return s, nil
	}

	if s.phase != phaseSetup {
		// In running/finished phases, ↑/↓/j/k also scroll the viewport.
		if s.vpReady {
			switch key {
			case "up", "k", "down", "j":
				var cmd tea.Cmd
				s.viewport, cmd = s.viewport.Update(msg)
				return s, cmd
			}
		}
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
		s.refreshViewport()
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

// refreshViewport re-renders the body into the viewport immediately after a
// key event changes visible state but before the next tick fires. Without
// this, View() would serve stale content set by the previous tick.
func (s *Setup) refreshViewport() {
	if !s.vpReady {
		return
	}
	w := s.viewport.Width
	if w <= 0 {
		w = s.width
	}
	if w > 240 {
		w = 240
	}
	s.viewport.SetContent(s.renderBody(w))
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
	s.startedAt = time.Now()
	s.speedHistory = nil
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

// recordSpeedSample keeps a rolling window of real observed throughput for
// the Speed History sparkline — the same KeysPerSec the Statistics panel
// already shows, just kept over time instead of only the latest reading.
func (s *Setup) recordSpeedSample() {
	const maxSamples = 120
	if s.snap.KeysPerSec <= 0 {
		return
	}
	s.speedHistory = append(s.speedHistory, s.snap.KeysPerSec)
	if len(s.speedHistory) > maxSamples {
		s.speedHistory = s.speedHistory[len(s.speedHistory)-maxSamples:]
	}
}

// logProgress appends a periodic throughput line and an immediate line for
// every new match, so the log reads like an actual event history rather
// than a snapshot that happens to redraw.
func (s *Setup) logProgress() {
	for ; s.matchesLogged < len(s.snap.Matches); s.matchesLogged++ {
		m := s.snap.Matches[s.matchesLogged]
		s.appendLog("MATCH", m.Address+".onion")
	}

	// Errors the engine reported (e.g. a match write failing) were previously
	// tracked in the snapshot but never actually surfaced anywhere in this
	// dashboard — logging them as WARN closes that gap and gives the "warn"
	// filter level something real to show.
	for ; s.errorsLogged < len(s.snap.Errors); s.errorsLogged++ {
		s.appendLog("WARN", s.snap.Errors[s.errorsLogged])
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
	const maxStored = 300
	text := fmt.Sprintf("[%s]  [%-5s]  %s", time.Now().Format("15:04:05"), tag, msg)
	s.logLines = append(s.logLines, logEntry{tag: tag, text: text, level: levelOf(tag)})
	if len(s.logLines) > maxStored {
		s.logLines = s.logLines[len(s.logLines)-maxStored:]
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

const panelOverhead = 3 // top border + title line + bottom border

// alignRow renders a row of same-height panels: each renderer runs once to
// measure its natural height, then any that came out shorter than the
// tallest are re-rendered padded to match — so a row's bottom borders line
// up instead of the shortest box trailing off into blank space beside a
// taller neighbor.
func alignRow(widths []int, renderers ...func(width, minBodyLines int) string) []string {
	rendered := make([]string, len(renderers))
	lineCounts := make([]int, len(renderers))
	maxLines := 0
	for i, r := range renderers {
		rendered[i] = r(widths[i], 0)
		lineCounts[i] = strings.Count(rendered[i], "\n") + 1
		if lineCounts[i] > maxLines {
			maxLines = lineCounts[i]
		}
	}
	for i, r := range renderers {
		if lineCounts[i] < maxLines {
			rendered[i] = r(widths[i], maxLines-panelOverhead)
		}
	}
	return rendered
}

func joinRow(parts []string) string {
	spaced := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			spaced = append(spaced, " ")
		}
		spaced = append(spaced, p)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, spaced...)
}

// View renders the dashboard. When the viewport is ready (after the first
// WindowSizeMsg), all panel content is wrapped in a scrollable viewport so
// nothing is ever clipped on a small terminal — the user can always reach
// every panel by scrolling with pgup/pgdown or ↑/↓ (during a search).
func (s *Setup) View() string {
	width := s.width
	if width <= 0 {
		width = 100
	}
	// 240 is a sanity ceiling; a real monitoring dashboard fills the window.
	if width > 240 {
		width = 240
	}

	header := s.renderHeader(width)
	footer := s.renderFooter(width)

	if s.vpReady {
		// Header and footer are pinned outside the viewport so they remain
		// visible at all scroll positions.
		return header + "\n\n" + s.viewport.View() + "\n" + footer + "\n"
	}
	// Viewport not yet initialised (before the first WindowSizeMsg):
	// render everything inline so the very first frame is never blank.
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n")
	b.WriteString(s.renderBody(width))
	b.WriteString(footer)
	b.WriteString("\n")
	return b.String()
}

// renderBody produces all panel content between the header and footer.
// It is called from both View() and the tickMsg/WindowSizeMsg handlers to
// keep the viewport content current.
func (s *Setup) renderBody(width int) string {
	var b strings.Builder

	if width >= twoColumnMinWidth {
		wLeft := width*50/100 - 1
		wRight := width - wLeft - 1

		resStr := s.renderResources(wRight, 0)
		statsStr := s.renderStats(wRight, 0)
		rightCol1 := resStr + "\n" + statsStr

		rightLines := strings.Count(rightCol1, "\n") + 1
		configMinBody := rightLines - panelOverhead
		if configMinBody < 0 {
			configMinBody = 0
		}

		configStr := s.renderConfig(wLeft, configMinBody)
		topRow := lipgloss.JoinHorizontal(lipgloss.Top, configStr, " ", rightCol1)

		b.WriteString(topRow)
		b.WriteString("\n")

		diffStr := s.renderDifficulty(wLeft, 0)
		histStr := s.renderSpeedHistory(wRight, 0)
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, diffStr, " ", histStr)
		b.WriteString(row2)
		b.WriteString("\n")

		b.WriteString(s.renderProgressBar(width))
		b.WriteString("\n")

		sessStr := s.renderSessionInfo(wLeft, 0)
		gpuStr := s.renderGPUStatus(wRight, 0)
		row3 := lipgloss.JoinHorizontal(lipgloss.Top, sessStr, " ", gpuStr)
		b.WriteString(row3)
		b.WriteString("\n")

		b.WriteString(s.renderMatches(width, 0))
		b.WriteString("\n")
	} else {
		b.WriteString(s.renderConfig(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderResources(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderStats(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderDifficulty(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderSpeedHistory(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderSessionInfo(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderGPUStatus(width, 0))
		b.WriteString("\n")
		b.WriteString(s.renderProgressBar(width))
		b.WriteString("\n")
		b.WriteString(s.renderMatches(width, 0))
		b.WriteString("\n")
	}

	// Logs: expand to fill remaining space — viewport height when scrolling
	// is active, terminal height otherwise (same behaviour as before).
	usedLines := strings.Count(b.String(), "\n") + 1
	const fixedOverhead = 2 // spacer + margin
	ref := s.height
	if s.vpReady {
		ref = s.viewport.Height
	}
	logsMinBody := 8
	if ref > 0 {
		avail := ref - usedLines - panelOverhead - fixedOverhead
		if avail > logsMinBody {
			logsMinBody = avail
		}
	}
	if logsMinBody > 200 {
		logsMinBody = 200
	}
	b.WriteString(s.renderLogs(width, logsMinBody))
	b.WriteString("\n")

	return b.String()
}

func (s *Setup) renderHeader(width int) string {
	colTitleCyan := lipgloss.AdaptiveColor{Light: "#0284c7", Dark: "#38bdf8"}
	colTitleBlue := lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#60a5fa"}

	vStyle := lipgloss.NewStyle().Bold(true).Foreground(colTitleCyan)
	rStyle := lipgloss.NewStyle().Bold(true).Foreground(colTitleBlue)
	title := vStyle.Render("Vanity") + rStyle.Render("Rig")

	titleWidth := lipgloss.Width("VanityRig")
	sub1Max := width - titleWidth - 3
	if sub1Max < 10 {
		sub1Max = 10
	}
	// Not "GPU/CPU powered": there's no GPU engine yet (GPU Status panel
	// says so plainly) — the tagline shouldn't claim support that doesn't
	// exist while the panel right below it says "coming soon".
	sub1Text := truncateTo("CPU-powered vanity address generator", sub1Max)
	sub1 := stLabel.Render(sub1Text)
	sub2Text := truncateTo("Find your dream address. Bruteforce it.", sub1Max)
	sub2 := stLabel.Render(sub2Text)

	left := title + "   " + sub1 + "\n" +
		strings.Repeat(" ", titleWidth+3) + sub2

	ver := lipgloss.NewStyle().Foreground(colTitleCyan).Render("v" + s.version)
	url := lipgloss.NewStyle().Foreground(colTitleCyan).Render(repoURL)
	right := ver + "\n" + url
	rightBlock := lipgloss.NewStyle().Align(lipgloss.Right).Render(right)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(rightBlock)

	if gap := width - leftW - rightW; gap >= 1 {
		spacer := lipgloss.NewStyle().Width(gap).Render("")
		return lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, rightBlock)
	}
	return left
}

// footer returns the keybinding hints in [Key] Label format (matching the
// reference screenshot). Shorter list first so a narrow terminal can drop
// the later, less critical ones without overflowing.
func (s *Setup) footer() []string {
	if s.editing {
		return []string{"enter Done", "esc Cancel"}
	}
	switch s.phase {
	case phaseRunning:
		return []string{"s Start/Stop", "q Quit", "v Log Level", "l Clear Log", "pgup/dn Scroll"}
	case phaseFinished:
		return []string{"s Restart", "q Quit", "v Log Level", "l Clear Log", "pgup/dn Scroll"}
	default:
		return []string{"↑↓ Navigate", "↔ Change", "enter Edit", "s Start", "q Quit", "v Log Level", "pgup/dn Scroll"}
	}
}

// keyBadge renders a keyboard hint as a coloured chip: [key] description.
// h must be in "key description" form, e.g. "s stop".
func keyBadge(h string) string {
	i := strings.Index(h, " ")
	if i < 0 {
		return stHint.Render(h)
	}
	key := lipgloss.NewStyle().Bold(true).Foreground(colConfig).Render("[" + h[:i] + "]")
	return key + stHint.Render(h[i:])
}

func (s *Setup) renderFooter(width int) string {
	// Animated Braille spinner while running; static indicators otherwise.
	var status string
	switch {
	case s.phase == phaseSetup:
		status = stLabel.Render("◉ Ready")
	case s.finished:
		status = stWarn.Render("○ Stopped")
	default:
		frame := spinnerFrames[s.spinTick%len(spinnerFrames)]
		status = stGood.Render(frame + " Running...")
	}

	// Append a scroll percentage when the content extends beyond the viewport.
	if s.vpReady && (s.viewport.YOffset > 0 || !s.viewport.AtBottom()) {
		pct := int(s.viewport.ScrollPercent() * 100)
		status += stLabel.Render(fmt.Sprintf("  %d%%↕", pct))
	}

	badges := []string{
		"[↑↓] Navigate",
		"[↔] Change Value",
		"[Enter] Edit",
		"[S] Start/Stop",
		"[R] Reset Stats",
		"[O] Open Output",
		"[Q] Quit",
	}

	var kept []string
	var styledLeft string
	for _, b := range badges {
		sepStyled := ""
		if len(kept) > 0 {
			sepStyled = stLabel.Render("   ")
		}
		keyEnd := strings.Index(b, "]")
		var badgeStyled string
		if keyEnd > 0 {
			badgeStyled = lipgloss.NewStyle().Foreground(colConfig).Bold(true).Render(b[:keyEnd+1]) +
				stHint.Render(b[keyEnd+1:])
		} else {
			badgeStyled = stHint.Render(b)
		}
		candidate := styledLeft + sepStyled + badgeStyled
		if lipgloss.Width("  "+candidate) > width-lipgloss.Width(status)-1 {
			break
		}
		styledLeft = candidate
		kept = append(kept, b)
	}
	if styledLeft != "" {
		styledLeft = "  " + styledLeft
	}

	// Drop the status aside rather than pushing it past the terminal's edge.
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
		l := stFieldLabel.Render(pad(label, 15))
		if focused && editable {
			l = stFocused.Render(pad("▸ "+label, 15))
		}
		return "  " + l + " " + value + "\n"
	}

	fieldWidth := width - 24
	if fieldWidth < 6 {
		fieldWidth = 6
	}
	s.word.Width = fieldWidth
	s.outDir.Width = fieldWidth

	wordEditing := editable && s.editing && s.focus == fieldWord
	outEditing := editable && s.editing && s.focus == fieldOutDir

	var wordView string
	switch {
	case wordEditing:
		wordView = s.word.View()
	case s.word.Value() != "":
		wordView = stValue.Render(truncateTo(strings.Join(s.patterns(), ", "), fieldWidth))
	default:
		wordView = stLabel.Render(truncateTo(s.word.Placeholder, fieldWidth))
	}

	var outView string
	if outEditing {
		outView = s.outDir.View()
	} else {
		out := abbreviateHome(s.outDir.Value())
		if out == "" {
			out = abbreviateHome(defaultOutputDirHint())
		}
		outView = stValue.Render(truncateTo(out, fieldWidth))
	}

	var b strings.Builder
	b.WriteString(row("Word(s)", wordView, s.focus == fieldWord))
	b.WriteString(row("Save to", outView, s.focus == fieldOutDir))
	b.WriteString(row("Match mode", modeRow(s.mode, s.focus == fieldMode && editable), s.focus == fieldMode))
	b.WriteString(row("CPU share", cpuRow(s.percent, barWidthFor(width, 24)), s.focus == fieldThreads))

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

func barWidthFor(panelWidth, reserved int) int {
	w := panelWidth - 6 - reserved
	if w < 4 {
		w = 4
	}
	if w > 60 {
		w = 60
	}
	return w
}

func cpuRow(percent, barWidth int) string {
	filled := int(float64(barWidth) * float64(percent) / 100)
	bar := lipgloss.NewStyle().Foreground(colConfig).Render(strings.Repeat("█", filled)) +
		stBarEmpty.Render(strings.Repeat("░", barWidth-filled))
	return fmt.Sprintf("%s  %3d%%", bar, percent)
}

// renderResources shows the CPU share actually configured and, where the
// kernel exposes one, a real measured package temperature — never invented
// OS telemetry (GPU utilization, VRAM, memory) for anything this can't
// actually read.
func (s *Setup) renderResources(width int, minBodyLines int) string {
	cores := runtime.NumCPU()
	threads := int(math.Round(float64(cores) * float64(s.percent) / 100))
	if threads < 1 {
		threads = 1
	}

	percent := s.percent
	if s.phase == phaseSetup {
		percent = 0 // nothing is actually running yet
	}

	barW := barWidthFor(width, 24)
	cpuFilled := int(float64(barW) * float64(percent) / 100)
	cpuBar := lipgloss.NewStyle().Foreground(colConfig).Render(strings.Repeat("█", cpuFilled)) +
		stBarEmpty.Render(strings.Repeat("░", barW-cpuFilled))
	cpuLabel := fmt.Sprintf("CPU (%d threads)", cores)
	cpuLine := fmt.Sprintf("  %-16s %s  %3d%%   %d / %d threads\n", cpuLabel, cpuBar, percent, threads, cores)

	tempStr := "n/a"
	if c, ok := cpuTempC(); ok {
		tempStr = fmt.Sprintf("%.0f°C", c)
	}
	tempLine := fmt.Sprintf("  %-16s %s", "Temp", stValue.Render(tempStr))

	var b strings.Builder
	b.WriteString(cpuLine)
	b.WriteString(tempLine)

	return panel("📊 System Resources", width, minBodyLines, b.String(), colResources)
}

func (s *Setup) renderGPUStatus(width int, minBodyLines int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %-13s %s\n", "CUDA Support", stLabel.Render("coming soon")))
	b.WriteString(fmt.Sprintf("  %-13s %s\n", "Device", stLabel.Render("n/a")))
	b.WriteString(fmt.Sprintf("  %-13s %s", "VRAM", stLabel.Render("n/a")))
	return panel("🖥 GPU Status", width, minBodyLines, b.String(), colResources)
}

func (s *Setup) renderStats(width, minBodyLines int) string {
	labelWidth := 20
	keysTested := "0"
	if s.snap.KeysTried > 0 {
		keysTested = fmtWithCommas(s.snap.KeysTried)
	}

	hashRate := "0.0 keys/s"
	if s.snap.KeysPerSec > 0 {
		hashRate = humanCount(s.snap.KeysPerSec) + " keys/s"
	}

	elapsed := "00:00:00"
	if s.snap.SessionTime > 0 {
		elapsed = roughElapsed(s.snap.SessionTime)
	}

	eta := "~ n/a"
	if pats := s.patterns(); len(pats) > 0 {
		est := vanity.NewEstimate(pats, s.mode, s.effectiveRate())
		if est.Probability > 0 {
			eta = "~ " + humanDur(est.P50)
		}
	}

	matches := fmt.Sprintf("%d", len(s.snap.Matches))

	var b strings.Builder
	b.WriteString(row2("Keys Tested", keysTested, labelWidth))
	b.WriteString(row2("Hash Rate", hashRate, labelWidth))
	b.WriteString(row2("Elapsed Time", elapsed, labelWidth))
	b.WriteString(row2("Estimated Time", eta, labelWidth))
	b.WriteString(row2("Matches Found", matches, labelWidth))

	return panel("📊 Statistics", width, minBodyLines, strings.TrimRight(b.String(), "\n"), colStats)
}

func (s *Setup) renderDifficulty(width, minBodyLines int) string {
	pats := s.patterns()
	var body string
	switch {
	case len(pats) == 0:
		body = stLabel.Render("Type a word to see cost\nestimates for each mode.")
	default:
		cmp := vanity.CompareModes(pats, s.effectiveRate())
		var b strings.Builder
		b.WriteString(stLabel.Render(fmt.Sprintf("  %-10s %-12s %s", "Mode", "Est. Time", "Complexity")))
		b.WriteString("\n")
		for _, m := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
			e := cmp.Estimates[m]
			timing := humanDur(e.P50)
			bits := fmt.Sprintf("2^%.0f", e.Bits)
			note := ""
			switch {
			case e.Probability <= 0:
				timing = "impossible"
				bits = ""
			case m == cmp.Best:
				note = stGood.Render("← best")
			}
			marker := "  "
			if m == s.mode {
				marker = stFocused.Render("▸ ")
			}
			b.WriteString(fmt.Sprintf("%s%-10s %-12s %-9s %s\n", marker, m, timing, bits, note))
		}
		body = strings.TrimRight(b.String(), "\n")
	}
	return panel("Σ Difficulty Analysis", width, minBodyLines, body, colStats)
}

func (s *Setup) renderSpeedHistory(width, minBodyLines int) string {
	barWidth := width - 6
	if barWidth < 10 {
		barWidth = 10
	}

	var body string
	if len(s.speedHistory) == 0 {
		body = stLabel.Render("No samples yet — starts once a search is running.")
	} else {
		var cur, max, sum float64
		for _, v := range s.speedHistory {
			sum += v
			if v > max {
				max = v
			}
		}
		cur = s.speedHistory[len(s.speedHistory)-1]
		avg := sum / float64(len(s.speedHistory))

		graph := lipgloss.NewStyle().Foreground(colProgress).Render(sparkline(s.speedHistory, barWidth))
		plainStats := fmt.Sprintf("cur %s/sec  ·  avg %s/sec  ·  max %s/sec",
			humanCount(cur), humanCount(avg), humanCount(max))
		body = graph + "\n" + stLabel.Render(truncateTo(plainStats, width-6))
	}
	return panel("📈 Speed History", width, minBodyLines, body, colStats)
}

func (s *Setup) renderSessionInfo(width, minBodyLines int) string {
	labelWidth := 12
	started := "—"
	if !s.startedAt.IsZero() {
		started = s.startedAt.Format("15:04:05")
	}
	status := "ready"
	switch {
	case s.phase == phaseRunning:
		status = "running"
	case s.finished:
		status = "stopped"
	}
	outDir := s.outDir.Value()
	if outDir == "" {
		outDir = defaultOutputDirHint()
	}

	var b strings.Builder
	b.WriteString(row2("Started At", started, labelWidth))
	b.WriteString(row2("Runtime", roughElapsed(s.snap.SessionTime), labelWidth))
	b.WriteString(row2("Keys Tested", humanCount(s.snap.KeysTried), labelWidth))
	b.WriteString(row2("Output Dir", truncateTo(outDir, width-4-labelWidth-2), labelWidth))
	b.WriteString(row2("Status", status, labelWidth))
	return panel("ℹ Session Info", width, minBodyLines, strings.TrimRight(b.String(), "\n"), colStats)
}

func (s *Setup) renderMatches(width, minBodyLines int) string {
	const shown = 6
	matches := s.snap.Matches

	var body string
	if len(matches) == 0 {
		body = stLabel.Render("No matches yet.")
	} else {
		recent := matches
		if len(recent) > shown {
			recent = recent[len(recent)-shown:]
		}
		var b strings.Builder
		b.WriteString(stLabel.Render(fmt.Sprintf("  %-56s %s", "Address", "Found At")))
		b.WriteString("\n")
		for _, m := range recent {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				stValue.Render(m.Address+".onion"), stLabel.Render(m.FoundAt.Format("15:04:05"))))
		}
		if hidden := len(matches) - len(recent); hidden > 0 {
			b.WriteString(stLabel.Render(fmt.Sprintf("  … and %d more — see matches.txt / matches.csv", hidden)))
			b.WriteString("\n")
		}
		body = strings.TrimRight(b.String(), "\n")
	}
	title := fmt.Sprintf("🏆 Matches Found (%d)", len(matches))
	return panel(title, width, minBodyLines, body, colGood)
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
	pats := s.patterns()
	frac := 0.0
	var hdrLabel string

	switch {
	case s.phase == phaseSetup:
		// Pre-start: an honest ETA once there's a word to evaluate, the
		// actual rule broken when the mode is impossible for it (matching
		// -check's report), or a plain nudge when nothing's typed yet —
		// never a placeholder word standing in for a real search.
		hdrLabel = "Type a word above and press enter to begin."
		if len(pats) > 0 {
			est := vanity.NewEstimate(pats, s.mode, s.effectiveRate())
			switch {
			case est.Probability > 0:
				hdrLabel = fmt.Sprintf("Ready to search %s for %q — typically %s. Press enter to begin.",
					s.mode, strings.Join(pats, ", "), humanDur(est.P50))
			default:
				reason := "no address can ever match this combination"
				if err := vanity.Validate(pats[0], s.mode); err != nil {
					reason = err.Error()
				}
				hdrLabel = reason
			}
		}
	default:
		hdrLabel = fmt.Sprintf("Searching for %s with %q", s.mode, strings.Join(pats, ", "))
		if s.snap.Estimate.Probability > 0 {
			frac = 1 - expNeg(s.snap.Estimate.Probability*s.snap.KeysTried)
		}
	}
	// Truncate the plain text before styling it — truncating an
	// already-ANSI-styled string risks cutting mid-escape-sequence.
	hdrStyled := stLabel.Render(truncateTo(hdrLabel, width-4))

	barWidth := width - 12
	if barWidth < 10 {
		barWidth = 10
	}
	filled := int(frac * float64(barWidth))
	bar := lipgloss.NewStyle().Foreground(colGood).Render(strings.Repeat("█", filled)) +
		stBarEmpty.Render(strings.Repeat("░", barWidth-filled))

	body := hdrStyled + "\n" + bar + fmt.Sprintf("  %3.0f%%", frac*100)
	if s.phase != phaseSetup {
		body += "\n" + stLabel.Render("Keys Tested: ") + stValue.Render(humanCount(s.snap.KeysTried))
	}
	return panel("⚙ Progress", width, 0, body, colProgress)
}

func (s *Setup) renderLogs(width, minBodyLines int) string {
	shown := minBodyLines
	if shown < 8 {
		shown = 8
	}
	var visible []logEntry
	for _, e := range s.logLines {
		if e.visible(s.logLevel) {
			visible = append(visible, e)
		}
	}

	var body string
	switch {
	case len(s.logLines) == 0:
		body = stLabel.Render("Nothing logged yet — start a search to see live events here.")
	case len(visible) == 0:
		body = stLabel.Render(fmt.Sprintf("Nothing at the %q level yet — press v to widen the filter.", s.logLevel))
	default:
		if len(visible) > shown {
			visible = visible[len(visible)-shown:]
		}
		var lines []string
		for _, e := range visible {
			lines = append(lines, styleLogLine(e))
		}
		body = strings.Join(lines, "\n")
	}
	title := fmt.Sprintf("📜 Logs · level %s", s.logLevel)
	return panel(title, width, minBodyLines, body, colLogs)
}

func styleLogLine(e logEntry) string {
	line := e.text
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
	switch e.tag {
	case "MATCH":
		style = stGood
	case "WARN":
		style = stWarn
	case "STATS":
		style = stFocused
	}
	return stLabel.Render(line[:tagStart]) + style.Render(tag) + stLabel.Render(line[tagEnd+1:])
}

func row2(label, value string, labelWidth int) string {
	return fmt.Sprintf("  %s %s\n", stLabel.Render(pad(label, labelWidth)), value)
}

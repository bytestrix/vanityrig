// Package tui renders a live view of a running search.
//
// Design intent: one screen, no navigation, nothing to learn. The questions a
// user actually has while a search runs are "is it working", "how far along am
// I", and "did it find anything" — so those are the only things on screen, in
// that order, and a found key is impossible to miss.
package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bytestrix/vanityrig/internal/runner"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// refreshRate is how often the screen redraws. Fast enough to feel live, slow
// enough that the numbers are readable rather than flickering.
const refreshRate = 500 * time.Millisecond

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5a3fc0", Dark: "#a78bfa"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6b6b7b", Dark: "#8b849e"}
	colGood   = lipgloss.AdaptiveColor{Light: "#136c3a", Dark: "#4ade80"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#8a5a00", Dark: "#fbbf24"}
	colErr    = lipgloss.AdaptiveColor{Light: "#a01b1b", Dark: "#f87171"}

	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stLabel    = lipgloss.NewStyle().Foreground(colDim)
	stValue    = lipgloss.NewStyle().Bold(true)
	stGood     = lipgloss.NewStyle().Bold(true).Foreground(colGood)
	stWarn     = lipgloss.NewStyle().Foreground(colWarn)
	stErr      = lipgloss.NewStyle().Foreground(colErr)
	stHint     = lipgloss.NewStyle().Foreground(colDim)
	stMatchBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colGood).
			Padding(0, 1)
)

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(refreshRate, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Model is the dashboard state.
type Model struct {
	run      *runner.Runner
	stop     func()
	snap     runner.Snapshot
	width    int
	height   int
	quitting bool
	// finished records that the search ended on its own, so the view can say so
	// instead of continuing to imply work is happening.
	finished bool
}

// New builds a dashboard for a runner. stop is called when the user quits.
func New(r *runner.Runner, stop func()) Model {
	return Model{run: r, stop: stop, snap: r.Snapshot(), width: 80}
}

func (m Model) Init() tea.Cmd { return tick() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		// Every common "get me out of here" key works, because a user who wants
		// to stop should never have to guess which one this program chose.
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			if m.stop != nil {
				m.stop()
			}
			return m, tea.Quit
		}
		return m, nil

	case tickMsg:
		m.snap = m.run.Snapshot()
		select {
		case <-m.run.Done():
			m.finished = true
			return m, tea.Quit
		default:
		}
		return m, tick()
	}
	return m, nil
}

func (m Model) View() string {
	s := m.snap
	var b strings.Builder

	// Render to the terminal's real width rather than forcing a minimum: padding
	// a 40-column window out to 60 does not make it wider, it just wraps every
	// line and destroys the column alignment.
	width := m.width
	if width <= 0 {
		width = 80
	}
	if width > 100 {
		width = 100
	}

	// Explanatory asides are dropped on a narrow terminal rather than being left
	// to wrap: a wrapped line loses the alignment that makes the screen scannable,
	// which costs more than the aside is worth.
	roomy := width >= 80
	labelWidth := 13
	if width < 60 {
		labelWidth = 9
	}

	title := stTitle.Render("VanityRig")
	if roomy {
		title += stLabel.Render("  searching for a .onion address")
	}
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(stLabel.Render(strings.Repeat("─", width)))
	b.WriteString("\n\n")

	// What is being searched for.
	lookingFor := stValue.Render(strings.Join(s.Patterns, ", "))
	if roomy {
		lookingFor += stLabel.Render(fmt.Sprintf("  (%s of the address)", placement(s.Mode)))
	} else {
		lookingFor += stLabel.Render("  " + placement(s.Mode))
	}
	b.WriteString(row("looking for", lookingFor, labelWidth))

	engine := s.EngineName
	if roomy {
		engine += "  " + stLabel.Render("· "+s.EngineNote)
	}
	b.WriteString(row("engine", engine, labelWidth))

	// Whether it is working, and how fast.
	b.WriteString("\n")
	status := stGood.Render("● running")
	switch {
	case m.finished || s.Stopped:
		status = stWarn.Render("■ stopped")
	case s.KeysPerSec <= 0:
		status = stWarn.Render("● starting…")
	}
	b.WriteString(row("status", status, labelWidth))
	b.WriteString(row("speed", rate(s.KeysPerSec), labelWidth))
	keys := humanCount(s.KeysTried)
	if roomy {
		keys += stLabel.Render(fmt.Sprintf("  over %s", humanDur(s.Elapsed)))
	}
	b.WriteString(row("keys tried", keys, labelWidth))

	// How far along, expressed as the chance of having found it by now rather
	// than a progress bar, because this is a random search with no fixed end.
	if s.Estimate.Probability > 0 {
		chance := 1 - expNeg(s.Estimate.Probability*s.KeysTried)
		odds := fmt.Sprintf("%.1f%%", chance*100)
		if roomy {
			odds += stLabel.Render("  chance of a hit by now")
		}
		b.WriteString(row("odds so far", odds, labelWidth))

		// Until a speed sample arrives there is nothing to divide by, and the
		// estimate degenerates to "forever". Saying so on the opening frame would
		// be alarming and false, so wait for a real measurement.
		if s.Estimate.KeysPerSec > 0 {
			wait := humanDur(s.Estimate.P50)
			if roomy {
				wait += stLabel.Render("  (50/50 point)")
			}
			b.WriteString(row("typical wait", wait, labelWidth))
		} else {
			b.WriteString(row("typical wait", stLabel.Render("measuring…"), labelWidth))
		}
		b.WriteString(progressBar(chance, width-labelWidth-5, labelWidth))
	}

	// Findings — the reason anyone is watching this screen.
	b.WriteString("\n")
	if len(s.Matches) == 0 {
		b.WriteString(stLabel.Render("  no matches yet\n"))
	} else {
		var lines []string
		lines = append(lines, stGood.Render(fmt.Sprintf("FOUND %d", len(s.Matches))))
		for _, mt := range s.Matches {
			lines = append(lines, stValue.Render(mt.Address+".onion"))
			if roomy {
				lines = append(lines, stLabel.Render("saved to "+mt.Dir))
			}
		}
		if roomy {
			lines = append(lines, stLabel.Render("keep hs_ed25519_secret_key private — it is the address"))
		} else {
			lines = append(lines, stLabel.Render("keep hs_ed25519_secret_key private"))
		}
		b.WriteString(stMatchBox.Render(strings.Join(lines, "\n")))
		b.WriteString("\n")
	}

	if len(s.Errors) > 0 {
		b.WriteString("\n")
		for _, e := range s.Errors {
			b.WriteString(stErr.Render("  ! "+e) + "\n")
		}
	}

	b.WriteString("\n")
	switch {
	case m.finished || s.Stopped:
		b.WriteString(stHint.Render("  search finished — press q to close"))
	case roomy:
		b.WriteString(stHint.Render("  press q to stop  ·  progress is saved, you can resume later"))
	default:
		b.WriteString(stHint.Render("  press q to stop"))
	}
	b.WriteString("\n")

	return b.String()
}

// row renders one label/value line. labelWidth is passed in rather than fixed so
// a narrow terminal can use a tighter column instead of wrapping.
func row(label, value string, labelWidth int) string {
	return fmt.Sprintf("  %s %s\n", stLabel.Render(pad(label, labelWidth)), value)
}

func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func placement(m vanity.MatchMode) string {
	switch m {
	case vanity.MatchSuffix:
		return "at the end"
	case vanity.MatchAnywhere:
		return "anywhere in"
	default:
		return "at the start"
	}
}

// progressBar shows the cumulative chance of success. It is explicitly not a
// completion bar: the search can succeed at any moment or run well past the
// bar filling, so it is labelled as odds rather than progress.
func progressBar(frac float64, width, labelWidth int) string {
	if width < 10 {
		width = 10
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * float64(width))
	bar := lipgloss.NewStyle().Foreground(colAccent).Render(strings.Repeat("█", filled)) +
		stLabel.Render(strings.Repeat("░", width-filled))
	return "  " + stLabel.Render(pad("", labelWidth)) + bar + "\n"
}

func rate(r float64) string {
	if r <= 0 {
		return stLabel.Render("—")
	}
	return humanCount(r) + stLabel.Render("/sec")
}

func humanCount(f float64) string {
	switch {
	case f <= 0:
		return "0"
	case f < 1000:
		return fmt.Sprintf("%.0f", f)
	case f < 1e6:
		return fmt.Sprintf("%.1fK", f/1e3)
	case f < 1e9:
		return fmt.Sprintf("%.2fM", f/1e6)
	case f < 1e12:
		return fmt.Sprintf("%.2fB", f/1e9)
	case f < 1e15:
		return fmt.Sprintf("%.2fT", f/1e12)
	default:
		return fmt.Sprintf("%.2e", f)
	}
}

func humanDur(d time.Duration) string {
	if vanity.Saturated(d) {
		return "longer than a lifetime"
	}
	switch {
	case d < time.Second:
		return "moments"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	default:
		return fmt.Sprintf("%.1f years", d.Hours()/24/365.25)
	}
}

// expNeg is exp(-x), clamped so an enormous exponent cannot produce a NaN or a
// nonsense percentage on a very long-running search.
func expNeg(x float64) float64 {
	switch {
	case math.IsNaN(x) || x < 0:
		return 1
	case x > 700: // exp(-700) is already below float64 resolution
		return 0
	default:
		return math.Exp(-x)
	}
}

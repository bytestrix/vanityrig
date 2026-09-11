// Package runner owns a search session: it drives an engine, keeps a durable
// count of work done, files away found keys, and exposes a snapshot for a UI to
// render.
//
// The durable count is the point. mkp224o only reports an instantaneous rate and
// forgets everything on restart, so answering "how much work has this search
// actually done" previously meant integrating rates out of a log by hand. That
// number lives here instead, and survives restarts.
package runner

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bytestrix/vanityrig/internal/engine"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// Match is a found address as recorded by the runner.
type Match struct {
	Address   string    `json:"address"`
	Dir       string    `json:"dir"`
	FoundAt   time.Time `json:"found_at"`
	KeysTried float64   `json:"keys_tried"`
}

// state is the part of a session that must outlive the process.
type state struct {
	Patterns    []string         `json:"patterns"`
	Mode        vanity.MatchMode `json:"mode"`
	KeysTried   float64          `json:"keys_tried"`
	ElapsedSecs float64          `json:"elapsed_secs"`
	Matches     []Match          `json:"matches"`
	StartedAt   time.Time        `json:"started_at"`
}

// Snapshot is a consistent read of a running search, safe to render.
type Snapshot struct {
	Patterns   []string
	Mode       vanity.MatchMode
	EngineName string
	EngineNote string
	Threads    int
	OutputDir  string

	KeysPerSec  float64 // most recent sample
	KeysTried   float64 // cumulative, including previous runs
	Elapsed     time.Duration
	SessionTime time.Duration // this run only

	Matches []Match
	Errors  []string

	Estimate vanity.Estimate
	Running  bool
	Stopped  bool
	ExitErr  error
}

// Config describes a search session.
type Config struct {
	Patterns   []string
	Mode       vanity.MatchMode
	Threads    int
	OutputDir  string
	StateDir   string // where the durable counter lives; defaults under OutputDir
	EnginePath string // optional explicit mkp224o path
	StopAfter  int    // stop once this many matches are found; 0 means never
}

// Runner drives one search.
type Runner struct {
	cfg       Config
	eng       engine.Engine
	note      string
	statePath string

	mu          sync.RWMutex
	st          state
	lastRate    float64
	sessionFrom time.Time
	errs        []string
	running     bool
	stopped     bool
	exitErr     error

	// stopOnce guards the caller-facing stop signal so a search that reaches its
	// match target and one cancelled by the user cannot both close it.
	stopOnce sync.Once
	done     chan struct{}
}

// New prepares a search. It does not start anything until Run is called.
func New(cfg Config) (*Runner, error) {
	if len(cfg.Patterns) == 0 {
		return nil, fmt.Errorf("no patterns given")
	}
	for _, p := range cfg.Patterns {
		if err := vanity.Validate(p, cfg.Mode); err != nil {
			return nil, err
		}
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("no output directory given")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o700); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	if cfg.StateDir == "" {
		cfg.StateDir = cfg.OutputDir
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	eng, note := engine.Select(cfg.Mode, cfg.EnginePath)

	r := &Runner{
		cfg:       cfg,
		eng:       eng,
		note:      note,
		statePath: filepath.Join(cfg.StateDir, "session.json"),
		done:      make(chan struct{}),
	}
	r.st = state{
		Patterns:  cfg.Patterns,
		Mode:      cfg.Mode,
		StartedAt: time.Now(),
	}
	r.loadState()
	return r, nil
}

// loadState restores a previous session's totals when the patterns and mode
// match, so stopping and restarting a search continues the count instead of
// silently resetting it to zero.
func (r *Runner) loadState() {
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		return
	}
	var prev state
	if err := json.Unmarshal(data, &prev); err != nil {
		return
	}
	if !samePatterns(prev.Patterns, r.cfg.Patterns) || prev.Mode != r.cfg.Mode {
		// A different search: previous work does not count toward this one.
		return
	}
	r.st.KeysTried = prev.KeysTried
	r.st.ElapsedSecs = prev.ElapsedSecs
	r.st.Matches = prev.Matches
	if !prev.StartedAt.IsZero() {
		r.st.StartedAt = prev.StartedAt
	}
}

func samePatterns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[vanity.Normalize(s)]++
	}
	for _, s := range b {
		seen[vanity.Normalize(s)]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func (r *Runner) saveState() {
	r.mu.RLock()
	data, err := json.MarshalIndent(r.st, "", "  ")
	r.mu.RUnlock()
	if err != nil {
		return
	}
	// Write via a temp file so a crash mid-write cannot leave a truncated state
	// file that would silently reset the user's progress on next start.
	tmp := r.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, r.statePath)
}

// appendMatchSummary appends one line to matches.txt and one row to
// matches.csv in the output directory. Best-effort: a failure to write the
// summary must never interrupt a search that already succeeded at the
// expensive part (finding the key), so errors here are silently dropped
// rather than surfaced as a search failure.
func (r *Runner) appendMatchSummary(m Match) {
	txtPath := filepath.Join(r.cfg.OutputDir, "matches.txt")
	if f, err := os.OpenFile(txtPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%s\t%s.onion\t%s\n", m.FoundAt.Format(time.RFC3339), m.Address, m.Dir)
		f.Close()
	}

	csvPath := filepath.Join(r.cfg.OutputDir, "matches.csv")
	isNew := true
	if _, err := os.Stat(csvPath); err == nil {
		isNew = false
	}
	if f, err := os.OpenFile(csvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		w := csv.NewWriter(f)
		if isNew {
			_ = w.Write([]string{"found_at", "address", "dir", "keys_tried"})
		}
		_ = w.Write([]string{
			m.FoundAt.Format(time.RFC3339),
			m.Address + ".onion",
			m.Dir,
			fmt.Sprintf("%.0f", m.KeysTried),
		})
		w.Flush()
		f.Close()
	}
}

// EngineName reports which backend will run, for display before starting.
func (r *Runner) EngineName() string { return r.eng.Name() }

// EngineNote explains the engine choice in one line.
func (r *Runner) EngineNote() string { return r.note }

// Done is closed when the search has finished for any reason.
func (r *Runner) Done() <-chan struct{} { return r.done }

// Run drives the search until ctx is cancelled or the stop condition is met.
//
// It owns a derived context for the engine rather than using ctx directly, so
// that reaching StopAfter can stop the engine the same way an outside
// cancellation does: this function never returns until the engine's events
// channel has actually closed, which only happens after its goroutines have
// fully exited. Returning early while the engine could still be mid-write is
// exactly the kind of race that shows up as a caller (a test's t.TempDir
// cleanup, or a second Run of the same output directory) finding a key
// directory in a half-written state.
func (r *Runner) Run(ctx context.Context) error {
	engineCtx, stopEngine := context.WithCancel(ctx)
	defer stopEngine()

	events, err := r.eng.Run(engineCtx, engine.Config{
		Patterns:   r.cfg.Patterns,
		Mode:       r.cfg.Mode,
		Threads:    r.cfg.Threads,
		OutputDir:  r.cfg.OutputDir,
		BinaryPath: r.cfg.EnginePath,
	})
	if err != nil {
		return err
	}

	// drain waits for the engine to fully stop after we've decided to,
	// discarding anything further it reports — StopAfter having been reached
	// means we no longer want more matches, but we must not return while the
	// engine could still be writing one.
	drain := func() {
		stopEngine()
		for range events {
		}
	}

	r.mu.Lock()
	r.running = true
	r.sessionFrom = time.Now()
	r.mu.Unlock()

	saveTick := time.NewTicker(5 * time.Second)
	defer saveTick.Stop()

	defer func() {
		r.mu.Lock()
		r.running = false
		r.stopped = true
		r.mu.Unlock()
		r.saveState()
		r.stopOnce.Do(func() { close(r.done) })
	}()

	for {
		select {
		case <-saveTick.C:
			r.saveState()

		case ev, ok := <-events:
			if !ok {
				return nil
			}
			switch ev.Kind {
			case engine.EventSample:
				r.mu.Lock()
				r.lastRate = ev.KeysPerSec
				// Integrate the sample into the running total using the interval
				// the engine reported, rather than assuming a fixed period.
				r.st.KeysTried += ev.KeysPerSec * ev.Interval.Seconds()
				r.st.ElapsedSecs += ev.Interval.Seconds()
				r.mu.Unlock()

			case engine.EventMatch:
				r.mu.Lock()
				m := Match{
					Address:   ev.Match.Address,
					Dir:       ev.Match.Dir,
					FoundAt:   ev.Match.FoundAt,
					KeysTried: r.st.KeysTried,
				}
				dup := false
				for _, existing := range r.st.Matches {
					if existing.Address == m.Address {
						dup = true
						break
					}
				}
				if !dup {
					r.st.Matches = append(r.st.Matches, m)
					// A plain-text/CSV summary alongside the real key files —
					// never a replacement for them (those must stay in Tor's
					// exact binary layout to be usable as a hidden service),
					// just a quick human/spreadsheet-readable record of what
					// was found and when.
					r.appendMatchSummary(m)
				}
				count := len(r.st.Matches)
				r.mu.Unlock()
				// Persist immediately: a found key is expensive to earn and must
				// not depend on the next periodic save.
				r.saveState()

				if r.cfg.StopAfter > 0 && count >= r.cfg.StopAfter {
					drain()
					return nil
				}

			case engine.EventError:
				r.mu.Lock()
				r.errs = append(r.errs, ev.Err.Error())
				r.mu.Unlock()

			case engine.EventExit:
				r.mu.Lock()
				r.exitErr = ev.Err
				r.mu.Unlock()
				return ev.Err
			}
		}
	}
}

// Snapshot returns a consistent view for rendering.
func (r *Runner) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var session time.Duration
	if !r.sessionFrom.IsZero() {
		session = time.Since(r.sessionFrom)
	}

	rate := r.lastRate
	if rate <= 0 {
		// Before the first sample arrives, fall back to the session average so the
		// estimate has something honest to work from rather than showing "never".
		if session > time.Second && r.st.KeysTried > 0 {
			rate = r.st.KeysTried / session.Seconds()
		}
	}

	matches := make([]Match, len(r.st.Matches))
	copy(matches, r.st.Matches)
	errs := make([]string, len(r.errs))
	copy(errs, r.errs)

	return Snapshot{
		Patterns:    r.cfg.Patterns,
		Mode:        r.cfg.Mode,
		EngineName:  r.eng.Name(),
		EngineNote:  r.note,
		Threads:     r.cfg.Threads,
		OutputDir:   r.cfg.OutputDir,
		KeysPerSec:  r.lastRate,
		KeysTried:   r.st.KeysTried,
		Elapsed:     time.Duration(r.st.ElapsedSecs * float64(time.Second)),
		SessionTime: session,
		Matches:     matches,
		Errors:      errs,
		Estimate:    vanity.NewEstimate(r.cfg.Patterns, r.cfg.Mode, rate),
		Running:     r.running,
		Stopped:     r.stopped,
		ExitErr:     r.exitErr,
	}
}

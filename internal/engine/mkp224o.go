package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// Mkp224o drives the external mkp224o binary.
//
// It is roughly 180x faster per core than the native engine, so it is the right
// choice whenever it can be used — but it only matches prefixes, so suffix and
// anywhere searches have to fall back to Native.
type Mkp224o struct {
	// Path overrides binary lookup; empty means search PATH and the usual spots.
	Path string
}

func (Mkp224o) Name() string { return "mkp224o" }

// Modes reports which match modes this engine can run. mkp224o has no flag for
// match position — every filter is a prefix.
func (Mkp224o) Modes() []vanity.MatchMode {
	return []vanity.MatchMode{vanity.MatchPrefix}
}

// searchPaths are the usual install locations, checked after PATH so a binary the
// user put on their PATH always wins.
var searchPaths = []string{
	"~/.local/bin/mkp224o",
	"/usr/local/bin/mkp224o",
	"/usr/bin/mkp224o",
}

// Locate finds the mkp224o binary, returning the path it will actually run.
func (m Mkp224o) Locate() (string, error) {
	if m.Path != "" {
		if _, err := os.Stat(m.Path); err != nil {
			return "", fmt.Errorf("mkp224o not found at %s", m.Path)
		}
		return m.Path, nil
	}
	if p, err := exec.LookPath("mkp224o"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range searchPaths {
		full := strings.Replace(p, "~", home, 1)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("mkp224o not found on PATH or in %s", strings.Join(searchPaths, ", "))
}

func (m Mkp224o) Available() (bool, string) {
	if _, err := m.Locate(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// statLine parses mkp224o's periodic progress output, whose format is:
//
//	>calc/sec:18832402.516944, succ/sec:0.000000, rest/sec:0.000000, elapsed:90.171354sec
var statLine = regexp.MustCompile(`calc/sec:([0-9.]+).*?elapsed:([0-9.]+)sec`)

func (m Mkp224o) Run(ctx context.Context, cfg Config) (<-chan Event, error) {
	if cfg.Mode != vanity.MatchPrefix {
		return nil, fmt.Errorf("mkp224o only matches prefixes; %s mode needs the native engine", cfg.Mode)
	}
	if len(cfg.Patterns) == 0 {
		return nil, fmt.Errorf("no patterns to search for")
	}
	for _, p := range cfg.Patterns {
		if err := vanity.Validate(p, cfg.Mode); err != nil {
			return nil, err
		}
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("no output directory configured")
	}

	bin, err := m.Locate()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o700); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	threads := cfg.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	args := append([]string{}, cfg.Patterns...)
	args = append(args,
		"-d", cfg.OutputDir,
		"-t", strconv.Itoa(threads),
		"-S", "1", // report throughput every second so the UI stays live
	)

	cmd := exec.CommandContext(ctx, bin, args...)
	// mkp224o writes its progress lines to stderr and found names to stdout.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mkp224o: %w", err)
	}

	events := make(chan Event, 64)

	go func() {
		defer close(events)

		done := make(chan struct{}, 2)

		// Progress lines. mkp224o resets its elapsed counter across restarts, so
		// the per-interval delta is computed here and passed on as an interval
		// rather than trusting a cumulative figure that can go backwards.
		go func() {
			defer func() { done <- struct{}{} }()
			sc := bufio.NewScanner(stderr)
			var prevElapsed float64
			for sc.Scan() {
				mm := statLine.FindStringSubmatch(sc.Text())
				if mm == nil {
					continue
				}
				rate, err1 := strconv.ParseFloat(mm[1], 64)
				elapsed, err2 := strconv.ParseFloat(mm[2], 64)
				if err1 != nil || err2 != nil {
					continue
				}
				interval := elapsed - prevElapsed
				if interval <= 0 || interval > 3600 {
					// A restart or a bogus jump: fall back to the nominal period
					// rather than inventing a huge or negative one.
					interval = 1
				}
				prevElapsed = elapsed
				events <- Event{
					Kind:       EventSample,
					At:         time.Now(),
					KeysPerSec: rate,
					Interval:   time.Duration(interval * float64(time.Second)),
				}
			}
		}()

		// Found addresses. mkp224o prints the name and writes the key files itself.
		go func() {
			defer func() { done <- struct{}{} }()
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				name := strings.TrimSpace(sc.Text())
				if !strings.HasSuffix(name, ".onion") {
					continue
				}
				addr := strings.TrimSuffix(name, ".onion")
				events <- Event{
					Kind: EventMatch,
					At:   time.Now(),
					Match: Match{
						Address: addr,
						Dir:     filepath.Join(cfg.OutputDir, name),
						FoundAt: time.Now(),
					},
				}
			}
		}()

		<-done
		<-done
		err := cmd.Wait()
		if ctx.Err() != nil {
			// Cancellation is the normal way a search ends; not an error.
			err = nil
		}
		events <- Event{Kind: EventExit, At: time.Now(), Err: err}
	}()

	return events, nil
}

// Select returns the best engine available for the requested mode, and a short
// note explaining the choice for the user.
//
// The note matters: a user asking for anywhere mode silently dropping from
// mkp224o's throughput to the native engine's would see a wildly different
// timescale with no explanation of why.
func Select(mode vanity.MatchMode, mkPath string) (Engine, string) {
	mk := Mkp224o{Path: mkPath}
	if mode == vanity.MatchPrefix {
		if ok, _ := mk.Available(); ok {
			return mk, "using mkp224o (fast prefix engine)"
		}
		return Native{}, "mkp224o not installed, using the slower built-in engine"
	}
	return Native{}, fmt.Sprintf("using the built-in engine: mkp224o cannot do %s matching", mode)
}

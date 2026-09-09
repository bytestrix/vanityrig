// Package engine runs vanity address searches and reports what they find.
//
// An Engine wraps some underlying generator (mkp224o today, others later) and
// normalises it to one event stream, so the rest of VanityRig never has to know
// which backend produced a number or in what format.
package engine

import (
	"context"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// EventKind distinguishes what an Event carries.
type EventKind int

const (
	// EventSample is a throughput reading.
	EventSample EventKind = iota
	// EventMatch is a found address, already written to disk by the engine.
	EventMatch
	// EventError is a non-fatal problem worth surfacing.
	EventError
	// EventExit means the engine stopped; Err says whether that was clean.
	EventExit
)

// Event is one thing the engine has to report.
type Event struct {
	Kind EventKind
	At   time.Time

	// EventSample
	KeysPerSec float64
	// Interval is the time this sample covers, so callers can integrate it into
	// a running total without guessing the reporting period.
	Interval time.Duration

	// EventMatch
	Match Match

	// EventError, EventExit
	Err error
}

// Match is a found address and the key material on disk that goes with it.
type Match struct {
	Address string // the .onion hostname, without the ".onion" suffix stripped
	Dir     string // directory holding hostname + the two key files
	FoundAt time.Time
}

// Config describes a search to run.
type Config struct {
	Patterns   []string
	Mode       vanity.MatchMode
	Threads    int    // 0 means "all cores"
	OutputDir  string // where the engine writes found keys
	BinaryPath string // path to the backend binary; empty means look it up on PATH
}

// Engine is a vanity address search backend.
//
// Run blocks until ctx is cancelled or the search dies, sending everything it
// observes to the returned channel. The channel is closed when Run returns, so a
// caller can range over it safely.
type Engine interface {
	// Name identifies the backend for display, e.g. "mkp224o".
	Name() string
	// Available reports whether this engine can actually run here, with a reason
	// when it cannot, so the caller can explain the problem instead of failing
	// with a bare exec error.
	Available() (bool, string)
	// Run starts the search.
	Run(ctx context.Context, cfg Config) (<-chan Event, error)
}

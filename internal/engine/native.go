package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// sampleInterval is how often throughput is reported. Short enough that the UI
// feels live, long enough that the counters are not the bottleneck.
const sampleInterval = time.Second

// Native is a pure-Go search engine.
//
// It is slower per core than mkp224o, which is heavily optimised, but it has two
// properties that make it the right default: it needs no external binary, and it
// supports all three match modes. mkp224o can only match prefixes, so suffix and
// anywhere searches have nowhere else to run.
type Native struct{}

func (Native) Name() string { return "native" }

func (Native) Available() (bool, string) { return true, "" }

// Modes reports which match modes this engine can run.
func (Native) Modes() []vanity.MatchMode {
	return []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere}
}

func (n Native) Run(ctx context.Context, cfg Config) (<-chan Event, error) {
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

	threads := cfg.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	matcher, err := newMatcher(cfg.Patterns, cfg.Mode)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 64)
	var tried atomic.Uint64

	go func() {
		defer close(events)

		workerCtx, stop := context.WithCancel(ctx)
		defer stop()

		found := make(chan foundKey, threads)
		for i := 0; i < threads; i++ {
			go searchWorker(workerCtx, matcher, &tried, found)
		}

		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()

		var last uint64
		lastAt := time.Now()

		for {
			select {
			case <-ctx.Done():
				events <- Event{Kind: EventExit, At: time.Now()}
				return

			case <-ticker.C:
				now := time.Now()
				cur := tried.Load()
				interval := now.Sub(lastAt)
				// Report the count for this interval rather than a rate alone, so
				// the consumer can keep an exact running total instead of
				// re-deriving one from a rate and a guessed period.
				events <- Event{
					Kind:       EventSample,
					At:         now,
					KeysPerSec: float64(cur-last) / interval.Seconds(),
					Interval:   interval,
				}
				last, lastAt = cur, now

			case f := <-found:
				addr := AddressFor(f.pub)
				dir := filepath.Join(cfg.OutputDir, addr+".onion")
				// Persist before announcing: a crash between finding and saving
				// would otherwise lose a key that took real time to earn.
				if err := WriteKeyFiles(dir, f.pub, f.priv); err != nil {
					events <- Event{Kind: EventError, At: time.Now(),
						Err: fmt.Errorf("saving %s: %w", addr, err)}
					continue
				}
				events <- Event{
					Kind: EventMatch,
					At:   time.Now(),
					Match: Match{
						Address: addr,
						Dir:     dir,
						FoundAt: time.Now(),
					},
				}
			}
		}
	}()

	return events, nil
}

type foundKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// searchWorker generates candidate keys until its context is cancelled.
//
// The counter is incremented in batches rather than per key: an atomic add on
// every attempt turns into the hottest instruction in the loop and measurably
// slows the search down.
func searchWorker(ctx context.Context, m *matcher, tried *atomic.Uint64, found chan<- foundKey) {
	const batch = 256
	n := 0
	for {
		if n >= batch {
			tried.Add(uint64(n))
			n = 0
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			// A failing entropy source is not something to spin on.
			tried.Add(uint64(n))
			return
		}
		n++
		if m.matches(AddressFor(pub)) {
			select {
			case found <- foundKey{pub: pub, priv: priv}:
			case <-ctx.Done():
				return
			}
		}
	}
}

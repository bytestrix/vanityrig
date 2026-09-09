package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"math/big"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"filippo.io/edwards25519"
	"filippo.io/edwards25519/field"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// Symmetry is a faster pure-Go engine than Native, for the same three match
// modes.
//
// Native derives a fully independent Ed25519 keypair for every candidate: a
// scalar multiplication plus one modular field inversion (needed to convert
// the computed curve point into its standard 32-byte encoding), each done
// from scratch. The inversion alone is roughly as expensive as the entire
// rest of the computation. Symmetry instead starts each worker from one
// randomly chosen point and steps it forward by a fixed multiple of the
// curve's base point — a single cheap point addition per step — and
// batches the one genuinely expensive operation, the inversion, across many
// steps at once via Montgomery's trick, so its cost is amortised across the
// whole batch instead of paid once per candidate.
//
// Security note: stepping by a fixed public increment does not make any
// candidate weaker, nor does it relate candidates in any way an outside
// observer could exploit. Only the scalar that actually matches a search
// pattern is ever recovered, independently re-verified, and written to
// disk; every other point produced during the walk is discarded unread and
// never leaves this process. From outside, the one key that gets kept is
// exactly as unpredictable as a freshly and independently generated
// key — recovering it requires knowing the walk's own randomly drawn
// starting scalar. See symmetry_test.go for the proofs this rests on: a
// stepped point must equal direct computation from its own scalar, the
// batch-inversion encoding must equal per-point encoding, the encoding
// must match crypto/ed25519's own output for the same key, and a recovered
// scalar must produce signatures that verify under crypto/ed25519 — the
// same algorithm Tor itself uses to sign with a loaded on-disk key.
type Symmetry struct{}

func (Symmetry) Name() string { return "symmetry" }

func (Symmetry) Available() (bool, string) { return true, "" }

// Modes reports which match modes this engine can run.
func (Symmetry) Modes() []vanity.MatchMode {
	return []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere}
}

// symmetryBatch is how many points are stepped and normalised together
// before checking for a match. Larger amortises the one modular inversion
// per batch further; this is small enough to keep memory trivial and the
// gap between throughput samples short.
const symmetryBatch = 512

// eightScalar is the scalar 8. Stepping the point by eightScalar*B each time
// means the corresponding integer scalar increases by exactly 8 each step —
// which matters because a properly RFC 8032-clamped starting scalar is
// always a multiple of 8, and a multiple of 8 plus another multiple of 8 is
// still a multiple of 8. That property is Ed25519's "clamping", and
// preserving it as an exact integer fact (not just "some value congruent
// mod the group order", which is all this library's Scalar type tracks) is
// what lets a recovered candidate be written out in exactly the on-disk
// format real Tor keys use — see runningScalar below, which tracks the
// actual unreduced integer separately from the point arithmetic.
var eightScalar = mustSmallScalar(8)

func mustSmallScalar(n uint64) *edwards25519.Scalar {
	le := make([]byte, 32)
	for i := 0; i < 8; i++ {
		le[i] = byte(n >> (8 * i))
	}
	s, err := edwards25519.NewScalar().SetCanonicalBytes(le)
	if err != nil {
		panic("engine: invalid built-in scalar constant: " + err.Error())
	}
	return s
}

func (Symmetry) Run(ctx context.Context, cfg Config) (<-chan Event, error) {
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

		found := make(chan expandedMatch, threads)
		errs := make(chan error, threads)
		for i := 0; i < threads; i++ {
			go symmetryWorker(workerCtx, matcher, &tried, found, errs)
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

			case err := <-errs:
				// A self-check failure is a bug, not a fluke, and must never be
				// allowed to fall through to writing an unverified key. Stop the
				// whole search rather than silently dropping the candidate.
				events <- Event{Kind: EventError, At: time.Now(),
					Err: fmt.Errorf("internal consistency check failed: %w", err)}
				events <- Event{Kind: EventExit, At: time.Now(), Err: err}
				stop()
				return

			case <-ticker.C:
				now := time.Now()
				cur := tried.Load()
				interval := now.Sub(lastAt)
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
				if err := WriteKeyFilesExpanded(dir, f.pub, f.expanded); err != nil {
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

// expandedMatch is a verified match, already in the exact 64-byte on-disk
// secret key layout (clamped scalar || nonce prefix).
type expandedMatch struct {
	pub      ed25519.PublicKey
	expanded [64]byte
}

// symmetryWorker walks a random-start arithmetic progression of points,
// batching the expensive per-point modular inversion across symmetryBatch
// steps at a time, until its context is cancelled or an unrecoverable
// self-check failure is reported on errs.
func symmetryWorker(ctx context.Context, m *matcher, tried *atomic.Uint64, found chan<- expandedMatch, errs chan<- error) {
	// Computed once per worker, not once per step: it's a full scalar
	// multiplication, exactly the expensive operation this engine exists to
	// avoid paying per candidate.
	step := new(edwards25519.Point).ScalarBaseMult(eightScalar)

	points := make([]*edwards25519.Point, symmetryBatch)
	for i := range points {
		points[i] = edwards25519.NewIdentityPoint()
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		a0, point0, err := randomStart()
		if err != nil {
			return // entropy source failing is not something to spin on
		}

		cur := point0
		for i := 0; i < symmetryBatch; i++ {
			points[i].Set(cur)
			cur = new(edwards25519.Point).Add(cur, step)
		}

		pubs := batchEncode(points)
		tried.Add(uint64(symmetryBatch))

		for i, pub := range pubs {
			if !m.matches(AddressFor(pub)) {
				continue
			}

			ai := new(big.Int).Add(a0, new(big.Int).Mul(big.NewInt(int64(i)), big.NewInt(8)))
			aiBytes, err := bigIntToLE32(ai)
			if err != nil {
				select {
				case errs <- fmt.Errorf("recovered scalar out of range: %w", err):
				case <-ctx.Done():
				}
				return
			}

			// Never write a key without independently re-deriving its public
			// point from the recovered scalar and confirming it matches the
			// point the search actually found. This is the check the
			// technique's own reference implementation is missing.
			verify, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(aiBytes))
			if err != nil {
				select {
				case errs <- fmt.Errorf("reducing recovered scalar: %w", err):
				case <-ctx.Done():
				}
				return
			}
			recomputed := new(edwards25519.Point).ScalarBaseMult(verify)
			if recomputed.Equal(points[i]) != 1 {
				select {
				case errs <- fmt.Errorf("recovered scalar does not reproduce the matched point (candidate %d)", i):
				case <-ctx.Done():
				}
				return
			}

			var prefix [32]byte
			if _, err := rand.Read(prefix[:]); err != nil {
				select {
				case errs <- fmt.Errorf("reading random nonce prefix: %w", err):
				case <-ctx.Done():
				}
				return
			}
			var expanded [64]byte
			copy(expanded[:32], aiBytes)
			copy(expanded[32:], prefix[:])

			select {
			case found <- expandedMatch{pub: append(ed25519.PublicKey{}, pub...), expanded: expanded}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// randomStart draws a fresh RFC 8032-clamped scalar and returns it both as
// the exact unreduced integer (for later reconstructing on-disk key bytes)
// and as the point it corresponds to (for the walk).
func randomStart() (*big.Int, *edwards25519.Point, error) {
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, nil, err
	}
	clamp(seed[:])

	a0 := leToBigInt(seed[:])

	scalar, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(seed[:]))
	if err != nil {
		return nil, nil, err
	}
	point := new(edwards25519.Point).ScalarBaseMult(scalar)
	return a0, point, nil
}

// clamp applies RFC 8032, Section 5.1.5 buffer pruning in place: the same
// operation crypto/ed25519 applies to a SHA-512 seed hash, and the same one
// this project's own expandSecret applies when writing out a seed-derived
// key. Applying it directly to fresh random bytes (rather than to a
// SHA-512 output) is fine — clamping only needs 32 random-looking input
// bytes to clamp, and crypto/rand already provides that; the SHA-512 step
// in ordinary Ed25519 key generation exists to derive the seed's OTHER use
// (the signing nonce prefix), not to make the scalar itself valid.
func clamp(b []byte) {
	b[0] &= 248
	b[31] &= 127
	b[31] |= 64
}

// leToBigInt interprets b as a little-endian integer.
func leToBigInt(b []byte) *big.Int {
	be := make([]byte, len(b))
	for i, v := range b {
		be[len(b)-1-i] = v
	}
	return new(big.Int).SetBytes(be)
}

// bigIntToLE32 encodes n as exactly 32 little-endian bytes, erroring rather
// than silently truncating if it doesn't fit.
func bigIntToLE32(n *big.Int) ([]byte, error) {
	be := n.Bytes()
	if len(be) > 32 {
		return nil, fmt.Errorf("value is %d bytes, want at most 32", len(be))
	}
	le := make([]byte, 32)
	for i, v := range be {
		le[len(be)-1-i] = v
	}
	return le, nil
}

// padTo64 right-pads a 32-byte little-endian value with zero bytes, for
// SetUniformBytes, which requires exactly 64 bytes but treats the value as
// the same little-endian integer regardless of the extra high-order zeros.
func padTo64(b32 []byte) []byte {
	out := make([]byte, 64)
	copy(out, b32)
	return out
}

// batchEncode converts points to their standard 32-byte Ed25519 public key
// encodings, doing exactly one modular field inversion for the whole batch
// (Montgomery's trick) instead of one per point.
//
// Standard encoding is the little-endian affine y-coordinate with the
// affine x-coordinate's sign folded into the encoding's top bit — see
// TestBatchEncodeMatchesPerPointEncoding, which checks this against calling
// Point.Bytes() (the library's own, unbatched, trusted encoder) directly.
func batchEncode(points []*edwards25519.Point) []ed25519.PublicKey {
	n := len(points)
	zs := make([]*field.Element, n)
	for i, p := range points {
		_, _, z, _ := p.ExtendedCoordinates()
		zs[i] = z
	}

	// Montgomery's trick: one inversion instead of n.
	prefix := make([]*field.Element, n)
	prefix[0] = zs[0]
	for i := 1; i < n; i++ {
		prefix[i] = new(field.Element).Multiply(prefix[i-1], zs[i])
	}
	inv := new(field.Element).Invert(prefix[n-1])

	zInv := make([]*field.Element, n)
	for i := n - 1; i > 0; i-- {
		zInv[i] = new(field.Element).Multiply(inv, prefix[i-1])
		inv = new(field.Element).Multiply(inv, zs[i])
	}
	zInv[0] = inv

	out := make([]ed25519.PublicKey, n)
	for i, p := range points {
		x, y, _, _ := p.ExtendedCoordinates()
		yAff := new(field.Element).Multiply(y, zInv[i])
		xAff := new(field.Element).Multiply(x, zInv[i])

		enc := yAff.Bytes()
		if xAff.IsNegative() == 1 {
			enc[31] |= 0x80
		}
		out[i] = ed25519.PublicKey(enc)
	}
	return out
}

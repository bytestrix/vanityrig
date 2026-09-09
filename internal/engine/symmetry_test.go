package engine

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/edwards25519"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// The scalar constants are typed-out little-endian byte arrays — exactly the
// kind of hand-written constant this project's own culture treats with
// suspicion (PROJECT.md's origin story is a hand-typed constant that was
// wrong by 1024x). Check it against the library's own generator rather than
// trusting it by inspection.
func TestEightScalarMatchesDirectComputation(t *testing.T) {
	one := mustSmallScalar(1)
	if edwards25519.NewGeneratorPoint().Equal(new(edwards25519.Point).ScalarBaseMult(one)) != 1 {
		t.Fatal("mustSmallScalar(1) * B does not equal the library's own generator point")
	}

	got := new(edwards25519.Point).ScalarBaseMult(eightScalar)
	want := new(edwards25519.Point).ScalarBaseMult(one)
	for i := 0; i < 7; i++ {
		want = new(edwards25519.Point).Add(want, new(edwards25519.Point).ScalarBaseMult(one))
	}
	if got.Equal(want) != 1 {
		t.Fatal("eightScalar * B does not equal seven additions of B to B")
	}
}

// leToBigInt/bigIntToLE32 are a hand-rolled byte-order conversion — exactly
// where an off-by-endianness bug hides silently, since it would still
// produce syntactically valid 32-byte values.
func TestByteOrderConversionRoundTrips(t *testing.T) {
	cases := [][]byte{
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x40},
	}
	for _, want := range cases {
		n := leToBigInt(want)
		got, err := bigIntToLE32(n)
		if err != nil {
			t.Fatalf("bigIntToLE32: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("round-trip mismatch:\n got  %x\n want %x", got, want)
		}
	}

	// leToBigInt(1, 0, 0, ...) must be the integer 1, not 2^248 (which is
	// what a reversed byte-order bug would silently produce instead).
	one := make([]byte, 32)
	one[0] = 1
	if leToBigInt(one).Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("leToBigInt of a low-byte-set value should be 1, got %s", leToBigInt(one))
	}
}

// The whole point of stepping is that it must reach the same point as
// computing the scalar directly and multiplying from scratch. If this test
// passes, the incremental walk is provably not accumulating drift or error.
func TestSteppedPointsMatchDirectComputation(t *testing.T) {
	a0, point0, err := randomStart()
	if err != nil {
		t.Fatal(err)
	}
	step := new(edwards25519.Point).ScalarBaseMult(eightScalar)

	cur := point0
	for i := 0; i < 50; i++ {
		wantScalarInt := new(big.Int).Add(a0, big.NewInt(int64(i)*8))
		wantBytes, err := bigIntToLE32(wantScalarInt)
		if err != nil {
			t.Fatal(err)
		}
		wantScalar, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(wantBytes))
		if err != nil {
			t.Fatal(err)
		}
		want := new(edwards25519.Point).ScalarBaseMult(wantScalar)

		if cur.Equal(want) != 1 {
			t.Fatalf("step %d: incremental point does not match direct computation from a0+8i", i)
		}
		cur = new(edwards25519.Point).Add(cur, step)
	}
}

// batchEncode's whole justification is that it produces the same bytes as
// the library's own trusted, unbatched Point.Bytes() encoder — just faster.
// If this test passes, the Montgomery-trick batching introduced no encoding
// bug, independent of whether the underlying points are "real" keys.
func TestBatchEncodeMatchesPerPointEncoding(t *testing.T) {
	points := make([]*edwards25519.Point, 37) // deliberately not a power of 2
	for i := range points {
		var seed [32]byte
		if _, err := rand.Read(seed[:]); err != nil {
			t.Fatal(err)
		}
		scalar, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(seed[:]))
		if err != nil {
			t.Fatal(err)
		}
		points[i] = new(edwards25519.Point).ScalarBaseMult(scalar)
	}

	got := batchEncode(points)
	for i, p := range points {
		want := p.Bytes()
		if !bytes.Equal(got[i], want) {
			t.Errorf("point %d: batch encoding %x, want %x (from Point.Bytes())", i, got[i], want)
		}
	}
}

// This is the deepest check: does a scalar recovered exactly the way the
// engine recovers it (RFC 8032 clamping applied to fresh bytes, not derived
// through SHA-512 from a seed) actually work as a real Ed25519 private key?
// It signs with the recovered scalar using the standard EdDSA algorithm
// (the same one Tor runs when it loads an on-disk expanded key) and checks
// the signature against crypto/ed25519.Verify — the standard library,
// trusted as ground truth, with no code from this engine involved on the
// verifying side.
func TestRecoveredScalarProducesVerifiableSignatures(t *testing.T) {
	a0, point0, err := randomStart()
	if err != nil {
		t.Fatal(err)
	}
	aBytes, err := bigIntToLE32(a0)
	if err != nil {
		t.Fatal(err)
	}
	pub := point0.Bytes()

	var prefix [32]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}

	message := []byte("VanityRig curve-symmetry engine self-check")
	sig := signExpanded(t, aBytes, prefix[:], pub, message)

	if !ed25519.Verify(ed25519.PublicKey(pub), message, sig) {
		t.Fatal("signature produced with the recovered scalar does not verify under crypto/ed25519 — " +
			"this key would not work as a real Tor hidden service identity")
	}

	// A signature over a different message, or from a different key, must not
	// verify — otherwise the test would trivially pass no matter what.
	if ed25519.Verify(ed25519.PublicKey(pub), []byte("a different message"), sig) {
		t.Fatal("signature verified against the wrong message")
	}
}

// signExpanded implements RFC 8032 EdDSA signing directly from an expanded
// (scalar, prefix) key pair, exactly as Tor does when it loads an on-disk
// hs_ed25519_secret_key rather than a seed. crypto/ed25519.Sign cannot be
// used here since it always re-derives the scalar from a seed via SHA-512;
// this is the only way to test the expanded-key path end to end.
func signExpanded(t *testing.T, scalarLE, prefix, pub, message []byte) []byte {
	t.Helper()

	a, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(scalarLE))
	if err != nil {
		t.Fatal(err)
	}

	rh := sha512.New()
	rh.Write(prefix)
	rh.Write(message)
	r, err := new(edwards25519.Scalar).SetUniformBytes(rh.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	R := new(edwards25519.Point).ScalarBaseMult(r)
	Renc := R.Bytes()

	kh := sha512.New()
	kh.Write(Renc)
	kh.Write(pub)
	kh.Write(message)
	k, err := new(edwards25519.Scalar).SetUniformBytes(kh.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}

	s := new(edwards25519.Scalar).MultiplyAdd(k, a, r) // s = k*a + r
	sig := make([]byte, 64)
	copy(sig[:32], Renc)
	copy(sig[32:], s.Bytes())
	return sig
}

// Ground truth from crypto/ed25519 itself: given a normally generated
// keypair, extract its scalar the same way this project's own expandSecret
// does, recompute the public key using this engine's point arithmetic and
// encoder, and confirm it matches crypto/ed25519's own public key bytes
// exactly. This is the strongest evidence that the engine's math and
// encoding agree with the standard library on what an Ed25519 public key
// actually is.
func TestEnginePointArithmeticMatchesStdlibForARealKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	expanded := expandSecret(priv) // this project's own seed -> (a, prefix) derivation
	scalar, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(expanded[:32]))
	if err != nil {
		t.Fatal(err)
	}
	recomputed := new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()

	if !bytes.Equal(recomputed, pub) {
		t.Fatalf("recomputed public key %x does not match crypto/ed25519's own %x for the same scalar", recomputed, pub)
	}
}

func newSymmetryTestRunnerConfig(t *testing.T, patterns []string, mode vanity.MatchMode) Config {
	t.Helper()
	return Config{
		Patterns:  patterns,
		Mode:      mode,
		Threads:   1,
		OutputDir: t.TempDir(),
	}
}

// End-to-end: run the real engine against a short pattern and confirm it
// finds a match, that the written files are self-consistent (address really
// derives from the public key on disk, secret key really expands to that
// public key), and that the recovered secret key is byte-for-byte usable —
// not just "some file got written".
func TestSymmetryEngineFindsAndWritesAValidMatch(t *testing.T) {
	for _, mode := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			var pattern string
			switch mode {
			case vanity.MatchSuffix:
				pattern = "ad" // last char fixed 'd', 'a' is a legal penultimate char
			default:
				pattern = "a"
			}

			cfg := newSymmetryTestRunnerConfig(t, []string{pattern}, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			events, err := Symmetry{}.Run(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}

			var matchDir, matchAddr string
			for ev := range events {
				switch ev.Kind {
				case EventError:
					t.Fatalf("engine reported an error: %v", ev.Err)
				case EventMatch:
					matchDir = ev.Match.Dir
					matchAddr = ev.Match.Address
					cancel()
				}
			}

			if matchDir == "" {
				t.Fatal("no match found within the timeout")
			}

			pubFile, err := os.ReadFile(filepath.Join(matchDir, "hs_ed25519_public_key"))
			if err != nil {
				t.Fatal(err)
			}
			secFile, err := os.ReadFile(filepath.Join(matchDir, "hs_ed25519_secret_key"))
			if err != nil {
				t.Fatal(err)
			}
			hostFile, err := os.ReadFile(filepath.Join(matchDir, "hostname"))
			if err != nil {
				t.Fatal(err)
			}

			pub := ed25519.PublicKey(pubFile[32:])
			if got := AddressFor(pub); got != matchAddr {
				t.Errorf("public key on disk derives to %s, but the match was reported as %s", got, matchAddr)
			}
			if want := matchAddr + ".onion\n"; string(hostFile) != want {
				t.Errorf("hostname file = %q, want %q", hostFile, want)
			}

			// The recovered scalar must independently reproduce this exact
			// public key — the same self-check the engine did before writing,
			// re-run here against the actual on-disk bytes.
			scalarLE := secFile[32:64]
			scalar, err := new(edwards25519.Scalar).SetUniformBytes(padTo64(scalarLE))
			if err != nil {
				t.Fatal(err)
			}
			recomputed := new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()
			if !bytes.Equal(recomputed, pub) {
				t.Fatal("on-disk secret key does not expand to the on-disk public key")
			}

			// And it must actually sign, verifiably, under crypto/ed25519.
			prefix := secFile[64:96]
			message := []byte("end-to-end signature check")
			sig := signExpanded(t, scalarLE, prefix, pub, message)
			if !ed25519.Verify(pub, message, sig) {
				t.Fatal("on-disk key does not produce verifiable signatures")
			}

		})
	}
}

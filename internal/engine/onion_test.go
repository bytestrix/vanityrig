package engine

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// realKeyDir is a key pair produced by mkp224o during the search this project
// came out of. Our own output must be byte-identical to it, since these files are
// consumed by Tor and a format mistake yields an address that silently fails to
// serve. Skipped when the directory is absent so the suite stays portable.
const realKeyDir = "/home/x/borderland_vanity/borderx6srnnpcnl24ppsnmzcdqch5pwnx45lp3lxo6inbb3m4jadcid.onion"

func TestAddressMatchesRealMkp224oKey(t *testing.T) {
	pubFile, err := os.ReadFile(filepath.Join(realKeyDir, "hs_ed25519_public_key"))
	if err != nil {
		t.Skip("reference mkp224o key not present on this machine")
	}
	want, err := os.ReadFile(filepath.Join(realKeyDir, "hostname"))
	if err != nil {
		t.Skip("reference hostname not present")
	}

	if len(pubFile) != 64 {
		t.Fatalf("public key file is %d bytes, want 64", len(pubFile))
	}
	// Our header must match the real one exactly.
	if !bytes.Equal(pubFile[:32], publicKeyHeader) {
		t.Errorf("public key header mismatch:\n got %q\nwant %q", pubFile[:32], publicKeyHeader)
	}

	got := AddressFor(ed25519.PublicKey(pubFile[32:]))
	wantAddr := strings.TrimSuffix(strings.TrimSpace(string(want)), ".onion")
	if got != wantAddr {
		t.Errorf("derived address:\n got %s\nwant %s", got, wantAddr)
	}
}

func TestSecretKeyHeaderMatchesRealFile(t *testing.T) {
	secFile, err := os.ReadFile(filepath.Join(realKeyDir, "hs_ed25519_secret_key"))
	if err != nil {
		t.Skip("reference mkp224o key not present on this machine")
	}
	if len(secFile) != 96 {
		t.Fatalf("secret key file is %d bytes, want 96", len(secFile))
	}
	if !bytes.Equal(secFile[:32], secretKeyHeader) {
		t.Errorf("secret key header mismatch:\n got %q\nwant %q", secFile[:32], secretKeyHeader)
	}
}

func TestAddressIsWellFormed(t *testing.T) {
	for i := 0; i < 200; i++ {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		addr := AddressFor(pub)

		if len(addr) != vanity.AddressLen {
			t.Fatalf("address is %d chars, want %d: %s", len(addr), vanity.AddressLen, addr)
		}
		if !vanity.IsBase32(addr) {
			t.Fatalf("address is not valid base32: %s", addr)
		}
		// The protocol rules the advisor relies on must hold for keys we generate
		// ourselves, not just for ones observed in the wild.
		if !strings.HasSuffix(addr, "d") {
			t.Fatalf("address does not end in 'd': %s", addr)
		}
		if got := addr[vanity.AddressLen-2:]; !map[string]bool{"ad": true, "id": true, "qd": true, "yd": true}[got] {
			t.Fatalf("address ends %q, want one of ad/id/qd/yd: %s", got, addr)
		}
	}
}

// The files we write must be loadable as a working hidden service: the address in
// hostname has to be the one the public key actually derives, and the secret key
// has to expand to that same public key. A mismatch here is the failure mode
// where Tor starts happily and serves nothing.
func TestWrittenKeyFilesAreSelfConsistent(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDir := filepath.Join(dir, "test.onion")
	if err := WriteKeyFiles(keyDir, pub, priv); err != nil {
		t.Fatal(err)
	}

	hostname, err := os.ReadFile(filepath.Join(keyDir, "hostname"))
	if err != nil {
		t.Fatal(err)
	}
	pubFile, err := os.ReadFile(filepath.Join(keyDir, "hs_ed25519_public_key"))
	if err != nil {
		t.Fatal(err)
	}
	secFile, err := os.ReadFile(filepath.Join(keyDir, "hs_ed25519_secret_key"))
	if err != nil {
		t.Fatal(err)
	}

	if len(pubFile) != 64 {
		t.Errorf("public key file is %d bytes, want 64", len(pubFile))
	}
	if len(secFile) != 96 {
		t.Errorf("secret key file is %d bytes, want 96", len(secFile))
	}

	wantHost := AddressFor(pub) + ".onion\n"
	if string(hostname) != wantHost {
		t.Errorf("hostname = %q, want %q", hostname, wantHost)
	}
	if !bytes.Equal(pubFile[32:], pub) {
		t.Error("public key file does not contain the public key we generated")
	}
	if !bytes.Equal(secFile[32:], expandSecret(priv)) {
		t.Error("secret key file does not contain the expanded secret key")
	}
}

// The secret key is the permanent identity of a site. It must never exist on disk
// with loose permissions, not even briefly.
func TestKeyFilesAreNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keyDir := filepath.Join(dir, "perm.onion")
	if err := WriteKeyFiles(keyDir, pub, priv); err != nil {
		t.Fatal(err)
	}

	di, err := os.Stat(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("key directory mode is %04o, want 0700", perm)
	}
	for _, name := range []string{"hostname", "hs_ed25519_public_key", "hs_ed25519_secret_key"} {
		fi, err := os.Stat(filepath.Join(keyDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode is %04o, want 0600", name, perm)
		}
	}
}

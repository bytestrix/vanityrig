package engine

import (
	"crypto/ed25519"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tor's on-disk key files are a 32-byte ASCII header followed by the key bytes.
// These strings are padded with NULs to exactly 32 bytes; the byte layout was
// confirmed against real mkp224o output before being written here, because a key
// file in the wrong format produces an address Tor silently refuses to serve.
var (
	secretKeyHeader = padHeader("== ed25519v1-secret: type0 ==")
	publicKeyHeader = padHeader("== ed25519v1-public: type0 ==")
)

func padHeader(s string) []byte {
	h := make([]byte, 32)
	copy(h, s)
	return h
}

// onionBase32 is the lowercase, unpadded alphabet Tor renders addresses in.
var onionBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// AddressFor derives the 56-character v3 onion address (without the ".onion"
// suffix) from an ed25519 public key.
func AddressFor(pub ed25519.PublicKey) string {
	const version = byte(0x03)

	h := sha3.New256()
	h.Write([]byte(".onion checksum"))
	h.Write(pub)
	h.Write([]byte{version})
	checksum := h.Sum(nil)[:2]

	buf := make([]byte, 0, 35)
	buf = append(buf, pub...)
	buf = append(buf, checksum...)
	buf = append(buf, version)

	return strings.ToLower(onionBase32.EncodeToString(buf))
}

// expandSecret converts an ed25519 seed into the 64-byte expanded secret key that
// Tor stores. Go keeps private keys as seed||public, but Tor wants the clamped
// SHA-512 expansion, so writing Go's representation straight to disk would
// produce a key file Tor cannot use.
func expandSecret(priv ed25519.PrivateKey) []byte {
	h := sha512.Sum512(priv.Seed())
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	out := make([]byte, 64)
	copy(out, h[:])
	return out
}

// WriteKeyFiles saves a found keypair in the layout Tor's HiddenServiceDir
// expects, byte-compatible with what mkp224o produces.
//
// Permissions are tight from the moment of creation rather than fixed up
// afterwards: this file is the permanent identity of a site, and a window where
// it sits world-readable is a window too many.
func WriteKeyFiles(dir string, pub ed25519.PublicKey, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}

	addr := AddressFor(pub)
	files := []struct {
		name string
		data []byte
	}{
		{"hostname", []byte(addr + ".onion\n")},
		{"hs_ed25519_public_key", append(append([]byte{}, publicKeyHeader...), pub...)},
		{"hs_ed25519_secret_key", append(append([]byte{}, secretKeyHeader...), expandSecret(priv)...)},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

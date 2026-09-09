// Package vanity implements the search-space arithmetic and pattern validation
// behind VanityRig's feasibility advisor.
//
// Everything here works in BITS of difficulty rather than decimal counts. That is
// a deliberate accuracy measure, not a style preference: a 50-vs-60 bit mistake is
// glaring, while a 1.126e15-vs-1.153e18 mistake is invisible.
package vanity

import "strings"

// Base32Alphabet is the lowercase RFC 4648 base32 alphabet Tor renders addresses
// in. Note the absent digits: 0, 1, 8 and 9 are not encodable.
const Base32Alphabet = "abcdefghijklmnopqrstuvwxyz234567"

// BitsPerChar is how many bits one base32 character carries.
const BitsPerChar = 5

// AddressLen is the character length of every v3 onion address, excluding the
// ".onion" suffix. A v3 address encodes 35 bytes (32 pubkey + 2 checksum + 1
// version) = 280 bits, which divides evenly into 56 base32 characters with no
// padding.
const AddressLen = 56

// The trailing characters of every v3 address are constrained by the fixed
// version byte (0x03), because 280 bits divides evenly and the version byte lands
// wholly inside the final two characters.
//
// Verified analytically and empirically against 200,000 generated addresses:
//
//   - char 55 (last) is ALWAYS 'd'    — the low 5 bits of 0x03
//   - char 54 is ALWAYS one of a/i/q/y — 2 free bits from the checksum, then the
//     high 3 bits of 0x03, which are zero
//
// This makes most suffixes outright impossible rather than merely slow, and makes
// the possible ones cheaper than an equivalent prefix. Both facts must be surfaced
// to the user before a search starts.
const FinalChar = 'd'

// PenultimateChars are the only values that can appear at index 54.
var PenultimateChars = []rune{'a', 'i', 'q', 'y'}

func isPenultimateValid(r rune) bool {
	for _, c := range PenultimateChars {
		if c == r {
			return true
		}
	}
	return false
}

// IsBase32 reports whether every character of s is encodable in a v3 address.
func IsBase32(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(Base32Alphabet, r) {
			return false
		}
	}
	return true
}

// Normalize lowercases a user-supplied pattern. Onion addresses are rendered in
// lowercase base32, so "BORDER" and "border" describe the same search.
func Normalize(pattern string) string {
	return strings.ToLower(strings.TrimSpace(pattern))
}

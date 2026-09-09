package vanity

import (
	"fmt"
	"math"
	"math/big"
)

// MatchMode is where in the address a pattern is allowed to appear.
type MatchMode string

const (
	MatchPrefix   MatchMode = "prefix"
	MatchSuffix   MatchMode = "suffix"
	MatchAnywhere MatchMode = "anywhere"
)

// ParseMatchMode converts user input into a MatchMode.
func ParseMatchMode(s string) (MatchMode, error) {
	switch MatchMode(Normalize(s)) {
	case MatchPrefix:
		return MatchPrefix, nil
	case MatchSuffix:
		return MatchSuffix, nil
	case MatchAnywhere:
		return MatchAnywhere, nil
	case "":
		return MatchPrefix, nil
	default:
		return "", fmt.Errorf("unknown match mode %q (want prefix, suffix or anywhere)", s)
	}
}

// charProbability returns the chance that a random v3 address has rune r at
// address index idx.
//
// This single function is the source of truth for all three match modes, so the
// tail constraints cannot be applied inconsistently between them.
func charProbability(r rune, idx int) float64 {
	switch idx {
	case AddressLen - 1: // always 'd', determined by version byte 0x03
		if r == FinalChar {
			return 1
		}
		return 0
	case AddressLen - 2: // always one of a/i/q/y — 2 free bits
		if isPenultimateValid(r) {
			return 1.0 / 4.0
		}
		return 0
	default:
		return 1.0 / 32.0
	}
}

// ProbabilityAt returns the chance a random address contains pattern starting at
// address index start. Returns 0 if the pattern cannot fit or violates a
// protocol-fixed character.
func ProbabilityAt(pattern string, start int) float64 {
	runes := []rune(pattern)
	if start < 0 || start+len(runes) > AddressLen {
		return 0
	}
	p := 1.0
	for i, r := range runes {
		p *= charProbability(r, start+i)
		if p == 0 {
			return 0
		}
	}
	return p
}

// Probability returns the chance that a single randomly generated address
// satisfies pattern under the given mode.
//
// For MatchAnywhere the per-position chances are combined as 1-Π(1-p), which
// stays bounded in [0,1] for short patterns where a naive sum would not.
func Probability(pattern string, mode MatchMode) float64 {
	pattern = Normalize(pattern)
	runes := []rune(pattern)
	if len(runes) == 0 || len(runes) > AddressLen {
		return 0
	}

	switch mode {
	case MatchPrefix:
		return ProbabilityAt(pattern, 0)
	case MatchSuffix:
		return ProbabilityAt(pattern, AddressLen-len(runes))
	case MatchAnywhere:
		var ps []float64
		for start := 0; start+len(runes) <= AddressLen; start++ {
			ps = append(ps, ProbabilityAt(pattern, start))
		}
		return unionProbability(ps)
	default:
		return 0
	}
}

// unionProbability returns the chance that at least one of the independent
// events occurs: 1 - Π(1-p).
//
// It accumulates in log space, which matters more than it looks. Computed
// directly, 1-p rounds to exactly 1.0 once p falls below float64 epsilon
// (~2.2e-16), the product stays 1.0, and the result cancels to a hard zero — so a
// merely astronomically-slow search gets reported as *impossible*, which is a
// categorically different and much stronger claim. That bug appeared twice in this
// file (anywhere-mode and multi-pattern) before being centralised here; do not
// re-inline it.
func unionProbability(ps []float64) float64 {
	sumLog := 0.0
	for _, p := range ps {
		if p >= 1 {
			return 1
		}
		sumLog += math.Log1p(-p)
	}
	return -math.Expm1(sumLog)
}

// CombinedProbability returns the chance a single address satisfies ANY of the
// patterns (multi-filter searches are an OR).
func CombinedProbability(patterns []string, mode MatchMode) float64 {
	ps := make([]float64, 0, len(patterns))
	for _, p := range patterns {
		ps = append(ps, Probability(p, mode))
	}
	return unionProbability(ps)
}

// DifficultyBits expresses a probability as bits of work: -log2(p).
// A 7-character prefix is 35 bits, a 10-character prefix is 50 bits.
func DifficultyBits(p float64) float64 {
	if p <= 0 {
		return math.Inf(1)
	}
	b := -math.Log2(p)
	if b == 0 {
		return 0 // avoid printing "-0.0" when a match is certain
	}
	return b
}

// ExpectedTries is the mean number of candidate keys needed, i.e. 1/p.
func ExpectedTries(p float64) float64 {
	if p <= 0 {
		return math.Inf(1)
	}
	return 1 / p
}

// PrefixSpace returns the exact integer 32^n, for showing work in the advisor
// output. Always computed, never hardcoded, so a wrong exponent can't hide
// behind a typed-out constant.
func PrefixSpace(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(32), big.NewInt(int64(n)), nil)
}

// PreflightSyntax checks only whether a pattern is well-formed — the right
// alphabet and a length that fits inside an address. These are typos, and there is
// no useful estimate to report for them.
//
// It deliberately says nothing about whether the pattern can ever match. That is a
// finding to be reported with its arithmetic, not an input error to be thrown back
// at the user: informed consent, not gatekeeping.
func PreflightSyntax(pattern string) error {
	pattern = Normalize(pattern)
	runes := []rune(pattern)

	if len(runes) == 0 {
		return fmt.Errorf("pattern is empty")
	}
	if !IsBase32(pattern) {
		return fmt.Errorf("pattern %q contains characters that cannot appear in an onion address; "+
			"valid characters are a-z and 2-7 (note: 0, 1, 8 and 9 are not encodable)", pattern)
	}
	if len(runes) > AddressLen {
		return fmt.Errorf("pattern is %d characters but an onion address is only %d",
			len(runes), AddressLen)
	}
	return nil
}

// Validate reports whether a pattern can ever match, explaining why not when it
// cannot. Callers deciding whether to *start* a search should treat a failure here
// as something to show the user and let them override, not as a hard block.
func Validate(pattern string, mode MatchMode) error {
	if err := PreflightSyntax(pattern); err != nil {
		return err
	}
	pattern = Normalize(pattern)
	runes := []rune(pattern)

	if mode == MatchSuffix {
		last := runes[len(runes)-1]
		if last != FinalChar {
			return fmt.Errorf("%q cannot be a suffix: every v3 onion address ends in %q, "+
				"but this pattern ends in %q. Try --match anywhere instead",
				pattern, string(FinalChar), string(last))
		}
		if len(runes) >= 2 {
			pen := runes[len(runes)-2]
			if !isPenultimateValid(pen) {
				return fmt.Errorf("%q cannot be a suffix: the second-to-last character of a v3 "+
					"onion address is always one of a/i/q/y, but this pattern has %q there. "+
					"Try --match anywhere instead", pattern, string(pen))
			}
		}
	}

	// A pattern long enough to reach the end of the address is subject to the same
	// fixed trailing characters as a suffix, whatever mode was requested. Explain
	// that rather than falling through to a bare "can never match".
	if p := Probability(pattern, mode); p <= 0 {
		if mode == MatchPrefix && len(runes) == AddressLen {
			return fmt.Errorf("%q spans the whole address, so its last two characters are "+
				"fixed by the protocol: the final one must be %q and the one before it must be "+
				"a/i/q/y. This pattern has %q and %q", pattern, string(FinalChar),
				string(runes[len(runes)-2]), string(runes[len(runes)-1]))
		}
		return fmt.Errorf("%q can never match in %s mode", pattern, mode)
	}
	return nil
}

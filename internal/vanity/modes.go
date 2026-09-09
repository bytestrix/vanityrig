package vanity

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// allModes is every match mode, in the order they're always shown to a user.
var allModes = []MatchMode{MatchPrefix, MatchSuffix, MatchAnywhere}

// ModeComparison is a side-by-side look at how a set of patterns fares under
// each match mode, for a user who hasn't committed to one yet. Comparing modes
// keeps the whole word intact in every row — unlike shortening it — which is
// why this replaced offering shortened spellings as the default comparison.
type ModeComparison struct {
	Patterns  []string
	Rate      float64
	Estimates map[MatchMode]Estimate
	Best      MatchMode // fastest achievable mode; "" if none of the three can ever match
}

// CompareModes estimates patterns under prefix, suffix, and anywhere matching.
func CompareModes(patterns []string, rate float64) ModeComparison {
	cmp := ModeComparison{
		Patterns:  patterns,
		Rate:      rate,
		Estimates: make(map[MatchMode]Estimate, len(allModes)),
	}
	var bestP50 time.Duration = -1
	for _, m := range allModes {
		e := NewEstimate(patterns, m, rate)
		cmp.Estimates[m] = e
		if e.Probability > 0 && (bestP50 < 0 || e.P50 < bestP50) {
			bestP50 = e.P50
			cmp.Best = m
		}
	}
	return cmp
}

// legalEndings lists every possible last-two-characters of a v3 address —
// computed from the protocol constants, never typed out, so the list can't
// drift from the rule it's describing.
func legalEndings() []string {
	out := make([]string, 0, len(PenultimateChars))
	for _, c := range PenultimateChars {
		out = append(out, string(c)+string(FinalChar))
	}
	return out
}

// suffixReason names, concretely, why a single-word pattern can't be a
// suffix: not just which rule it breaks, but what a legal ending actually
// looks like, so the reader doesn't have to reconstruct that themselves.
// Empty if the pattern is fine as a suffix.
func suffixReason(pattern string) string {
	pattern = Normalize(pattern)
	runes := []rune(pattern)
	if len(runes) == 0 {
		return ""
	}

	badEnding := false
	if last := runes[len(runes)-1]; last != FinalChar {
		badEnding = true
	} else if len(runes) >= 2 && !isPenultimateValid(runes[len(runes)-2]) {
		badEnding = true
	}
	if !badEnding {
		return ""
	}

	got := string(runes[len(runes)-1:])
	if len(runes) >= 2 {
		got = string(runes[len(runes)-2:])
	}
	return fmt.Sprintf("every v3 address ends in %s — this one ends in %q",
		strings.Join(legalEndings(), "/"), got)
}

// WriteModeComparison prints the three-mode table, plus a tail-completion
// suggestion when a single word can never legally end an address.
func WriteModeComparison(w io.Writer, cmp ModeComparison) {
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }
	label := strings.Join(cmp.Patterns, ", ")

	p("")
	p("Where should %q appear in the address?", label)
	p("")
	p("  %-10s %-14s %s", "mode", "typical time", "")
	p("  %s", strings.Repeat("─", 58))
	for _, m := range allModes {
		e := cmp.Estimates[m]
		timing := humanDuration(e.P50, e.YearsFor(0.5))
		note := ""
		switch {
		case e.Probability <= 0:
			timing = "impossible"
			if m == MatchSuffix && len(cmp.Patterns) == 1 {
				note = suffixReason(cmp.Patterns[0])
			}
		case m == cmp.Best:
			note = "← recommended"
		}
		p("  %-10s %-14s %s", m, timing, note)
	}

	// A word that can never end an address can still sit right before the two
	// characters the protocol forces there — worth naming, not just marking
	// suffix "impossible" and moving on.
	if len(cmp.Patterns) == 1 {
		if e := cmp.Estimates[MatchSuffix]; e.Probability <= 0 {
			word := cmp.Patterns[0]
			if len(Normalize(word))+2 <= AddressLen {
				comps := SuffixCompletions(word)
				tail := NewEstimate(comps, MatchSuffix, cmp.Rate)
				if tail.Probability > 0 {
					p("")
					p("  %q can never end an address, but %q can. Searching all four",
						word, comps[0])
					p("  legal endings together takes about %s.",
						humanDuration(tail.P50, tail.YearsFor(0.5)))
				}
			}
		}
	}
	p("")
}

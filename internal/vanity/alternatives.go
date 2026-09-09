package vanity

import (
	"sort"
	"strings"
	"time"
)

// Alternative is a suggested substitute pattern that is cheaper than what the
// user asked for, with its own fully computed estimate so the suggestion is
// checkable rather than hand-wavy.
type Alternative struct {
	Pattern  string   // display label
	Patterns []string // what to actually search; more than one means an OR
	Mode     MatchMode
	Kind     string // "requested" | "shorter" | "vowel-dropped" | "mode-change" | "tail-completed"
	Estimate Estimate

	// Placement and KeepsWord describe the trade-off in the terms a user actually
	// weighs: where the word ends up, and whether they still get all of it. Cost
	// is only one axis — someone may well want the slower option because it puts
	// the word where they wanted it.
	Placement string
	KeepsWord bool
}

// Requested reports whether this is the option the user originally asked for.
func (a Alternative) Requested() bool { return a.Kind == "requested" }

func placementFor(mode MatchMode, kind string) string {
	switch {
	case kind == "tail-completed":
		return "end"
	case mode == MatchPrefix && (kind == "shorter" || kind == "vowel-dropped"):
		return "start (shortened)"
	case mode == MatchPrefix:
		return "start"
	case mode == MatchSuffix:
		return "end"
	default:
		return "anywhere"
	}
}

// SuffixCompletions returns word with each legal address ending appended.
//
// A word that cannot be a suffix can still sit immediately before the mandatory
// ending: "borderland" can never finish an address, but "...borderlandad" can.
// All four completions must be searched together as an OR — any one of them
// gives the user the same result, and searching a single ending is exactly 4x
// slower for no benefit.
func SuffixCompletions(word string) []string {
	word = Normalize(word)
	out := make([]string, 0, len(PenultimateChars))
	for _, pen := range PenultimateChars {
		out = append(out, word+string(pen)+string(FinalChar))
	}
	return out
}

const vowels = "aeiou"

// dropVowels removes the last n vowels from s, right to left. Dropping from the
// right keeps the recognisable opening of a word intact: "borderland" loses its
// tail vowels before its first one.
func dropVowels(s string, n int) string {
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0 && n > 0; i-- {
		if strings.ContainsRune(vowels, runes[i]) {
			runes = append(runes[:i], runes[i+1:]...)
			n--
		}
	}
	return string(runes)
}

func countVowels(s string) int {
	n := 0
	for _, r := range s {
		if strings.ContainsRune(vowels, r) {
			n++
		}
	}
	return n
}

// SuggestAlternatives returns cheaper patterns that are achievable within budget
// at the given throughput, best (longest, most recognisable) first.
//
// Suggestions are always accompanied by a real Estimate — the advisor must never
// propose a fallback without having computed it, since an unchecked suggestion is
// how a wrong number reaches the user.
func SuggestAlternatives(pattern string, mode MatchMode, keysPerSec float64, budget time.Duration) []Alternative {
	pattern = Normalize(pattern)
	var out []Alternative
	seen := map[string]bool{pattern + string(mode): true}

	original := NewEstimate([]string{pattern}, mode, keysPerSec)

	add := func(p string, m MatchMode, kind string) {
		key := p + string(m)
		if p == "" || seen[key] {
			return
		}
		if err := Validate(p, m); err != nil {
			return
		}
		est := NewEstimate([]string{p}, m, keysPerSec)
		if Saturated(est.P50) {
			return
		}
		// The budget gates suggestions that sacrifice the pattern. A mode change
		// keeps the user's whole word, so it is judged on improvement instead:
		// "8 days, full word" is more useful than "30 seconds, shorter word", and
		// filtering it on budget would hide the best option the tool has.
		if kind == "mode-change" {
			// When the requested search is impossible or unbounded, any workable
			// mode is an infinite improvement and must be offered — this is the
			// case where the user most needs a way forward.
			if original.Probability > 0 && !Saturated(original.P50) && est.P50 >= original.P50/2 {
				return
			}
		} else if est.P50 > budget {
			return
		}
		seen[key] = true
		out = append(out, Alternative{
			Pattern:   p,
			Patterns:  []string{p},
			Mode:      m,
			Kind:      kind,
			Estimate:  est,
			Placement: placementFor(m, kind),
			KeepsWord: p == pattern,
		})
	}

	// Switching to anywhere mode keeps the whole word and is often the single
	// biggest win available, so it is offered before shortening anything.
	if mode != MatchAnywhere {
		add(pattern, MatchAnywhere, "mode-change")
	}

	// A word that cannot end an address can still sit directly before the two
	// characters the protocol forces there, which reads almost identically:
	// "...borderlandid". Offered only when the user actually asked for a suffix
	// and the word was rejected for the tail rule.
	if mode == MatchSuffix && Probability(pattern, MatchSuffix) <= 0 && IsBase32(pattern) {
		comps := SuffixCompletions(pattern)
		if len(pattern)+2 <= AddressLen {
			est := NewEstimate(comps, MatchSuffix, keysPerSec)
			if est.Probability > 0 && !Saturated(est.P50) {
				out = append(out, Alternative{
					Pattern:   pattern + "{ad,id,qd,yd}",
					Patterns:  comps,
					Mode:      MatchSuffix,
					Kind:      "tail-completed",
					Estimate:  est,
					Placement: "end",
					KeepsWord: true,
				})
			}
		}
	}

	runes := []rune(pattern)
	for n := len(runes) - 1; n >= 4; n-- {
		add(string(runes[:n]), mode, "shorter")
	}
	for k := 1; k <= countVowels(pattern); k++ {
		add(dropVowels(pattern, k), mode, "vowel-dropped")
	}

	// Longest first: a longer surviving pattern is more recognisable, which is the
	// whole point of a vanity address.
	// Sort on the length actually being searched, not the display label, so a
	// label like "borderland{ad,id,qd,yd}" does not jump the queue on width.
	searchLen := func(a Alternative) int {
		if len(a.Patterns) > 0 {
			return len([]rune(a.Patterns[0]))
		}
		return len([]rune(a.Pattern))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if searchLen(out[i]) != searchLen(out[j]) {
			return searchLen(out[i]) > searchLen(out[j])
		}
		return out[i].Estimate.P50 < out[j].Estimate.P50
	})
	return out
}

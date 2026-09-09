package vanity

import (
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

// --- Accuracy requirement #4: known-good table. --------------------------------
// The session that motivated this project had three of four such constants right
// and one wrong by 1024x. A table test makes that class of error impossible to
// ship silently.

func TestPrefixSpaceKnownValues(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{6, "1073741824"},           // 32^6  = 2^30
		{7, "34359738368"},          // 32^7  = 2^35
		{8, "1099511627776"},        // 32^8  = 2^40
		{10, "1125899906842624"},    // 32^10 = 2^50  <-- the one that was wrong
		{12, "1152921504606846976"}, // 32^12 = 2^60  <-- what was used by mistake
	}
	for _, c := range cases {
		got := PrefixSpace(c.n).String()
		if got != c.want {
			t.Errorf("PrefixSpace(%d) = %s, want %s", c.n, got, c.want)
		}
	}
}

// Accuracy requirement #3: 32^L must equal 2^(5L). This single assertion would
// have caught the original bug immediately.
func TestPrefixSpaceIsPowerOfTwo(t *testing.T) {
	for n := 1; n <= AddressLen; n++ {
		want := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(BitsPerChar*n)), nil)
		if PrefixSpace(n).Cmp(want) != 0 {
			t.Fatalf("32^%d != 2^%d", n, BitsPerChar*n)
		}
	}
}

// Each extra character must multiply the space by exactly 32, never more or less.
func TestPrefixSpaceRatioIsExactly32(t *testing.T) {
	for n := 1; n < AddressLen; n++ {
		a, b := PrefixSpace(n), PrefixSpace(n+1)
		ratio := new(big.Int).Div(b, a)
		if ratio.Int64() != 32 {
			t.Fatalf("space(%d)/space(%d) = %s, want 32", n+1, n, ratio)
		}
	}
}

func TestPrefixDifficultyBits(t *testing.T) {
	cases := []struct {
		pattern string
		bits    float64
	}{
		{"a", 5}, {"border", 30}, {"borderx", 35}, {"borderla", 40}, {"borderland", 50},
	}
	for _, c := range cases {
		got := DifficultyBits(Probability(c.pattern, MatchPrefix))
		if math.Abs(got-c.bits) > 1e-9 {
			t.Errorf("%q prefix = %.4f bits, want %.1f", c.pattern, got, c.bits)
		}
	}
}

// --- Protocol tail constraints -------------------------------------------------
// Verified empirically against 200,000 generated addresses before being encoded
// here: char 55 is always 'd', char 54 is always one of a/i/q/y.

func TestFinalCharIsFixed(t *testing.T) {
	if got := charProbability('d', AddressLen-1); got != 1 {
		t.Errorf("P('d' at last index) = %v, want 1", got)
	}
	for _, r := range Base32Alphabet {
		if r == FinalChar {
			continue
		}
		if got := charProbability(r, AddressLen-1); got != 0 {
			t.Errorf("P(%q at last index) = %v, want 0", string(r), got)
		}
	}
}

func TestPenultimateCharHasFourOptions(t *testing.T) {
	allowed := 0
	for _, r := range Base32Alphabet {
		p := charProbability(r, AddressLen-2)
		switch {
		case p == 0:
		case math.Abs(p-0.25) < 1e-12:
			allowed++
		default:
			t.Errorf("P(%q at index 54) = %v, want 0 or 0.25", string(r), p)
		}
	}
	if allowed != 4 {
		t.Errorf("got %d allowed penultimate chars, want 4 (a/i/q/y)", allowed)
	}
}

// The bug this test guards: "borderland ends in d, so it works as a suffix" is
// wrong, because its second-to-last character is 'n'.
func TestBorderlandIsNotAValidSuffix(t *testing.T) {
	if err := Validate("borderland", MatchSuffix); err == nil {
		t.Fatal("expected borderland to be rejected as a suffix (penultimate 'n' is not in a/i/q/y)")
	}
	if p := Probability("borderland", MatchSuffix); p != 0 {
		t.Errorf("P(borderland as suffix) = %v, want 0", p)
	}
}

func TestSuffixValidation(t *testing.T) {
	cases := []struct {
		pattern string
		ok      bool
		why     string
	}{
		{"d", true, "the last character is always d, so a bare d always matches"},
		{"ad", true, "ends d, penultimate a is allowed"},
		{"id", true, "ends d, penultimate i is allowed"},
		{"qd", true, "ends d, penultimate q is allowed"},
		{"yd", true, "ends d, penultimate y is allowed"},
		{"bd", false, "penultimate b is not in a/i/q/y"},
		{"border", false, "does not end in d"},
		{"borderland", false, "ends in d but penultimate n is not allowed"},
		{"borderlad", true, "ends d, penultimate a is allowed"},
	}
	for _, c := range cases {
		err := Validate(c.pattern, MatchSuffix)
		if c.ok && err != nil {
			t.Errorf("Validate(%q, suffix) = %v, want ok (%s)", c.pattern, err, c.why)
		}
		if !c.ok && err == nil {
			t.Errorf("Validate(%q, suffix) = ok, want error (%s)", c.pattern, c.why)
		}
	}
}

// A bare "d" suffix is free: every address already ends in d.
func TestBareDSuffixIsCertain(t *testing.T) {
	if p := Probability("d", MatchSuffix); p != 1 {
		t.Errorf("P(d as suffix) = %v, want 1", p)
	}
	if b := DifficultyBits(Probability("d", MatchSuffix)); b != 0 {
		t.Errorf("difficulty = %v bits, want 0", b)
	}
}

// A valid suffix is cheaper than the same-length prefix, because its final
// character is free and its penultimate one costs 2 bits instead of 5.
func TestValidSuffixIsCheaperThanPrefix(t *testing.T) {
	const p = "borderlad" // 9 chars, valid suffix
	prefixBits := DifficultyBits(Probability(p, MatchPrefix))
	suffixBits := DifficultyBits(Probability(p, MatchSuffix))
	wantSuffix := float64(BitsPerChar*(len(p)-2)) + 2 // 5*(L-2) + 2
	if math.Abs(suffixBits-wantSuffix) > 1e-9 {
		t.Errorf("suffix difficulty = %.3f bits, want %.3f", suffixBits, wantSuffix)
	}
	if suffixBits >= prefixBits {
		t.Errorf("suffix (%.1f bits) should be cheaper than prefix (%.1f bits)", suffixBits, prefixBits)
	}
	if d := prefixBits - suffixBits; math.Abs(d-8) > 1e-9 {
		t.Errorf("expected an 8-bit (256x) discount, got %.3f", d)
	}
}

// --- Match mode relationships (accuracy requirement #3) ------------------------

func TestAnywhereIsAlwaysCheaperThanPrefix(t *testing.T) {
	for _, p := range []string{"a", "bo", "border", "borderx", "borderland", "borderlandx"} {
		pre := Probability(p, MatchPrefix)
		any := Probability(p, MatchAnywhere)
		if any <= pre {
			t.Errorf("%q: anywhere %.3g should exceed prefix %.3g", p, any, pre)
		}
	}
}

func TestAnywhereApproximatesPositionCount(t *testing.T) {
	// For a 10-char pattern there are 56-10+1 = 47 placements. Two of them overlap
	// the constrained tail, so the true gain is slightly under 47x — verifying the
	// exact-position model is doing real work rather than a flat multiply.
	const p = "borderland"
	ratio := Probability(p, MatchAnywhere) / Probability(p, MatchPrefix)
	if ratio > float64(AddressLen-len(p)+1) {
		t.Errorf("anywhere gain %.3f exceeds the %d available positions", ratio, AddressLen-len(p)+1)
	}
	if ratio < 40 {
		t.Errorf("anywhere gain %.3f is implausibly low for %d positions", ratio, AddressLen-len(p)+1)
	}
}

func TestProbabilityIsMonotonicInLength(t *testing.T) {
	prev := 2.0
	for _, p := range []string{"b", "bo", "bor", "bord", "borde", "border", "borderx"} {
		cur := Probability(p, MatchPrefix)
		if cur >= prev {
			t.Errorf("%q: probability %.3g should be lower than the shorter pattern's %.3g", p, cur, prev)
		}
		prev = cur
	}
}

// --- Input validation ----------------------------------------------------------

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []struct{ pattern, why string }{
		{"", "empty"},
		{"border1", "1 is not in the base32 alphabet"},
		{"border0", "0 is not in the base32 alphabet"},
		{"border8", "8 is not in the base32 alphabet"},
		{"border9", "9 is not in the base32 alphabet"},
		{"border-land", "hyphen is not encodable"},
		{"border land", "space is not encodable"},
	}
	for _, c := range cases {
		if err := Validate(c.pattern, MatchPrefix); err == nil {
			t.Errorf("Validate(%q) accepted it, want rejection (%s)", c.pattern, c.why)
		}
	}
}

func TestValidateRejectsOverlongPattern(t *testing.T) {
	long := ""
	for i := 0; i < AddressLen+1; i++ {
		long += "a"
	}
	if err := Validate(long, MatchPrefix); err == nil {
		t.Error("expected rejection of a pattern longer than the address itself")
	}
}

func TestNormalizeHandlesCaseAndSpace(t *testing.T) {
	if got := Normalize("  BorderLand \n"); got != "borderland" {
		t.Errorf("Normalize = %q, want %q", got, "borderland")
	}
	if Probability("BORDER", MatchPrefix) != Probability("border", MatchPrefix) {
		t.Error("uppercase and lowercase patterns should be equivalent")
	}
}

// --- Multi-pattern -------------------------------------------------------------

// Regression: tiny probabilities must survive the union calculation. Computed as
// 1-Π(1-p), a probability below float64 epsilon collapses to a hard zero, turning
// "astronomically slow" into "impossible" — a categorically different claim. This
// bug occurred twice (anywhere-mode and multi-pattern) before being centralised.
func TestUnionProbabilitySurvivesTinyValues(t *testing.T) {
	for _, p := range []float64{1e-17, 1e-31, 1e-60, 1e-100} {
		if got := unionProbability([]float64{p}); got <= 0 {
			t.Errorf("unionProbability([%g]) = %g, must stay positive", p, got)
		}
	}
	// A 20-character prefix is 2^-100 — slow beyond reason, but not impossible.
	long := "borderlandborderland"
	if got := CombinedProbability([]string{long}, MatchPrefix); got <= 0 {
		t.Errorf("CombinedProbability(%q) = %g, want > 0", long, got)
	}
	if e := NewEstimate([]string{long}, MatchPrefix, 22.2e6); e.Probability <= 0 {
		t.Errorf("NewEstimate probability = %g; an infeasible search is not an impossible one", e.Probability)
	}
	// An 11+ character anywhere search must not collapse either.
	if got := Probability("borderlandx", MatchAnywhere); got <= 0 {
		t.Errorf("anywhere probability collapsed to %g", got)
	}
}

// Impossible and infeasible must stay distinguishable: only a protocol violation
// yields probability zero.
func TestInfeasibleIsNotReportedAsImpossible(t *testing.T) {
	infeasible := NewEstimate([]string{"borderlandborderland"}, MatchPrefix, 22.2e6)
	if infeasible.Probability <= 0 {
		t.Error("a very long prefix is infeasible, not impossible")
	}
	impossible := NewEstimate([]string{"border"}, MatchSuffix, 22.2e6)
	if impossible.Probability != 0 {
		t.Error("a suffix violating the trailing-character rule is genuinely impossible")
	}
}

func TestCombinedProbabilityBeatsSingle(t *testing.T) {
	single := Probability("borderx", MatchPrefix)
	multi := CombinedProbability([]string{"borderx", "bordery", "borderz"}, MatchPrefix)
	if multi <= single {
		t.Errorf("three patterns (%.3g) should beat one (%.3g)", multi, single)
	}
	if multi > 3*single {
		t.Errorf("three patterns (%.3g) cannot exceed 3x one (%.3g)", multi, single)
	}
}

func TestDuplicatePatternsAreDeduped(t *testing.T) {
	e := NewEstimate([]string{"border", "border", "BORDER"}, MatchPrefix, 1e6)
	if len(e.Patterns) != 1 {
		t.Errorf("got %d patterns after dedupe, want 1: %v", len(e.Patterns), e.Patterns)
	}
}

// --- Estimation ----------------------------------------------------------------

func TestEstimateMatchesSessionNumbers(t *testing.T) {
	// The real numbers from the search this project came out of: three hosts at a
	// combined ~22.2M keys/sec looking for a 10-character prefix.
	e := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)

	if math.Abs(e.Bits-50) > 1e-9 {
		t.Errorf("bits = %.4f, want 50", e.Bits)
	}
	if got := e.YearsFor(0.5); math.Abs(got-1.11) > 0.05 {
		t.Errorf("P50 = %.3f years, want ~1.11", got)
	}
	if got := e.ExpectedTries / 22.2e6 / (365.25 * 24 * 3600); math.Abs(got-1.61) > 0.05 {
		t.Errorf("mean = %.3f years, want ~1.61", got)
	}
	if e.Verdict != VerdictHard {
		t.Errorf("verdict = %s, want %s", e.Verdict, VerdictHard)
	}
}

func TestPercentileOrdering(t *testing.T) {
	e := NewEstimate([]string{"borderx"}, MatchPrefix, 22.2e6)
	if !(e.P50 < e.Mean && e.Mean < e.P90) {
		t.Errorf("expected P50 < mean < P90, got %v / %v / %v", e.P50, e.Mean, e.P90)
	}
}

func TestZeroRateDoesNotPanicOrWrap(t *testing.T) {
	e := NewEstimate([]string{"border"}, MatchPrefix, 0)
	if e.P50 <= 0 {
		t.Errorf("P50 with no compute = %v, must not be zero or negative", e.P50)
	}
	if !Saturated(e.P50) {
		t.Error("expected a saturated duration when throughput is zero")
	}
}

// Durations past ~292 years overflow int64 nanoseconds. They must saturate rather
// than silently wrap to a negative time.
func TestVeryHardSearchSaturatesInsteadOfWrapping(t *testing.T) {
	e := NewEstimate([]string{"borderlandborderland"}, MatchPrefix, 22.2e6)
	if e.P50 < 0 || e.Mean < 0 {
		t.Errorf("durations wrapped negative: P50=%v mean=%v", e.P50, e.Mean)
	}
	if !Saturated(e.P50) {
		t.Error("expected saturation for a 20-character prefix")
	}
	if y := e.YearsFor(0.5); y < 1e6 {
		t.Errorf("YearsFor should still report a huge finite value, got %v", y)
	}
}

func TestImpossiblePatternIsInfeasible(t *testing.T) {
	e := NewEstimate([]string{"border"}, MatchSuffix, 22.2e6) // cannot end in 'r'
	if e.Probability != 0 {
		t.Errorf("probability = %v, want 0", e.Probability)
	}
	if e.Verdict != VerdictInfeasible {
		t.Errorf("verdict = %s, want %s", e.Verdict, VerdictInfeasible)
	}
	if math.IsInf(e.Bits, 1) != true {
		t.Errorf("bits = %v, want +Inf for an impossible pattern", e.Bits)
	}
}

func TestVerdictBands(t *testing.T) {
	cases := []struct {
		pattern string
		rate    float64
		want    Verdict
	}{
		{"bo", 22.2e6, VerdictTrivial},
		{"borderx", 22.2e6, VerdictReasonable}, // ~18 min: past the 60s trivial band
		{"borderla", 22.2e6, VerdictReasonable},
		{"borderland", 22.2e6, VerdictHard},
		{"borderlandxyz", 22.2e6, VerdictInfeasible},
	}
	for _, c := range cases {
		if got := NewEstimate([]string{c.pattern}, MatchPrefix, c.rate).Verdict; got != c.want {
			t.Errorf("%q at %.0f keys/s: verdict %s, want %s", c.pattern, c.rate, got, c.want)
		}
	}
}

// --- Cloud pricing -------------------------------------------------------------

func TestPriceBurstReproducesTheAWSFinding(t *testing.T) {
	// ~50 large instances for a day should give a coin-flip-or-better shot at a
	// 10-character prefix. This is the finding that reversed the earlier (wrong)
	// "no budget can do this" conclusion, so it is pinned as a test.
	e := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)
	opt := e.PriceBurst(DefaultInstances[1], 50, 24*time.Hour)
	if opt.SuccessProb < 0.5 || opt.SuccessProb > 0.8 {
		t.Errorf("50 instances for a day: success %.3f, want roughly 0.6", opt.SuccessProb)
	}
	if opt.CostUSD < 5000 || opt.CostUSD > 20000 {
		t.Errorf("cost $%.0f is outside the expected order of magnitude", opt.CostUSD)
	}
}

func TestFleetForOddsIsConsistentWithPriceBurst(t *testing.T) {
	e := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)
	inst := DefaultInstances[1]
	n := e.FleetForOdds(inst, 24*time.Hour, 0.9)
	if n <= 0 {
		t.Fatal("expected a workable fleet size for 90% odds in a day")
	}
	if got := e.PriceBurst(inst, n, 24*time.Hour).SuccessProb; got < 0.9 {
		t.Errorf("fleet of %d gave %.4f odds, want >= 0.90", n, got)
	}
	if got := e.PriceBurst(inst, n-1, 24*time.Hour).SuccessProb; got >= 0.9 {
		t.Errorf("fleet of %d already reaches %.4f, so %d is not minimal", n-1, got, n)
	}
}

// --- Alternatives --------------------------------------------------------------

// When the requested search is outright impossible, the user most needs a way
// forward — so a workable mode change must still be offered rather than skipped
// for failing to "improve on" an unbounded baseline.
func TestImpossiblePatternStillGetsAWayForward(t *testing.T) {
	alts := SuggestAlternatives("borderland", MatchSuffix, 22.2e6, 24*time.Hour)
	if len(alts) == 0 {
		t.Fatal("an impossible suffix should still yield a suggestion")
	}
	var found bool
	for _, a := range alts {
		if a.Mode == MatchAnywhere && a.Pattern == "borderland" {
			found = true
			if a.Estimate.Probability <= 0 {
				t.Error("the suggested alternative must itself be achievable")
			}
		}
	}
	if !found {
		t.Errorf("expected anywhere mode to be offered; got %+v", alts)
	}
}

func TestSuffixCompletions(t *testing.T) {
	got := SuffixCompletions("borderland")
	want := []string{"borderlandad", "borderlandid", "borderlandqd", "borderlandyd"}
	if len(got) != len(want) {
		t.Fatalf("got %d completions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("completion %d = %q, want %q", i, got[i], want[i])
		}
		if err := Validate(got[i], MatchSuffix); err != nil {
			t.Errorf("completion %q is not a usable suffix: %v", got[i], err)
		}
	}
}

// All four completions must be searched together. Any one of them satisfies the
// user equally, so searching a single ending is exactly 4x slower for no gain —
// a mistake worth pinning down.
func TestSuffixCompletionsMustBeSearchedTogether(t *testing.T) {
	comps := SuffixCompletions("borderland")
	all := NewEstimate(comps, MatchSuffix, 22.2e6)
	one := NewEstimate(comps[:1], MatchSuffix, 22.2e6)

	if ratio := one.ExpectedTries / all.ExpectedTries; math.Abs(ratio-4) > 0.01 {
		t.Errorf("searching one ending is %.2fx the work of all four, want 4x", ratio)
	}
	// Pinning the word immediately before a forced ending costs the same as
	// pinning it at the front: the same number of characters is being fixed.
	pre := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)
	if math.Abs(all.Bits-pre.Bits) > 1e-9 {
		t.Errorf("tail-completed suffix = %.2f bits, prefix = %.2f bits; expected equal",
			all.Bits, pre.Bits)
	}
}

func TestRejectedSuffixIsOfferedTailCompletion(t *testing.T) {
	alts := SuggestAlternatives("borderland", MatchSuffix, 22.2e6, 24*time.Hour)

	var found *Alternative
	for i := range alts {
		if alts[i].Kind == "tail-completed" {
			found = &alts[i]
		}
	}
	if found == nil {
		t.Fatalf("a suffix rejected for the tail rule should be offered a completion; got %+v", alts)
	}
	if len(found.Patterns) != 4 {
		t.Errorf("expected all 4 endings searched as an OR, got %v", found.Patterns)
	}
	for _, p := range found.Patterns {
		if err := Validate(p, MatchSuffix); err != nil {
			t.Errorf("offered unusable completion %q: %v", p, err)
		}
		if !strings.HasPrefix(p, "borderland") {
			t.Errorf("completion %q does not preserve the user's word", p)
		}
	}
	if found.Estimate.Probability <= 0 {
		t.Error("the offered completion must actually be achievable")
	}
}

// A word already valid as a suffix needs no completion.
func TestValidSuffixIsNotOfferedTailCompletion(t *testing.T) {
	for _, a := range SuggestAlternatives("borderlad", MatchSuffix, 22.2e6, 24*time.Hour) {
		if a.Kind == "tail-completed" {
			t.Errorf("borderlad is already a valid suffix; should not be offered %q", a.Pattern)
		}
	}
}

func TestSuggestAlternativesOffersAnywhereFirst(t *testing.T) {
	alts := SuggestAlternatives("borderland", MatchPrefix, 22.2e6, 30*24*time.Hour)
	if len(alts) == 0 {
		t.Fatal("expected suggestions for a hard prefix")
	}
	var foundMode bool
	for _, a := range alts {
		if a.Kind == "mode-change" && a.Mode == MatchAnywhere && a.Pattern == "borderland" {
			foundMode = true
		}
	}
	if !foundMode {
		t.Error("switching to anywhere mode keeps the whole word and should be offered")
	}
}

// Every suggestion must be valid, and the budget must be respected by any
// suggestion that sacrifices the pattern. A mode change keeps the user's whole
// word, so it is exempt from the budget but must instead be a real improvement —
// otherwise the tool would hide its single best option ("8 days, full word")
// behind a tighter budget and only offer shorter words.
func TestSuggestedAlternativesRespectTheirRules(t *testing.T) {
	budget := 24 * time.Hour
	original := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)

	var sawModeChange bool
	for _, a := range SuggestAlternatives("borderland", MatchPrefix, 22.2e6, budget) {
		if err := Validate(a.Pattern, a.Mode); err != nil {
			t.Errorf("suggested invalid pattern %q (%s): %v", a.Pattern, a.Mode, err)
		}
		switch a.Kind {
		case "mode-change":
			sawModeChange = true
			if a.Pattern != "borderland" {
				t.Errorf("a mode change must keep the pattern, got %q", a.Pattern)
			}
			if a.Estimate.P50 >= original.P50/2 {
				t.Errorf("mode change to %s only improves P50 from %v to %v; not worth offering",
					a.Mode, original.P50, a.Estimate.P50)
			}
		default:
			if a.Estimate.P50 > budget {
				t.Errorf("suggested %q with P50 %v, over the %v budget", a.Pattern, a.Estimate.P50, budget)
			}
		}
	}
	if !sawModeChange {
		t.Error("anywhere mode is a large win for a 10-char prefix and should be suggested")
	}
}

func TestDropVowels(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"borderland", 1, "borderlnd"},
		{"borderland", 2, "bordrlnd"}, // drops a then e, right to left
		{"borderland", 3, "brdrlnd"},
		{"borderland", 99, "brdrlnd"}, // never removes more vowels than exist
	}
	for _, c := range cases {
		if got := dropVowels(c.in, c.n); got != c.want {
			t.Errorf("dropVowels(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestParseMatchMode(t *testing.T) {
	for _, s := range []string{"prefix", "PREFIX", "suffix", "anywhere", ""} {
		if _, err := ParseMatchMode(s); err != nil {
			t.Errorf("ParseMatchMode(%q) failed: %v", s, err)
		}
	}
	if _, err := ParseMatchMode("middle"); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}

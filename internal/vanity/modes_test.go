package vanity

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompareModesRecommendsAnywhereForAnOrdinaryWord(t *testing.T) {
	cmp := CompareModes([]string{"borderx"}, 22.2e6)

	if cmp.Best != MatchAnywhere {
		t.Fatalf("want anywhere recommended for an ordinary word, got %q", cmp.Best)
	}
	for _, m := range allModes {
		if _, ok := cmp.Estimates[m]; !ok {
			t.Errorf("missing estimate for mode %q", m)
		}
	}
	// Anywhere must never be slower than prefix for the same word — it strictly
	// dominates, since a prefix hit is one of anywhere's own positions.
	if cmp.Estimates[MatchAnywhere].P50 > cmp.Estimates[MatchPrefix].P50 {
		t.Error("anywhere must not be slower than prefix for the same word")
	}
}

// borderland ends in "nd", which is not one of the four legal endings
// (ad/id/qd/yd), so it can never be a valid suffix — the exact bug this
// project's own history caught.
func TestCompareModesMarksImpossibleSuffix(t *testing.T) {
	cmp := CompareModes([]string{"borderland"}, 22.2e6)

	suffix := cmp.Estimates[MatchSuffix]
	if suffix.Probability > 0 {
		t.Fatal("borderland must be impossible as a suffix")
	}
	if cmp.Best == MatchSuffix {
		t.Error("an impossible mode must never be recommended")
	}
	if reason := suffixReason("borderland"); !strings.Contains(reason, "ad/id/qd/yd") {
		t.Errorf("suffixReason should list the legal endings, got %q", reason)
	}
	if reason := suffixReason("borderland"); !strings.Contains(reason, `"nd"`) {
		t.Errorf("suffixReason should name what this pattern actually ends in, got %q", reason)
	}
}

func TestSuffixReasonNamesTheTrailingCharacterRule(t *testing.T) {
	reason := suffixReason("border")
	if !strings.Contains(reason, "ad/id/qd/yd") {
		t.Errorf("border ends in 'r', not a legal ending — want the endings listed, got %q", reason)
	}
	if !strings.Contains(reason, `"er"`) {
		t.Errorf("want the actual offending ending named, got %q", reason)
	}
}

func TestSuffixReasonIsEmptyForAValidSuffix(t *testing.T) {
	// "ad" is a legal ending: 'd' is the fixed final char, 'a' is a legal
	// penultimate char.
	if reason := suffixReason("ad"); reason != "" {
		t.Errorf("valid suffix should have no rejection reason, got %q", reason)
	}
}

func TestWriteModeComparisonListsAllThreeModes(t *testing.T) {
	var b bytes.Buffer
	WriteModeComparison(&b, CompareModes([]string{"borderx"}, 22.2e6))
	out := b.String()

	for _, want := range []string{"prefix", "suffix", "anywhere", "recommended"} {
		if !strings.Contains(out, want) {
			t.Errorf("comparison is missing %q:\n%s", want, out)
		}
	}
}

// The impossible-suffix case must explain itself and offer the tail-completion
// fix, not just print "impossible" and stop.
func TestWriteModeComparisonExplainsImpossibleSuffix(t *testing.T) {
	var b bytes.Buffer
	WriteModeComparison(&b, CompareModes([]string{"borderland"}, 22.2e6))
	out := b.String()

	if !strings.Contains(out, "impossible") {
		t.Errorf("expected borderland's suffix row to say impossible:\n%s", out)
	}
	if !strings.Contains(out, "ad/id/qd/yd") {
		t.Errorf("expected the legal endings to be listed:\n%s", out)
	}
	if !strings.Contains(out, "borderlandad") {
		t.Errorf("expected a tail-completion suggestion:\n%s", out)
	}
}

func TestCompareModesHandlesMultiplePatterns(t *testing.T) {
	cmp := CompareModes([]string{"borderx", "bordery"}, 22.2e6)
	if cmp.Best == "" {
		t.Fatal("an ordinary multi-pattern search should have an achievable mode")
	}
	var b bytes.Buffer
	WriteModeComparison(&b, cmp)
	// Multi-pattern suffix explanations are skipped rather than guessed at, since
	// the two patterns could break different rules.
	if strings.Contains(b.String(), "ad/id/qd/yd") {
		t.Error("multi-pattern suffix rows should not print a single-pattern reason")
	}
}

package vanity

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func render(e Estimate, alts []Alternative) string {
	var b bytes.Buffer
	WriteReport(&b, e, alts, 24*time.Hour)
	return b.String()
}

// Accuracy requirement #7: the report must show its work, so a wrong constant is
// visible to the reader rather than hidden behind a verdict.
func TestReportShowsItsWork(t *testing.T) {
	out := render(NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6), nil)

	for _, want := range []string{
		"32^10",                 // exponent form
		"2^50",                  // bit form
		"1,125,899,906,842,624", // exact count, grouped
		"22.20 million keys/sec",
		"P50", "mean", "P90",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
	// The wrong value from the origin session must never appear for this pattern.
	if strings.Contains(out, "1,152,921,504,606,846,976") {
		t.Error("report shows 32^12; the 1024x error has regressed")
	}
}

func TestReportExplainsImpossibility(t *testing.T) {
	out := render(NewEstimate([]string{"border"}, MatchSuffix, 22.2e6), nil)

	if !strings.Contains(out, "IMPOSSIBLE") {
		t.Errorf("expected an explicit impossibility notice:\n%s", out)
	}
	// A bare "impossible" is not enough — it has to say why, and offer the way out.
	if !strings.Contains(out, "ends in") {
		t.Errorf("expected the reason (trailing character rule):\n%s", out)
	}
	if !strings.Contains(out, "anywhere") {
		t.Errorf("expected a suggested alternative mode:\n%s", out)
	}
}

func TestReportNeverPrintsNegativeOrWrappedTimes(t *testing.T) {
	// A 20-character prefix overflows Duration; the report must degrade to years
	// rather than print a wrapped negative value.
	out := render(NewEstimate([]string{"borderlandborderland"}, MatchPrefix, 22.2e6), nil)
	if strings.Contains(out, "-") && strings.Contains(out, "h0m0s") {
		t.Errorf("looks like a wrapped duration:\n%s", out)
	}
	if !strings.Contains(out, "years") {
		t.Errorf("expected huge values rendered in years:\n%s", out)
	}
}

func TestReportShowsCloudOptionsOnlyWhenRelevant(t *testing.T) {
	easy := render(NewEstimate([]string{"bord"}, MatchPrefix, 22.2e6), nil)
	if strings.Contains(easy, "Rented compute") {
		t.Error("a trivial search should not be pitching cloud instances")
	}

	hard := render(NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6), nil)
	if !strings.Contains(hard, "Rented compute") {
		t.Error("a hard search should price the cloud option instead of just saying no")
	}
}

// A prefix long enough to reach the protocol-fixed trailing characters is cheaper
// than 32^n, so the report must not print that exponent next to the real
// expected-try count — two numbers that disagree is worse than one.
func TestReportDoesNotClaim32PowNForTailSpanningPrefix(t *testing.T) {
	full := strings.Repeat("a", AddressLen-2) + "ad" // valid, spans the tail
	out := render(NewEstimate([]string{full}, MatchPrefix, 22.2e6), nil)
	if strings.Contains(out, "2^280") {
		t.Errorf("report claims the naive 32^56 space for a tail-spanning prefix:\n%s", out)
	}
	if !strings.Contains(out, "Difficulty:") {
		t.Errorf("expected a bits-based difficulty line instead:\n%s", out)
	}
}

func TestReportUsesBitsForNonPrefixModes(t *testing.T) {
	// 32^n is only the exact space for a prefix. Other modes must report bits
	// rather than print an exponent that does not describe them.
	out := render(NewEstimate([]string{"borderland"}, MatchAnywhere, 22.2e6), nil)
	if strings.Contains(out, "32^10") {
		t.Errorf("anywhere mode must not claim the prefix search space:\n%s", out)
	}
	if !strings.Contains(out, "Difficulty:") {
		t.Errorf("expected a bits-based difficulty line:\n%s", out)
	}
}

// Options are presented as a comparison the user makes, not a correction handed
// down. The requested search must appear alongside the rest, the trade-off axes
// must be visible, and the recommendation must be advisory.
func TestReportPresentsOptionsForTheUserToChoose(t *testing.T) {
	e := NewEstimate([]string{"borderland"}, MatchPrefix, 22.2e6)
	opts := Options("borderland", MatchPrefix, 22.2e6, 24*time.Hour)
	out := render(e, opts)

	for _, want := range []string{
		"Your options",
		"word appears",       // where the word lands
		"typical time",       // what it costs
		"what you asked for", // the request is in the table, not replaced
		"anywhere",           // the alternative placement
	} {
		if !strings.Contains(out, want) {
			t.Errorf("options table is missing %q:\n%s", want, out)
		}
	}

	// A recommendation may be offered, but must be framed as the user's call.
	if strings.Contains(out, "Recommended:") && !strings.Contains(out, "yours to run") {
		t.Errorf("a recommendation must stay advisory:\n%s", out)
	}
}

// The word "cheaper" must not frame the option list: tail-completion costs
// exactly as much as a prefix, so presenting every option as a discount
// misdescribes the trade-off and pushes the user toward speed they may not want.
func TestOptionsAreNotFramedPurelyAsCost(t *testing.T) {
	opts := Options("borderland", MatchSuffix, 22.2e6, 24*time.Hour)
	out := render(NewEstimate([]string{"borderland"}, MatchSuffix, 22.2e6), opts)
	if strings.Contains(out, "Cheaper alternatives") {
		t.Errorf("options should be framed as choices, not discounts:\n%s", out)
	}
}

func TestCommas(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "1"},
		{"999", "999"},
		{"1000", "1,000"},
		{"1125899906842624", "1,125,899,906,842,624"},
	}
	for _, c := range cases {
		if got := commas(c.in); got != c.want {
			t.Errorf("commas(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDurationUnits(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "under a second"},
		{1500 * time.Millisecond, "1 second"},
		{30 * time.Second, "30 seconds"},
		{90 * time.Second, "1.5 minutes"},
		{5 * time.Hour, "5.0 hours"},
		{10 * 24 * time.Hour, "10.0 days"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d, 0); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

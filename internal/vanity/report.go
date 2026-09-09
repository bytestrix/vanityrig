package vanity

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"
	"time"
)

// humanDuration renders a duration at a sensible unit, and falls back to years
// computed in float seconds once the value exceeds what a Duration can hold.
func humanDuration(d time.Duration, yearsFallback float64) string {
	if Saturated(d) {
		return humanYears(yearsFallback)
	}
	switch {
	case d < time.Second:
		return "under a second"
	case d < 2*time.Second:
		return "1 second"
	case d < time.Minute:
		return fmt.Sprintf("%.0f seconds", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.1f minutes", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	default:
		return humanYears(d.Hours() / 24 / 365.25)
	}
}

func humanYears(y float64) string {
	switch {
	case math.IsInf(y, 1):
		return "never"
	case y < 1e4:
		return fmt.Sprintf("%.1f years", y)
	case y < 1e15:
		return fmt.Sprintf("%s years", commas(big.NewFloat(y).Text('f', 0)))
	default:
		return fmt.Sprintf("%.2e years", y)
	}
}

// commas groups digits for readability: 1125899906842624 -> 1,125,899,906,842,624.
func commas(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func humanCount(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "infinite"
	case f < 1e6:
		return fmt.Sprintf("%.0f", f)
	case f < 1e9:
		return fmt.Sprintf("%.2f million", f/1e6)
	case f < 1e12:
		return fmt.Sprintf("%.2f billion", f/1e9)
	case f < 1e15:
		return fmt.Sprintf("%.2f trillion", f/1e12)
	case f < 1e18:
		return fmt.Sprintf("%.2f quadrillion", f/1e15)
	default:
		return fmt.Sprintf("%.3e", f)
	}
}

// WriteReport prints the feasibility report.
//
// The format deliberately shows its work — exponent form, exact count, assumed
// rate and derived times all appear together — so that a wrong constant is
// visible to the reader instead of hidden behind a confident verdict. That is
// accuracy requirement #7, and it exists because a hand-computed estimate in this
// project's origin session was wrong by 1024x and nobody could see it.
func WriteReport(w io.Writer, e Estimate, alts []Alternative, budget time.Duration) {
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

	label := strings.Join(e.Patterns, ", ")
	p("")
	p("Feasibility check: %q (%s match)", label, e.Mode)
	p("")

	if e.Probability <= 0 {
		// State the arithmetic first, same as any other verdict. The distinction
		// being drawn is not "very unlikely" but "the set of matching addresses is
		// empty" — worth making explicitly, because the two are easy to conflate
		// and only one of them can be beaten with more hardware.
		p("  Matching addresses:  0")
		p("  Probability:         exactly zero, at any throughput")
		p("")
		for _, pat := range e.Patterns {
			if err := Validate(pat, e.Mode); err != nil {
				p("  %s", wrapText(err.Error(), 68, "  "))
			}
		}
		p("")
		p("  Verdict: IMPOSSIBLE — this is not a long shot. No v3 onion address")
		p("  exists that satisfies it, so no amount of compute will find one.")
		p("  (An astronomically unlikely pattern is a different thing, and this")
		p("  tool will happily estimate one for you and let you run it.)")
		p("")
		writeAlternatives(w, alts)
		return
	}

	// Show the arithmetic, not just the conclusion.
	//
	// 32^n is the honest space only for a single prefix that stops short of the
	// protocol-fixed trailing characters. A prefix long enough to reach them is
	// cheaper than 32^n, so printing that exponent alongside the real expected-try
	// count would show the reader two numbers that disagree.
	n := 0
	if len(e.Patterns) == 1 {
		n = len([]rune(e.Patterns[0]))
	}
	if n > 0 && e.Mode == MatchPrefix && n <= AddressLen-2 {
		exact := PrefixSpace(n).String()
		if len(exact) > 25 {
			// Past ~25 digits the exact figure wraps the terminal and stops being
			// readable, which defeats the point of showing it.
			p("  Search space:    32^%d = 2^%d possibilities", n, BitsPerChar*n)
		} else {
			p("  Search space:    32^%d = 2^%d = %s possibilities",
				n, BitsPerChar*n, commas(exact))
		}
	} else {
		p("  Difficulty:      %.1f bits", e.Bits)
		p("  Expected tries:  %s", humanCount(e.ExpectedTries))
	}
	p("  Your throughput: %s keys/sec", humanCount(e.KeysPerSec))
	p("")
	p("  Expected time to find:")
	p("    P50 (coin flip):    %s", humanDuration(e.P50, e.YearsFor(0.5)))
	p("    mean:               %s", humanDuration(e.Mean, e.ExpectedTries/e.KeysPerSec/(365.25*24*3600)))
	p("    P90 (90%% by then):  %s", humanDuration(e.P90, e.YearsFor(0.9)))
	p("")
	p("  Verdict: %s — %s", strings.ToUpper(string(e.Verdict)), verdictAdvice(e.Verdict))

	if e.Verdict == VerdictExpensive || e.Verdict == VerdictHard || e.Verdict == VerdictInfeasible {
		writeCloudOptions(w, e)
	}
	writeAlternatives(w, alts)
}

func verdictAdvice(v Verdict) string {
	switch v {
	case VerdictTrivial:
		return "effectively instant, just run it"
	case VerdictReasonable:
		return "a normal search, go ahead"
	case VerdictExpensive:
		return "a long run — confirm before committing the machines"
	case VerdictHard:
		return "years on this hardware, but rentable compute can close the gap"
	default:
		return "beyond reach even with a large rented fleet"
	}
}

func writeCloudOptions(w io.Writer, e Estimate) {
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }
	day := 24 * time.Hour

	p("")
	p("  Rented compute (approximate on-demand list prices; Spot is typically")
	p("  much cheaper, and rates should be replaced by a calibration run):")

	any := false
	for _, inst := range DefaultInstances {
		n := e.FleetForOdds(inst, day, 0.5)
		if n <= 0 {
			continue
		}
		opt := e.PriceBurst(inst, n, day)
		p("    %-14s x%-5d for 24h  ->  %.0f%% chance   ~$%s",
			inst.Name, n, opt.SuccessProb*100, commas(fmt.Sprintf("%.0f", opt.CostUSD)))
		any = true
	}
	if !any {
		p("    no fleet size within reach gets to a coin flip in 24 hours")
	}
}

func writeAlternatives(w io.Writer, alts []Alternative) {
	if len(alts) == 0 {
		return
	}
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }
	recIdx, reason := Recommend(alts)

	p("")
	p("  Your options — pick whichever suits you:")
	p("")
	p("    %-24s %-18s %-15s %s", "search for", "word appears", "typical time", "word")
	p("    %s", strings.Repeat("─", 72))

	shown := 0
	for i, a := range alts {
		if shown >= 7 {
			break
		}
		marker := "  "
		if i == recIdx {
			marker = "→ "
		}
		word := "full"
		if !a.KeepsWord {
			word = "shortened"
		}
		timing := humanDuration(a.Estimate.P50, a.Estimate.YearsFor(0.5))
		if a.Estimate.Probability <= 0 {
			timing = "impossible"
		}
		note := ""
		switch {
		case a.Requested():
			note = "  (what you asked for)"
		case i == recIdx:
			note = "  ← recommended"
		}
		p("  %s%-24s %-18s %-15s %s%s", marker, a.Pattern, a.Placement, timing, word, note)
		shown++
	}

	if recIdx >= 0 && reason != "" {
		r := alts[recIdx]
		// Name the placement too: several rows can share a pattern and differ only
		// in where the word lands, so the pattern alone does not identify a row.
		p("")
		p("    Recommended: %s (%s) — %s.", r.Pattern, r.Placement, reason)
		p("    That is only a suggestion; any option above is yours to run.")
	}

	// A multi-pattern option needs explaining, since "search all four at once"
	// being 4x faster than picking one is not self-evident.
	for _, a := range alts {
		if len(a.Patterns) > 1 {
			p("")
			p("    %q searches all %d endings together:", a.Pattern, len(a.Patterns))
			p("      %s", strings.Join(a.Patterns, "  "))
			p("    Any one of them puts your word at the end. Searching them together")
			p("    is %dx faster than committing to a single ending.", len(a.Patterns))
			break
		}
	}
}

// wrapText hard-wraps s at width, indenting continuation lines.
func wrapText(s string, width int, indent string) string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, wd := range words {
		if cur == "" {
			cur = wd
			continue
		}
		if len(cur)+1+len(wd) > width {
			lines = append(lines, cur)
			cur = wd
			continue
		}
		cur += " " + wd
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n"+indent)
}

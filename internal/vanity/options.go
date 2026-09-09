package vanity

import "time"

// Options returns everything the user could reasonably do with their word,
// including the search they actually asked for, so the trade-offs can be compared
// side by side rather than presented as a correction.
//
// The tool's job here is to lay out choices and mark a recommendation, not to
// steer. Someone may knowingly take a slower option because it puts the word
// where they want it, and that is a legitimate preference rather than a mistake.
func Options(pattern string, mode MatchMode, keysPerSec float64, budget time.Duration) []Alternative {
	pattern = Normalize(pattern)

	requested := Alternative{
		Pattern:   pattern,
		Patterns:  []string{pattern},
		Mode:      mode,
		Kind:      "requested",
		Estimate:  NewEstimate([]string{pattern}, mode, keysPerSec),
		Placement: placementFor(mode, "requested"),
		KeepsWord: true,
	}

	return append([]Alternative{requested},
		SuggestAlternatives(pattern, mode, keysPerSec, budget)...)
}

// Recommend picks the option worth highlighting and explains why in one line.
// It returns -1 when nothing stands out, in which case no recommendation should
// be shown rather than one being manufactured.
//
// The ranking deliberately prefers keeping the user's whole word over raw speed:
// a vanity address exists to be recognisable, so an option that truncates the
// word is a worse outcome even when it finishes sooner.
func Recommend(opts []Alternative) (int, string) {
	if len(opts) == 0 {
		return -1, ""
	}

	var requested *Alternative
	for i := range opts {
		if opts[i].Requested() {
			requested = &opts[i]
			break
		}
	}

	best, bestIdx := (*Alternative)(nil), -1
	for i := range opts {
		o := &opts[i]
		if o.Requested() || o.Estimate.Probability <= 0 || !o.KeepsWord {
			continue
		}
		if best == nil || o.Estimate.P50 < best.Estimate.P50 {
			best, bestIdx = o, i
		}
	}

	// The requested search is impossible: any working option is the recommendation.
	if requested != nil && requested.Estimate.Probability <= 0 {
		if best != nil {
			return bestIdx, "the search you asked for is impossible, and this keeps your whole word"
		}
		return -1, ""
	}

	// Nothing beats what was asked for, or nothing else keeps the word intact.
	if best == nil || requested == nil {
		return -1, ""
	}
	if best.Estimate.P50 >= requested.Estimate.P50 {
		return -1, ""
	}

	// Only shout about a speedup large enough to change someone's mind.
	speedup := float64(requested.Estimate.P50) / float64(best.Estimate.P50)
	if Saturated(requested.Estimate.P50) {
		speedup = requested.Estimate.ExpectedTries / best.Estimate.ExpectedTries
	}
	if speedup < 3 {
		return -1, ""
	}
	return bestIdx, "same full word, much faster"
}

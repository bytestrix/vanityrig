package vanity

import (
	"math"
	"time"
)

// Verdict classifies how achievable a search is at a given throughput.
type Verdict string

const (
	VerdictTrivial    Verdict = "trivial"    // seconds — just run it
	VerdictReasonable Verdict = "reasonable" // up to a day — show estimate, run it
	VerdictExpensive  Verdict = "expensive"  // up to a year — confirm before starting
	VerdictHard       Verdict = "hard"       // years locally, but rentable compute can close it
	VerdictInfeasible Verdict = "infeasible" // beyond reach even with a large rented fleet
)

// referenceFleetTries is the yardstick for "infeasible": what a large but orderable
// rented fleet (100 large instances for 30 days) could actually get through. Beyond
// this, no ordinary budget closes the gap — which is a different and much stronger
// claim than "slow on your laptop", so it gets its own band.
const referenceFleetTries = 100 * 250e6 * 30 * 24 * 3600

// Estimate is a complete, self-auditing feasibility report. Every field needed to
// re-derive the conclusion by hand is present, because a verdict the user cannot
// check is worse than no verdict.
type Estimate struct {
	Patterns      []string
	Mode          MatchMode
	Probability   float64 // per-candidate chance of success
	Bits          float64 // -log2(Probability)
	ExpectedTries float64 // 1/Probability
	KeysPerSec    float64 // assumed throughput

	Mean time.Duration
	P50  time.Duration
	P90  time.Duration

	Verdict Verdict
}

// timeToProbability returns how long until the cumulative chance of success
// reaches p, for a Poisson process of rate lambda = perCandidate * keysPerSec.
func timeToProbability(perCandidate, keysPerSec, p float64) time.Duration {
	if perCandidate <= 0 || keysPerSec <= 0 {
		return time.Duration(math.MaxInt64)
	}
	lambda := perCandidate * keysPerSec
	seconds := -math.Log(1-p) / lambda
	return secondsToDuration(seconds)
}

// secondsToDuration converts to a Duration, saturating instead of overflowing.
// time.Duration is int64 nanoseconds, which tops out around 292 years — routinely
// exceeded by these searches, so saturation must be explicit rather than wrapping
// to a negative number.
func secondsToDuration(s float64) time.Duration {
	if math.IsInf(s, 1) || s >= float64(math.MaxInt64)/1e9 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(s * float64(time.Second))
}

// Saturated reports whether a duration hit the representable ceiling, meaning the
// real answer is "longer than roughly 292 years" and should be rendered in years
// from the raw seconds rather than printed as a Duration.
func Saturated(d time.Duration) bool {
	return d == time.Duration(math.MaxInt64)
}

// YearsFor returns the number of years to reach probability p, computed in float
// seconds so it stays meaningful past the Duration ceiling.
func (e Estimate) YearsFor(p float64) float64 {
	if e.Probability <= 0 || e.KeysPerSec <= 0 {
		return math.Inf(1)
	}
	lambda := e.Probability * e.KeysPerSec
	return (-math.Log(1-p) / lambda) / (365.25 * 24 * 3600)
}

// SuccessProbability returns the chance of at least one match after running at
// keysPerSec for the given duration.
func (e Estimate) SuccessProbability(keysPerSec float64, d time.Duration) float64 {
	tries := keysPerSec * d.Seconds()
	return 1 - math.Exp(-e.Probability*tries)
}

// NewEstimate builds a feasibility report for a set of patterns at a throughput.
func NewEstimate(patterns []string, mode MatchMode, keysPerSec float64) Estimate {
	norm := make([]string, 0, len(patterns))
	seen := map[string]bool{}
	for _, p := range patterns {
		n := Normalize(p)
		if n != "" && !seen[n] {
			seen[n] = true
			norm = append(norm, n)
		}
	}

	p := CombinedProbability(norm, mode)
	e := Estimate{
		Patterns:      norm,
		Mode:          mode,
		Probability:   p,
		Bits:          DifficultyBits(p),
		ExpectedTries: ExpectedTries(p),
		KeysPerSec:    keysPerSec,
	}
	if p > 0 && keysPerSec > 0 {
		e.Mean = secondsToDuration(e.ExpectedTries / keysPerSec)
		e.P50 = timeToProbability(p, keysPerSec, 0.5)
		e.P90 = timeToProbability(p, keysPerSec, 0.9)
	} else {
		e.Mean, e.P50, e.P90 = time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	}
	e.Verdict = classify(e)
	return e
}

func classify(e Estimate) Verdict {
	if e.Probability <= 0 {
		return VerdictInfeasible
	}
	if e.ExpectedTries > referenceFleetTries {
		return VerdictInfeasible
	}
	years := e.YearsFor(0.5)
	switch {
	case years*365.25*24*3600 < 60:
		return VerdictTrivial
	case years*365.25 < 1:
		return VerdictReasonable
	case years < 1:
		return VerdictExpensive
	default:
		return VerdictHard
	}
}

// Instance is a rentable machine profile used to price a compute burst.
//
// Rates and prices are STARTING DEFAULTS, deliberately conservative and clearly
// approximate. Real deployments should replace KeysPerSec with a measured
// calibration run (accuracy requirement #5) and USDPerHour with live pricing;
// on-demand list prices drift and Spot is typically far cheaper.
type Instance struct {
	Name       string
	VCPUs      int
	KeysPerSec float64
	USDPerHour float64
}

// DefaultInstances are rough profiles for pricing a burst. KeysPerSec assumes
// ~1.3M keys/sec/core, measured on a Zen-class core running mkp224o in prefix
// mode; other engines and match modes differ substantially.
var DefaultInstances = []Instance{
	{Name: "c7a.16xlarge", VCPUs: 64, KeysPerSec: 64 * 1.3e6, USDPerHour: 3.29},
	{Name: "c7a.48xlarge", VCPUs: 192, KeysPerSec: 192 * 1.3e6, USDPerHour: 9.86},
}

// CloudOption is a priced burst of rented compute and what it actually buys.
type CloudOption struct {
	Instance    Instance
	Count       int
	Duration    time.Duration
	TotalRate   float64
	SuccessProb float64
	CostUSD     float64
}

// PriceBurst evaluates renting count instances for a duration.
func (e Estimate) PriceBurst(inst Instance, count int, d time.Duration) CloudOption {
	rate := inst.KeysPerSec * float64(count)
	return CloudOption{
		Instance:    inst,
		Count:       count,
		Duration:    d,
		TotalRate:   rate,
		SuccessProb: e.SuccessProbability(rate, d),
		CostUSD:     inst.USDPerHour * float64(count) * d.Hours(),
	}
}

// FleetForOdds returns how many of inst are needed to reach the given success
// probability within d. Returns 0 if the target is unreachable at any fleet size
// the caller would plausibly rent.
func (e Estimate) FleetForOdds(inst Instance, d time.Duration, odds float64) int {
	if e.Probability <= 0 || odds <= 0 || odds >= 1 {
		return 0
	}
	// need: 1-exp(-p * rate * seconds) >= odds
	triesNeeded := -math.Log(1-odds) / e.Probability
	perInstance := inst.KeysPerSec * d.Seconds()
	if perInstance <= 0 {
		return 0
	}
	n := math.Ceil(triesNeeded / perInstance)
	if n > 1e6 || math.IsInf(n, 1) {
		return 0
	}
	return int(n)
}

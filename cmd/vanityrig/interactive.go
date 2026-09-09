package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// askWord prompts for one or more words to search for, as a real text input
// field rather than a bare "type and press enter" line. A huh.Run error
// (including Ctrl-C/Esc) is treated as "cancelled" rather than a failure.
func askWord() ([]string, bool) {
	var input string
	field := huh.NewInput().
		Title("Word to search for").
		Description("Space-separated words match any one of them.").
		Placeholder("e.g. myproject").
		Value(&input).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("enter at least one word")
			}
			return nil
		})

	if err := field.Run(); err != nil {
		return nil, false
	}
	return strings.Fields(input), true
}

// askOutputDir prompts for where found keys should be saved. An empty answer
// means "use the default", exactly like leaving -out unset on the command line.
func askOutputDir() (string, bool) {
	var out string
	field := huh.NewInput().
		Title("Save found keys to").
		Placeholder("~/.vanityrig/keys (default)").
		Value(&out)

	if err := field.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// askMode presents the achievable match modes as a select list — the actual
// radio-button-style picker this replaces a typed "prefix"/"suffix"/"anywhere"
// answer with. Impossible modes are left off the list entirely; the
// comparison table already printed above explains why.
func askMode(cmp vanity.ModeComparison) (vanity.MatchMode, bool) {
	var opts []huh.Option[vanity.MatchMode]
	for _, m := range []vanity.MatchMode{vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere} {
		if cmp.Estimates[m].Probability <= 0 {
			continue
		}
		label := string(m)
		if m == cmp.Best {
			label += "  (recommended)"
		}
		opts = append(opts, huh.NewOption(label, m).Selected(m == cmp.Best))
	}

	chosen := cmp.Best
	field := huh.NewSelect[vanity.MatchMode]().
		Title(fmt.Sprintf("Where should %q appear in the address?", strings.Join(cmp.Patterns, ", "))).
		Options(opts...).
		Value(&chosen)

	if err := field.Run(); err != nil {
		return "", false
	}
	return chosen, true
}

// askProceed is the long-search gate: shown only when the chosen search will
// tie up hardware for a while, defaulting to "no" so committing real time is
// always a deliberate choice.
func askProceed(v vanity.Verdict) bool {
	description := "Start it anyway?"
	if v == vanity.VerdictInfeasible {
		description = "This isn't just slow — it's beyond what ordinary hardware or a\nrentable fleet can realistically close. Start it anyway?"
	}

	proceed := false
	field := huh.NewConfirm().
		Title("This search will take a long time.").
		Description(description).
		Affirmative("Start").
		Negative("Not now").
		Value(&proceed)

	if err := field.Run(); err != nil {
		return false
	}
	return proceed
}

// askStart is the ordinary "go ahead?" gate for a quick or reasonable search,
// defaulting to yes since there's nothing risky to weigh.
func askStart() bool {
	proceed := true
	field := huh.NewConfirm().
		Title("Start the search now?").
		Value(&proceed)

	if err := field.Run(); err != nil {
		return false
	}
	return proceed
}

package main

import (
	"bytes"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/bytestrix/vanityrig/internal/vanity"
)

// wizardRate is the throughput assumed for on-the-fly estimates while the
// guided form is still open — before a search exists to measure a real one.
const wizardRate = 22.2e6

// modeOptions lists the achievable match modes for cmp as select options,
// marking the recommended one. Impossible modes are left off entirely — the
// comparison text printed alongside already explains why.
func modeOptions(cmp vanity.ModeComparison) []huh.Option[vanity.MatchMode] {
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
	return opts
}

// comparisonText renders the mode table for embedding as a field's
// description, with the leading "Where should ... appear?" line stripped —
// the select field's own title already asks that.
func comparisonText(cmp vanity.ModeComparison) string {
	var b bytes.Buffer
	vanity.WriteModeComparison(&b, cmp)
	s := strings.TrimSpace(b.String())
	if i := strings.Index(s, "  mode"); i >= 0 {
		s = s[i:]
	}
	return s
}

// longSearchNote describes the chosen mode's estimate when it's worth a
// second thought before committing hardware to it; empty otherwise.
func longSearchNote(cmp vanity.ModeComparison, m vanity.MatchMode) string {
	switch cmp.Estimates[m].Verdict {
	case vanity.VerdictExpensive, vanity.VerdictHard, vanity.VerdictInfeasible:
		return "This will take a long time on your hardware."
	default:
		return ""
	}
}

// runWizardForm is the guided setup for a bare `vanityrig`: one continuous
// full-screen form — word, save location, mode (with live cost comparison),
// confirm — rather than a sequence of separate prompts. Returns ok=false if
// the user cancelled or declined at the final step.
func runWizardForm() (patterns []string, out string, mode vanity.MatchMode, ok bool) {
	var word string
	proceed := true

	wordField := huh.NewInput().
		Title("Word to search for").
		Description("Space-separated words match any one of them.").
		Placeholder("e.g. myproject").
		Value(&word).
		Validate(func(s string) error {
			fields := strings.Fields(s)
			if len(fields) == 0 {
				return fmt.Errorf("enter at least one word")
			}
			for _, p := range fields {
				if err := vanity.PreflightSyntax(p); err != nil {
					return err
				}
			}
			return nil
		})

	outField := huh.NewInput().
		Title("Save found keys to").
		Placeholder("~/.vanityrig/keys (default)").
		Value(&out)

	modeField := huh.NewSelect[vanity.MatchMode]().
		TitleFunc(func() string {
			return fmt.Sprintf("Where should %q appear in the address?", strings.Join(strings.Fields(word), ", "))
		}, &word).
		DescriptionFunc(func() string {
			return comparisonText(vanity.CompareModes(strings.Fields(word), wizardRate))
		}, &word).
		OptionsFunc(func() []huh.Option[vanity.MatchMode] {
			return modeOptions(vanity.CompareModes(strings.Fields(word), wizardRate))
		}, &word).
		Value(&mode)

	confirmField := huh.NewConfirm().
		TitleFunc(func() string {
			return fmt.Sprintf("Start the %s search now?", mode)
		}, &mode).
		DescriptionFunc(func() string {
			return longSearchNote(vanity.CompareModes(strings.Fields(word), wizardRate), mode)
		}, &mode).
		Affirmative("Start").
		Negative("Not now").
		Value(&proceed)

	// One group, not one-per-field: every field renders on the same screen at
	// once (Tab/Shift-Tab to move between them) instead of each answer paging
	// to a new screen and losing sight of the last one.
	form := huh.NewForm(
		huh.NewGroup(wordField, outField, modeField, confirmField),
	).WithProgramOptions(tea.WithAltScreen())

	if err := form.Run(); err != nil {
		return nil, "", "", false
	}
	if !proceed {
		return nil, "", "", false
	}
	return strings.Fields(word), strings.TrimSpace(out), mode, true
}

// askModeAndConfirm is the command-line-argument counterpart to the wizard's
// mode+confirm steps: the word is already known, so it's one screen (pick a
// mode, then confirm) instead of separate prompts.
func askModeAndConfirm(cmp vanity.ModeComparison) (mode vanity.MatchMode, ok bool) {
	chosen := cmp.Best
	proceed := true

	modeField := huh.NewSelect[vanity.MatchMode]().
		Title(fmt.Sprintf("Where should %q appear in the address?", strings.Join(cmp.Patterns, ", "))).
		Description(comparisonText(cmp)).
		Options(modeOptions(cmp)...).
		Value(&chosen)

	confirmField := huh.NewConfirm().
		TitleFunc(func() string {
			return fmt.Sprintf("Start the %s search now?", chosen)
		}, &chosen).
		DescriptionFunc(func() string {
			return longSearchNote(cmp, chosen)
		}, &chosen).
		Affirmative("Start").
		Negative("Not now").
		Value(&proceed)

	form := huh.NewForm(
		huh.NewGroup(modeField, confirmField),
	).WithProgramOptions(tea.WithAltScreen())

	if err := form.Run(); err != nil {
		return "", false
	}
	return chosen, proceed
}

// askConfirmStart is the final gate for an explicit `-match` search: a
// single-field full-screen confirm, defaulting to yes for anything ordinary
// and to no for anything that will tie up hardware for a long time.
func askConfirmStart(v vanity.Verdict) bool {
	defaultYes := v == vanity.VerdictTrivial || v == vanity.VerdictReasonable
	proceed := defaultYes

	description := ""
	if !defaultYes {
		description = "This will take a long time on your hardware."
	}

	field := huh.NewConfirm().
		Title("Start the search now?").
		Description(description).
		Affirmative("Start").
		Negative("Not now").
		Value(&proceed)

	form := huh.NewForm(huh.NewGroup(field)).WithProgramOptions(tea.WithAltScreen())
	if err := form.Run(); err != nil {
		return false
	}
	return proceed
}

package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bytestrix/vanityrig/internal/tui"
	"github.com/bytestrix/vanityrig/internal/vanity"
)

// runInteractive drives the combined settings+log dashboard: a Settings
// panel (word, save location, match mode, CPU share) and a Log/Status panel
// are both on screen from the first frame, rather than one question at a
// time. Whatever the caller already knows (from CLI args/flags) comes in
// pre-filled but still editable; mode is locked only when it was given
// explicitly via -match.
func runInteractive(patterns []string, out string, mode vanity.MatchMode, rate float64, threads, stopAfter int, enginePath string) int {
	m := tui.NewSetup(tui.SetupConfig{
		Words:      patterns,
		OutDir:     out,
		Mode:       mode,
		Threads:    threads,
		Rate:       rate,
		StopAfter:  stopAfter,
		EnginePath: enginePath,
		Version:    version,
	})

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard unavailable:", err)
		return 1
	}

	if snap, started := m.Snapshot(); started {
		printSummary(snap)
		return 0
	}
	fmt.Println("Not starting.")
	return 0
}

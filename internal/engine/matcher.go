package engine

import (
	"fmt"
	"strings"

	"github.com/rishibaghel25/vanityrig/internal/vanity"
)

// matcher tests a generated address against the search patterns.
//
// It exists so the hot loop does one cheap string test per candidate, with the
// mode resolved once up front rather than re-branched on every key.
type matcher struct {
	patterns []string
	mode     vanity.MatchMode
}

func newMatcher(patterns []string, mode vanity.MatchMode) (*matcher, error) {
	norm := make([]string, 0, len(patterns))
	seen := map[string]bool{}
	for _, p := range patterns {
		n := vanity.Normalize(p)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		norm = append(norm, n)
	}
	if len(norm) == 0 {
		return nil, fmt.Errorf("no usable patterns")
	}
	switch mode {
	case vanity.MatchPrefix, vanity.MatchSuffix, vanity.MatchAnywhere:
	default:
		return nil, fmt.Errorf("unsupported match mode %q", mode)
	}
	return &matcher{patterns: norm, mode: mode}, nil
}

// matches reports whether addr satisfies any pattern. Multiple patterns are an
// OR: any one of them is a win.
func (m *matcher) matches(addr string) bool {
	for _, p := range m.patterns {
		switch m.mode {
		case vanity.MatchPrefix:
			if strings.HasPrefix(addr, p) {
				return true
			}
		case vanity.MatchSuffix:
			if strings.HasSuffix(addr, p) {
				return true
			}
		default:
			if strings.Contains(addr, p) {
				return true
			}
		}
	}
	return false
}

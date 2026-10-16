// The skip ledger: what a run skipped, grouped by why.
package gate

import (
	"fmt"
	"sort"
	"strings"
)

// SkipEntry is one thing a run skipped and why, for Ledger.
type SkipEntry struct {
	// Name is what was skipped, as the ledger prints it.
	Name string
	// Reason is why.
	Reason string
}

// Ledger formats skips for a person: grouped by reason, largest group
// first, naming up to limit entries in each and counting the rest. noun
// names what was skipped ("test(s)", "check(s)"). It returns "" when
// nothing was skipped, since a skipped gate and a passing one used to print
// the same nothing.
func Ledger(noun string, skips []SkipEntry, limit int) string {
	if len(skips) == 0 {
		return ""
	}
	groups := map[string][]SkipEntry{}
	for _, s := range skips {
		groups[s.Reason] = append(groups[s.Reason], s)
	}
	reasons := make([]string, 0, len(groups))
	for r := range groups {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(a, b int) bool {
		if len(groups[reasons[a]]) != len(groups[reasons[b]]) {
			return len(groups[reasons[a]]) > len(groups[reasons[b]])
		}
		return reasons[a] < reasons[b]
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d %s skipped, so they are not evidence of anything in this run:\n", len(skips), noun)
	for _, r := range reasons {
		g := groups[r]
		fmt.Fprintf(&b, "\n  %d: %s\n", len(g), r)
		for i, s := range g {
			if i == limit {
				fmt.Fprintf(&b, "      and %d more\n", len(g)-limit)
				break
			}
			fmt.Fprintf(&b, "      %s\n", s.Name)
		}
	}
	return b.String()
}

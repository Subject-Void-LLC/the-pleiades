// Which of a manifest's checks a run selects, and why the rest are skipped.
package gate

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Heavy filters for Selection.Heavy.
const (
	HeavyInclude = "include"
	HeavyExclude = "exclude"
	HeavyOnly    = "only"
)

// Selection says which checks a run asks for. A zero Selection selects
// every check.
type Selection struct {
	// Kinds selects checks of these kinds; every kind when empty.
	Kinds []string
	// Checks selects checks by name; every check the kinds select when
	// empty.
	Checks []string
	// Heavy is HeavyInclude (the default when empty), HeavyExclude or
	// HeavyOnly.
	Heavy string
}

// Planned is one check of a run's plan: the check, and whether the run
// selected it, with the reason when it did not.
type Planned struct {
	Check    Check
	Timeout  Duration
	Selected bool
	Reason   string
}

// Plan applies sel to m, in m's order. A kind or check name m does not
// have is refused, and so is a selection that runs nothing: a gate that
// runs nothing must not read as one that passed.
func (m *Manifest) Plan(sel Selection) ([]Planned, error) {
	kinds, err := m.kindSet()
	if err != nil {
		return nil, err
	}
	for _, k := range sel.Kinds {
		if !kinds[k] {
			return nil, fmt.Errorf("kinds: %q is not a kind this manifest has", k)
		}
	}
	names := map[string]bool{}
	for _, c := range m.Checks {
		names[c.Name] = true
	}
	for _, n := range sel.Checks {
		if !names[n] {
			return nil, fmt.Errorf("checks: %q is not a check in this manifest", n)
		}
	}
	heavy := sel.Heavy
	if heavy == "" {
		heavy = HeavyInclude
	}
	if heavy != HeavyInclude && heavy != HeavyExclude && heavy != HeavyOnly {
		return nil, fmt.Errorf("heavy %q: it is include, exclude or only", sel.Heavy)
	}

	plan := make([]Planned, 0, len(m.Checks))
	selected := 0
	for _, c := range m.Checks {
		p := Planned{Check: c, Timeout: Duration(m.TimeoutOf(c)), Selected: true}
		switch {
		case len(sel.Kinds) > 0 && !slices.Contains(sel.Kinds, c.Kind):
			p.Selected, p.Reason = false, fmt.Sprintf("its kind, %s, is not among the kinds this run selected (%s)", c.Kind, strings.Join(sel.Kinds, ", "))
		case len(sel.Checks) > 0 && !slices.Contains(sel.Checks, c.Name):
			p.Selected, p.Reason = false, "this run selected other checks by name"
		case heavy == HeavyExclude && c.Heavy:
			p.Selected, p.Reason = false, "it is heavy, and this run excluded heavy checks"
		case heavy == HeavyOnly && !c.Heavy:
			p.Selected, p.Reason = false, "it is not heavy, and this run selected only heavy checks"
		}
		if p.Selected {
			selected++
		}
		plan = append(plan, p)
	}
	if selected == 0 {
		return nil, errors.New("the selection runs no check: a gate that runs nothing would read as one that passed")
	}
	return plan, nil
}

// Parallelism is how many checks a run starts at once: asked when it is
// positive, the manifest's default otherwise, and 1 when neither says;
// never more than MaxParallelism.
func (m *Manifest) Parallelism(asked int) int {
	p := asked
	if p <= 0 {
		p = m.Defaults.Parallelism
	}
	return min(max(p, 1), MaxParallelism)
}

// Package wait: wait.routes's state parameter, and what state absent waits
// for: a route withdrawn, or moved off the next hops named.
package wait

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// routesState reads wait.routes's state: present (the default) or absent,
// case ignored, and reports whether it is absent. Any other word is refused
// naming both, before the router is reached.
func routesState(params map[string]any) (bool, error) {
	if v, ok := params[waitParamState]; ok && v != nil {
		if _, isText := v.(string); !isText {
			return false, fmt.Errorf("%s: %s must be %s or %s, not %T", routesFQCN, waitParamState, waitStatePresent, waitStateAbsent, v)
		}
	}
	switch v := strings.ToLower(strings.TrimSpace(sdk.StringParam(params, waitParamState))); v {
	case "", waitStatePresent:
		return false, nil
	case waitStateAbsent:
		return true, nil
	default:
		return false, fmt.Errorf("%s: %s %q is neither %s nor %s", routesFQCN, waitParamState, v, waitStatePresent, waitStateAbsent)
	}
}

// gone is satisfied's opposite, for state absent. A prefix with no selected
// route is gone. A prefix next_hops names is gone once its selected route
// uses none of those next hops, whether or not another route took its
// place. It returns each prefix with the next hops its route still uses (an
// empty list when it has none) and, for each prefix not yet gone, what is
// still there.
func (w routesWant) gone(table map[string][]route) (map[string][]string, []string) {
	used := map[string][]string{}
	var remaining []string
	for _, p := range w.prefixes {
		hops, found := selectedHops(table[p])
		if hops == nil {
			hops = []string{}
		}
		used[p] = hops
		named := w.nextHops[p]
		switch {
		case !found:
			// No selected route at all: gone, whatever next_hops says.
		case len(named) == 0:
			remaining = append(remaining, fmt.Sprintf("to %s (through %s)", p, strings.Join(hops, ", ")))
		default:
			for _, h := range named {
				if slices.Contains(hops, h) {
					remaining = append(remaining, fmt.Sprintf("to %s through %s", p, h))
				}
			}
		}
	}
	return used, remaining
}

// selectedHops is whether routes holds a selected route, and the active
// next hops the selected ones use: an address, or the interface of a
// connected route.
func selectedHops(routes []route) ([]string, bool) {
	var hops []string
	found := false
	for _, r := range routes {
		if !r.Selected {
			continue
		}
		found = true
		for _, h := range r.NextHops {
			if !h.Active {
				continue
			}
			if h.IP != "" {
				hops = append(hops, h.IP)
			} else {
				hops = append(hops, h.Interface)
			}
		}
	}
	return hops, found
}

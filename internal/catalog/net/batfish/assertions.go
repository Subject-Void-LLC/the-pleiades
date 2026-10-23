// Package batfish: reading net.batfish.check's assertions.
package batfish

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// readAssertions reads a list of assertions, each a map of one kind.
func readAssertions(params map[string]any, key string, nodes map[string]bool) ([]assertion, error) {
	list, err := mapList(params, key)
	if err != nil {
		return nil, err
	}
	var out []assertion
	for i, item := range list {
		if len(item) != 1 {
			return nil, fmt.Errorf("%s[%d] must name one kind of assertion", key, i)
		}
		for kind, v := range item {
			a, err := readAssertion(kind, v, nodes)
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", key, i, err)
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// readAssertion reads one assertion of kind.
func readAssertion(kind string, v any, nodes map[string]bool) (assertion, error) {
	a := assertion{kind: kind}
	if kind == kindOSPF || kind == kindBGP {
		if b, ok := v.(bool); !ok || !b {
			return a, fmt.Errorf("%s takes true", kind)
		}
		return a, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return a, fmt.Errorf("%s takes a map", kind)
	}
	allowed := map[string][]string{
		kindRoute:       {"node", "prefix", "next_hops", "protocol"},
		kindNoRoute:     {"node", "prefix"},
		kindReach:       {"from", "dst", "delivered", "through", "avoids", "accepted_by"},
		kindRoutesMatch: {"node", "routes", "ignore"},
	}[kind]
	if allowed == nil {
		return a, fmt.Errorf("no assertion %q: one of %s, %s, %s, %s, %s or %s", kind, kindOSPF, kindBGP, kindRoute, kindNoRoute, kindReach, kindRoutesMatch)
	}
	for k := range m {
		if !slices.Contains(allowed, k) {
			return a, fmt.Errorf("%s takes no %q", kind, k)
		}
	}
	node := func(key string) (string, error) {
		n, _ := m[key].(string)
		if !nodes[strings.ToLower(n)] {
			return "", fmt.Errorf("%s.%s must name a device given, got %q", kind, key, n)
		}
		return strings.ToLower(n), nil
	}
	var err error
	switch kind {
	case kindRoute, kindNoRoute:
		if a.node, err = node("node"); err != nil {
			return a, err
		}
		p, _ := m["prefix"].(string)
		pfx, perr := netip.ParsePrefix(p)
		if perr != nil {
			return a, fmt.Errorf("%s.prefix must be a prefix, got %q", kind, p)
		}
		a.prefix = pfx.Masked().String()
		a.protocol, _ = m["protocol"].(string)
		hops, _, err := sdk.StringSlice(m, "next_hops")
		if err != nil {
			return a, err
		}
		for _, h := range hops {
			addr, err := netip.ParseAddr(h)
			if err != nil {
				return a, fmt.Errorf("%s.next_hops: %q is not an address", kind, h)
			}
			a.nextHops = append(a.nextHops, addr.String())
		}
	case kindReach:
		from, _ := m["from"].(string)
		if mm := interfaceRef.FindStringSubmatch(from); mm != nil && nodes[strings.ToLower(mm[1])] {
			a.from = "@enter(" + strings.ToLower(mm[1]) + "[" + mm[2] + "])"
		} else if nodes[strings.ToLower(from)] {
			a.from = strings.ToLower(from)
		} else {
			return a, fmt.Errorf("reach.from must be a device given or node[interface], got %q", from)
		}
		dst, _ := m["dst"].(string)
		addr, err := netip.ParseAddr(dst)
		if err != nil {
			return a, fmt.Errorf("reach.dst must be an address, got %q", dst)
		}
		a.dst = addr.String()
		a.delivered = true
		if d, ok := m["delivered"]; ok {
			b, ok := d.(bool)
			if !ok {
				return a, fmt.Errorf("reach.delivered must be true or false")
			}
			a.delivered = b
		}
		if by, present := m["accepted_by"]; present {
			name, _ := by.(string)
			if !nodes[strings.ToLower(name)] || !a.delivered {
				return a, fmt.Errorf("reach.accepted_by must name a device given, on a flow that is delivered")
			}
			a.acceptedBy = strings.ToLower(name)
		}
		for key, into := range map[string]*[]string{"through": &a.through, "avoids": &a.avoids} {
			list, _, err := sdk.StringSlice(m, key)
			if err != nil {
				return a, err
			}
			for _, n := range list {
				if !nodes[strings.ToLower(n)] {
					return a, fmt.Errorf("reach.%s: %q is not a device given", key, n)
				}
				*into = append(*into, strings.ToLower(n))
			}
		}
	case kindRoutesMatch:
		if a.node, err = node("node"); err != nil {
			return a, err
		}
		a.liveRoutes, ok = m["routes"].(map[string]any)
		if !ok || len(a.liveRoutes) == 0 {
			return a, fmt.Errorf("routes_match.routes must be net.frr.facts' routes for %s", a.node)
		}
		ignore, _, err := sdk.StringSlice(m, "ignore")
		if err != nil {
			return a, err
		}
		for _, p := range ignore {
			pfx, err := netip.ParsePrefix(p)
			if err != nil {
				return a, fmt.Errorf("routes_match.ignore: %q is not a prefix", p)
			}
			a.ignore = append(a.ignore, pfx.Masked().String())
		}
	}
	return a, nil
}

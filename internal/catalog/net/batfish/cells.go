// Package batfish: reading the cells of Batfish's answers.
package batfish

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// fileDevice is the device a snapshot file holds: snapshot/configs/r1.cfg
// is r1.
func fileDevice(name string) string {
	return strings.TrimSuffix(path.Base(name), ".cfg")
}

// nextHopIP reads a route row's next-hop address, in either shape Batfish
// answers with.
func nextHopIP(r map[string]any) string {
	if nh, ok := r["Next_Hop"].(map[string]any); ok {
		if ip := str(nh["ip"]); ip != "" && ip != "AUTO/NONE(-1l)" {
			return ip
		}
	}
	if ip := str(r["Next_Hop_IP"]); ip != "" && ip != "AUTO/NONE(-1l)" {
		return ip
	}
	return ""
}

// traceNodes returns the nodes a trace passes, in order.
func traceNodes(trace map[string]any) []string {
	hops, _ := trace["hops"].([]any)
	var out []string
	for _, h := range hops {
		hop, _ := h.(map[string]any)
		node, _ := hop["node"].(map[string]any)
		out = append(out, strings.ToLower(str(node["name"])))
	}
	return out
}

// cellName renders a cell naming an interface ({hostname, interface}), a
// node ({name}) or an address.
func cellName(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case map[string]any:
		if n := str(c["name"]); n != "" {
			return n
		}
		return str(c["hostname"]) + "[" + str(c["interface"]) + "]"
	}
	return fmt.Sprint(v)
}

// withoutSecret is a line with its masked secret removed, for matching an
// acceptance written without one.
func withoutSecret(line string) string {
	var kept []string
	for _, w := range strings.Fields(line) {
		if !strings.HasPrefix(w, netcfg.MaskPrefix) {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, " ")
}

// protocolFamily is a route's protocol as FRR names it: Batfish splits
// OSPF by route type, BGP by session type, IS-IS by level and EIGRP by
// origin.
func protocolFamily(p string) string {
	switch {
	case strings.HasPrefix(p, "ospf"):
		return "ospf"
	case p == "ibgp" || p == "bgp" || p == "aggregate":
		return "bgp"
	case strings.HasPrefix(p, "isis"):
		return "isis"
	case strings.HasPrefix(p, "eigrp"):
		return "eigrp"
	}
	return p
}

// str reads a cell as text.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// num reads a cell as a whole number.
func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// sameSet reports whether a and b hold the same keys.
func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// keys renders a set, sorted.
func keys(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return orNone(out)
}

// orNone joins list, or says none.
func orNone(list []string) string {
	if len(list) == 0 {
		return "no next hop"
	}
	return strings.Join(list, ", ")
}

// dedupe drops repeated entries, keeping order.
func dedupe(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

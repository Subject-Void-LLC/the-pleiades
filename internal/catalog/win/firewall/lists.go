// win.firewall.rule's lists: profiles, ports and addresses, read and put in
// the one form a call's and a rule's are compared in.
package firewall

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// stringList reads a list of strings, or one string of comma-separated
// entries, or a number, as a list. The string form is what an undo writes,
// since the run journal keeps only single values; no port, address or
// profile holds a comma.
func stringList(params map[string]any, name string) ([]string, error) {
	var raw []any
	switch v := params[name].(type) {
	case []any:
		raw = v
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			raw = append(raw, s)
		}
	case int, int64, float64:
		raw = []any{v}
	default:
		return nil, fmt.Errorf("%s is a list", name)
	}
	if len(raw) == 0 || len(raw) > 1000 {
		return nil, fmt.Errorf("%s holds 1 to 1000 entries", name)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		switch v.(type) {
		case string, int, int64, float64:
			out[i] = strings.TrimSpace(fmt.Sprint(v))
		default:
			return nil, fmt.Errorf("%s's entries are strings or numbers", name)
		}
	}
	return out, nil
}

// profileSet is a list of profiles as a sorted set; any, or all three,
// is the set of all three.
func profileSet(list []string) ([]string, error) {
	var set []string
	for _, p := range list {
		p = strings.ToLower(p)
		switch {
		case p == "any" && len(list) > 1:
			return nil, fmt.Errorf("%s holds any beside other profiles", fwProfiles)
		case p == "any":
			return slices.Clone(profileNames), nil
		case !slices.Contains(profileNames, p):
			return nil, fmt.Errorf("%s %q is domain, private, public or any", fwProfiles, p)
		case !slices.Contains(set, p):
			set = append(set, p)
		}
	}
	slices.Sort(set)
	return set, nil
}

// canonicalList is a port or address list in the form compared: lower
// case, an IPv4 network as a prefix whatever form it was written in, and
// sorted; "any" alone stands for every value.
func canonicalList(field string, list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, v := range list {
		c, err := canonicalEntry(field, v)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	if slices.Contains(out, "any") && len(out) > 1 {
		return nil, fmt.Errorf("%s holds any beside other entries", field)
	}
	slices.Sort(out)
	return out, nil
}

// canonicalEntry is one port or address in canonical form.
func canonicalEntry(field, v string) (string, error) {
	low := strings.ToLower(v)
	if low == "any" || low == "*" {
		return "any", nil
	}
	if field == fwLocalPort || field == fwRemotePort {
		if field == fwLocalPort && slices.Contains(portKeywords, low) {
			return low, nil
		}
		lo, hi, isRange := strings.Cut(low, "-")
		a, errA := strconv.Atoi(lo)
		b := a
		var errB error
		if isRange {
			b, errB = strconv.Atoi(hi)
		}
		if errA != nil || errB != nil || a < 1 || b > 65535 || a > b {
			return "", fmt.Errorf("%s %q is not a port, a range of ports (8000-8100) or any", field, v)
		}
		if isRange {
			return fmt.Sprintf("%d-%d", a, b), nil
		}
		return strconv.Itoa(a), nil
	}
	if slices.Contains(addressKeywords, low) {
		return low, nil
	}
	if lo, hi, isRange := strings.Cut(v, "-"); isRange {
		a, errA := netip.ParseAddr(lo)
		b, errB := netip.ParseAddr(hi)
		if errA != nil || errB != nil || a.Is4() != b.Is4() || b.Less(a) {
			return "", fmt.Errorf("%s %q is not an address range", field, v)
		}
		return a.String() + "-" + b.String(), nil
	}
	if addr, mask, isNet := strings.Cut(v, "/"); isNet {
		a, err := netip.ParseAddr(addr)
		if err != nil {
			return "", fmt.Errorf("%s %q is not a network", field, v)
		}
		bits, err := strconv.Atoi(mask)
		if err != nil {
			// Windows answers an IPv4 network with a dotted mask.
			m, merr := netip.ParseAddr(mask)
			if merr != nil || !m.Is4() || !a.Is4() {
				return "", fmt.Errorf("%s %q is not a network", field, v)
			}
			bits = maskBits(m.As4())
			if bits < 0 {
				return "", fmt.Errorf("%s %q has a mask that is not contiguous", field, v)
			}
		}
		p, err := a.Prefix(bits)
		if err != nil {
			return "", fmt.Errorf("%s %q is not a network", field, v)
		}
		if p.Bits() == a.BitLen() {
			return p.Addr().String(), nil
		}
		return p.String(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return "", fmt.Errorf("%s %q is not an address, network, range, keyword or any", field, v)
	}
	return a.String(), nil
}

// maskBits is a dotted IPv4 mask's prefix length, or -1 when its ones are
// not contiguous.
func maskBits(m [4]byte) int {
	n := uint32(m[0])<<24 | uint32(m[1])<<16 | uint32(m[2])<<8 | uint32(m[3])
	bits := 0
	for n&0x80000000 != 0 {
		bits++
		n <<= 1
	}
	if n != 0 {
		return -1
	}
	return bits
}

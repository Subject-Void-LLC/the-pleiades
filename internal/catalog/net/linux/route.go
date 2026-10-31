// net.linux.route: a route in the main routing table.
package linux

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// route is one routing-table entry as ip route show prints it.
type route struct {
	dest   string
	via    string
	dev    string
	metric int
}

// canonDest writes a destination one way: default stays default, an address
// without a prefix length is a host route, and a prefix is as netip prints it.
func canonDest(s string) (string, bool) {
	if s == "default" {
		return s, true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()).String(), true
	}
	return "", false
}

// routes returns the main table's routes of one address family.
func (s *session) routes(ctx context.Context, v6 bool) ([]route, error) {
	args := []string{"route", "show"}
	if v6 {
		args = []string{"-6", "route", "show"}
	}
	out, err := s.ip(ctx, args...)
	if err != nil {
		return nil, err
	}
	var table []route
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		dest, ok := canonDest(f[0])
		if !ok {
			continue
		}
		r := route{dest: dest}
		for i := 1; i+1 < len(f); i++ {
			switch f[i] {
			case "via":
				r.via = f[i+1]
			case "dev":
				r.dev = f[i+1]
			case "metric":
				r.metric, _ = strconv.Atoi(f[i+1])
			}
		}
		table = append(table, r)
	}
	return table, nil
}

// args are the words that name the route to ip.
func (r route) args() []string {
	a := []string{r.dest}
	if r.via != "" {
		a = append(a, "via", r.via)
	}
	if r.dev != "" {
		a = append(a, "dev", r.dev)
	}
	if r.metric != 0 {
		a = append(a, "metric", strconv.Itoa(r.metric))
	}
	return a
}

// params are the route as a method call's parameters.
func (r route) params(state string) map[string]any {
	m := map[string]any{"destination": r.dest, "state": state}
	if r.via != "" {
		m["via"] = r.via
	}
	if r.dev != "" {
		m["dev"] = r.dev
	}
	if r.metric != 0 {
		m["metric"] = r.metric
	}
	return m
}

// routeRun makes a route present in the main table, or absent.
func routeRun(ctx context.Context, rc sdk.RunbookContext, s *session, p map[string]any, check bool) (bool, error) {
	destText, err := textParam(p, "destination")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	dest, ok := canonDest(destText)
	if !ok {
		return false, fmt.Errorf("%s: destination %q is not a prefix (10.2.2.0/24) or default", s.fqcn, destText)
	}
	via, err := textParam(p, "via")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	if via != "" {
		if _, err := netip.ParseAddr(via); err != nil {
			return false, fmt.Errorf("%s: via %q is not an address", s.fqcn, via)
		}
	}
	dev := ""
	if _, set := p["dev"]; set {
		if dev, err = iface(p, "dev"); err != nil {
			return false, fmt.Errorf("%s: %w", s.fqcn, err)
		}
	}
	metric, _, err := intParam(p, "metric")
	if err != nil || metric < 0 {
		return false, fmt.Errorf("%s: metric must be a whole number, zero or more", s.fqcn)
	}
	present, err := wantPresent(p)
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	if present && via == "" && dev == "" {
		return false, fmt.Errorf("%s: set via, dev or both: a route needs somewhere to go", s.fqcn)
	}
	v6 := dest == "default" && strings.Contains(via, ":")
	if dest != "default" {
		pre, _ := netip.ParsePrefix(dest)
		v6 = pre.Addr().Is6()
	}

	table, err := s.routes(ctx, v6)
	if err != nil {
		return false, err
	}
	var found *route
	for i := range table {
		if table[i].dest == dest && table[i].metric == metric {
			found = &table[i]
			break
		}
	}
	record := func(is bool) error { return rc.SetStat("present", is) }
	want := route{dest: dest, via: via, dev: dev, metric: metric}

	if !present {
		if found == nil {
			return false, record(false)
		}
		if check {
			return true, record(false)
		}
		if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: s.fqcn, Params: found.params("present"), KeepOnFailure: true,
			Description: "Adds the route to " + dest + " back as it was."}); err != nil {
			return false, err
		}
		del := append([]string{"route", "del"}, found.args()...)
		if v6 {
			del = append([]string{"-6"}, del...)
		}
		_, err := s.ip(ctx, del...)
		return true, errOr(err, record(false))
	}

	if found != nil && (via == "" || found.via == via) && (dev == "" || found.dev == dev) {
		return false, record(true)
	}
	if check {
		return true, record(true)
	}
	verb := "add"
	undo := sdk.Inverse{FQCN: s.fqcn, Params: route{dest: dest, metric: metric}.params("absent"), KeepOnFailure: true, Description: "Removes the route to " + dest + ", which this run added."}
	if found != nil {
		verb = "replace"
		undo = sdk.Inverse{FQCN: s.fqcn, Params: found.params("present"), KeepOnFailure: true, Description: "Puts the route to " + dest + " back as it was."}
	} else {
		undo.Params = want.params("absent")
	}
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return false, err
	}
	add := append([]string{"route", verb}, want.args()...)
	if v6 {
		add = append([]string{"-6"}, add...)
	}
	_, err = s.ip(ctx, add...)
	return true, errOr(err, record(true))
}

// errOr returns first when it is an error, else second.
func errOr(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

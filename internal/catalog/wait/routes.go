// Package wait: wait.routes, waiting for a router's routing table to hold
// the routes a change needs and to stop changing.
package wait

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// routesFQCN is wait.routes's name.
const routesFQCN = "wait.routes"

// routesPoll is how often the table is read.
const routesPoll = time.Second

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: routesFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "This method reads the routing table and changes nothing, so there is nothing to undo.",
			},
			NoCheckReason: "what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: collection.Doc{
				Summary:     "Waits until a router has a route to every prefix given and its routing table has stopped changing.",
				Description: "A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. At timeout it fails naming each route still missing, or saying the table never held still. It changes nothing.",
				Params: []collection.Param{
					{Name: "prefixes", Type: "list of string", Required: true, Description: "The IPv4 prefixes that must each have a selected route, e.g. 10.3.3.0/24; the IPv4 routing table is the one read."},
					{Name: "next_hops", Type: "dict", Description: "Per prefix, the next-hop addresses its route must use, e.g. {\"10.3.3.0/24\": [\"10.0.12.1\"]}."},
					{Name: "stable_for", Type: "int", Default: "5", Description: "Seconds the routing table must stay unchanged."},
					{Name: "timeout", Type: "int", Default: "120", Description: "Seconds to wait in all."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "waited", Type: "float", Returned: "always", Description: "Seconds waited."},
					{Name: "routes", Type: "dict", Returned: "always", Description: "Each prefix given, with the next hops its route used."},
				},
				Examples: []collection.Example{
					{Name: "Wait for OSPF to settle", RunbookYAML: "- name: Every router routes both LANs\n  wait.routes:\n    prefixes: [10.1.1.0/24, 10.3.3.0/24]\n"},
				},
				SeeAlso: []string{"net.frr.config", "net.trace"},
			},
		},
		Invoke: Routes,
	})
}

// routesWant is what the task waits for.
type routesWant struct {
	prefixes []string
	nextHops map[string][]string
	stable   time.Duration
	timeout  time.Duration
}

// Routes waits until the router selects a route to every prefix given,
// through the next hops given, and its table has held still for
// stable_for seconds.
func Routes(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	want, err := routesParams(params)
	if err != nil {
		return collection.Result{}, err
	}
	conn, err := sdk.Connect(ctx, rc, device, params, routesFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	start := time.Now()
	deadline := start.Add(want.timeout)
	var last string
	var stableSince time.Time
	var missing []string
	for {
		table, err := readRoutes(ctx, conn)
		if err != nil {
			return collection.Result{}, err
		}
		now := time.Now()
		if fp := fingerprint(table); fp != last {
			last, stableSince = fp, now
		}
		var used map[string][]string
		used, missing = want.satisfied(table)
		if len(missing) == 0 && now.Sub(stableSince) >= want.stable {
			if err := rc.SetStat("waited", now.Sub(start).Seconds()); err != nil {
				return collection.Result{}, err
			}
			if err := rc.SetStat("routes", used); err != nil {
				return collection.Result{}, err
			}
			// What it found, in both halves, as every wait records.
			return collection.Result{}, sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{"routes": used}))
		}
		if now.After(deadline) {
			if len(missing) > 0 {
				return collection.Result{}, fmt.Errorf("%s: after %s the router still has no selected route %s", routesFQCN, want.timeout, strings.Join(missing, "; "))
			}
			return collection.Result{}, fmt.Errorf("%s: after %s the routing table had not held still for %s (it last changed %s ago)", routesFQCN, want.timeout, want.stable, now.Sub(stableSince).Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return collection.Result{}, ctx.Err()
		case <-time.After(routesPoll):
		}
	}
}

// routesParams reads and checks the task's parameters before the router
// is reached.
func routesParams(params map[string]any) (routesWant, error) {
	var w routesWant
	raw, ok, err := sdk.StringSlice(params, "prefixes")
	if err != nil {
		return w, fmt.Errorf("%s: prefixes: %w", routesFQCN, err)
	}
	if !ok || len(raw) == 0 {
		return w, fmt.Errorf("%s: prefixes must name at least one prefix", routesFQCN)
	}
	for _, p := range raw {
		pfx, err := netip.ParsePrefix(p)
		if err != nil {
			return w, fmt.Errorf("%s: prefixes: %q is not a prefix: %w", routesFQCN, p, err)
		}
		if !pfx.Addr().Is4() {
			// show ip route json holds IPv4 routes only, so a wait for an
			// IPv6 prefix could only time out.
			return w, fmt.Errorf("%s: prefixes: %q is IPv6, and this method reads the IPv4 routing table only", routesFQCN, p)
		}
		w.prefixes = append(w.prefixes, pfx.Masked().String())
	}
	w.nextHops = map[string][]string{}
	if v, ok := params["next_hops"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return w, fmt.Errorf("%s: next_hops must map prefixes to lists of addresses", routesFQCN)
		}
		for p, hops := range m {
			pfx, err := netip.ParsePrefix(p)
			if err != nil || !slices.Contains(w.prefixes, pfx.Masked().String()) {
				return w, fmt.Errorf("%s: next_hops names %q, which is not one of prefixes", routesFQCN, p)
			}
			list, _, err := sdk.StringSlice(map[string]any{"h": hops}, "h")
			if err != nil || len(list) == 0 {
				return w, fmt.Errorf("%s: next_hops[%s] must be a list of addresses", routesFQCN, p)
			}
			for _, h := range list {
				a, err := netip.ParseAddr(h)
				if err != nil {
					return w, fmt.Errorf("%s: next_hops[%s]: %q is not an address", routesFQCN, p, h)
				}
				w.nextHops[pfx.Masked().String()] = append(w.nextHops[pfx.Masked().String()], a.String())
			}
		}
	}
	stable, ok, err := sdk.IntParam(params, "stable_for")
	if err != nil {
		return w, err
	}
	if !ok {
		stable = 5
	}
	timeout, ok, err := sdk.IntParam(params, "timeout")
	if err != nil {
		return w, err
	}
	if !ok {
		timeout = 120
	}
	if stable < 0 || timeout < 1 || stable >= timeout {
		return w, fmt.Errorf("%s: stable_for must be at least 0 and less than timeout, which must be at least 1", routesFQCN)
	}
	w.stable, w.timeout = time.Duration(stable)*time.Second, time.Duration(timeout)*time.Second
	return w, nil
}

// route is the part of one of FRR's route entries this method reads.
type route struct {
	Protocol string    `json:"protocol"`
	Selected bool      `json:"selected"`
	Distance int       `json:"distance"`
	Metric   int       `json:"metric"`
	NextHops []nextHop `json:"nexthops"`
}

// nextHop is one of a route's next hops.
type nextHop struct {
	IP        string `json:"ip,omitempty"`
	Interface string `json:"interfaceName,omitempty"`
	Active    bool   `json:"active"`
	FIB       bool   `json:"fib,omitempty"`
}

// readRoutes reads show ip route json.
func readRoutes(ctx context.Context, conn *remoteexec.Conn) (map[string][]route, error) {
	res, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"vtysh", "-c", "show ip route json"}))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the routing table: %w", routesFQCN, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s: vtysh exited %d: %s", routesFQCN, res.ExitCode, deviceText(res.Stderr+res.Stdout))
	}
	var table map[string][]route
	if err := json.Unmarshal([]byte(res.Stdout), &table); err != nil {
		return nil, fmt.Errorf("%s: the routing table is not JSON: %w", routesFQCN, err)
	}
	return table, nil
}

// fingerprint is the table without what changes while nothing does (an
// uptime, an internal group id), so two reads compare equal exactly when
// the routes did not move.
func fingerprint(table map[string][]route) string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		for _, r := range table[k] {
			fmt.Fprintf(&b, "|%s,%t,%d,%d", r.Protocol, r.Selected, r.Distance, r.Metric)
			for _, h := range r.NextHops {
				fmt.Fprintf(&b, ";%s,%s,%t,%t", h.IP, h.Interface, h.Active, h.FIB)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// satisfied returns, for each prefix wanted, the next hops its selected
// route uses (an address, or the interface of a connected route), and
// what is still missing.
func (w routesWant) satisfied(table map[string][]route) (map[string][]string, []string) {
	used := map[string][]string{}
	var missing []string
	for _, p := range w.prefixes {
		var hops []string
		found := false
		for _, r := range table[p] {
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
		if !found {
			missing = append(missing, "to "+p)
			continue
		}
		used[p] = hops
		for _, h := range w.nextHops[p] {
			if !slices.Contains(hops, h) {
				missing = append(missing, fmt.Sprintf("to %s through %s (it uses %s)", p, h, strings.Join(hops, ", ")))
			}
		}
	}
	return used, missing
}

// deviceText is a device's words fit for an error: trimmed, at most 300
// bytes, quoted so a control character is shown rather than acted on.
func deviceText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return strconv.Quote(text)
}

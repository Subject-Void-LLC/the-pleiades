// Package wait: wait.routes, waiting for a router's routing table to hold
// the routes a change needs and to stop changing: FRR's, read through vtysh,
// or Cisco IOS's, read through its CLI and pkg/iosparse.
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
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
			// FRR is read over SSH, Cisco IOS over whatever reaches its CLI.
			// The capability is decided per device (routeReader): a device
			// that is neither is refused before it is contacted.
			SupportedTransports:  []string{capability.TransportSSH, capability.TransportTelnet, capability.TransportConsole},
			RequiredCapabilities: []capability.Name{},
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
				Description: "A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. At timeout it fails naming each route still missing, or saying the table never held still. It changes nothing. It reads an FRR router's table through vtysh (show ip route json, over SSH, the device granted FRRCapable and CommandExecCapable) and a Cisco IOS router's global table through its CLI (show ip route, over SSH, the controller's console or Telnet with insecure_telnet), and refuses any other device before contacting it. That refusal is made when the task runs: no single capability names both, so validate and a Controller's admission cannot hold the device to it beforehand.",
				Params: []collection.Param{
					{Name: "prefixes", Type: "list of string", Required: true, Description: "The IPv4 prefixes that must each have a selected route, e.g. 10.3.3.0/24; the IPv4 routing table is the one read."},
					{Name: "next_hops", Type: "dict", Description: "Per prefix, the next-hop addresses its route must use, e.g. {\"10.3.3.0/24\": [\"10.0.12.1\"]}."},
					{Name: "stable_for", Type: "int", Default: "5", Description: "Seconds the routing table must stay unchanged."},
					{Name: "timeout", Type: "int", Default: "120", Description: "Seconds to wait in all."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
					{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a Cisco IOS device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
				},
				Returns: []collection.ReturnField{
					{Name: "waited", Type: "float", Returned: "always", Description: "Seconds waited."},
					{Name: "routes", Type: "dict", Returned: "always", Description: "Each prefix given, with the next hops its route used."},
				},
				Examples: []collection.Example{
					{Name: "Wait for OSPF to settle", RunbookYAML: "- name: Every router routes both LANs\n  wait.routes:\n    prefixes: [10.1.1.0/24, 10.3.3.0/24]\n"},
					{Name: "Wait for a branch to learn the data center over its tunnel", RunbookYAML: "- name: BR1 routes the data center through the hub\n  wait.routes:\n    target: BR1\n    prefixes: [10.0.0.0/16]\n    next_hops: {10.0.0.0/16: [10.255.0.1]}\n    timeout: 180\n"},
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
	readRoutes, closeReader, err := routeReader(ctx, rc, device, params)
	if err != nil {
		return collection.Result{}, err
	}
	defer closeReader()

	start := time.Now()
	deadline := start.Add(want.timeout)
	var last string
	var stableSince time.Time
	var missing []string
	for {
		table, err := readRoutes(ctx)
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

// routeReader returns how to read device's routing table, and how to close
// what it opened: FRR's through vtysh over SSH, or Cisco IOS's through its
// CLI. A device that is neither is refused before anything is opened.
func routeReader(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (func(context.Context) (map[string][]route, error), func(), error) {
	switch {
	case device != nil && device.HasCapability(capability.NameFRR) && device.HasCapability(capability.NameCommandExec):
		conn, err := sdk.Connect(ctx, rc, device, params, routesFQCN)
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context) (map[string][]route, error) { return readFRRRoutes(ctx, conn) }, func() { _ = conn.Close() }, nil
	case device != nil && device.HasCapability(capability.NameCiscoIOS):
		session, err := sdk.OpenCLI(ctx, rc, device, params, routesFQCN, netcli.IOS)
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context) (map[string][]route, error) { return readIOSRoutes(ctx, session) }, func() { _ = session.Close() }, nil
	case device == nil:
		return nil, nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", routesFQCN)
	}
	return nil, nil, fmt.Errorf("%s: device %q is neither an FRR router (FRRCapable and CommandExecCapable) nor a Cisco IOS one (CiscoIOSCapable), whose routing tables this method reads", routesFQCN, device.Name())
}

// readIOSRoutes reads a Cisco IOS router's global table, show ip route, as
// pkg/iosparse parses it, into the shape FRR's JSON takes: every route IOS
// lists is one it installed, so each is selected.
func readIOSRoutes(ctx context.Context, session *netcli.Session) (map[string][]route, error) {
	out, err := session.Command(ctx, "show ip route")
	if err != nil {
		return nil, fmt.Errorf("%s: reading the routing table: %w", routesFQCN, err)
	}
	_, parsed, err := iosparse.Parse("show ip route", out)
	if err != nil {
		// What was read, bounded and masked, says why far better than the
		// parser can.
		return nil, fmt.Errorf("%s: the routing table did not parse: %w (it read %s)", routesFQCN, err, deviceText(netcfg.Mask(out)))
	}
	return iosRoutes(parsed), nil
}

// iosRoutes is iosparse's show ip route, the default VRF's IPv4 routes, as
// FRR's route shape.
func iosRoutes(parsed map[string]any) map[string][]route {
	table := map[string][]route{}
	routes := dig(parsed, "vrf", "default", "address_family", "ipv4", "routes")
	for prefix, v := range routes {
		entry, _ := v.(map[string]any)
		r := route{Protocol: text(entry["source_protocol"]), Selected: true, Distance: number(entry["route_preference"]), Metric: number(entry["metric"])}
		hops, _ := entry["next_hop"].(map[string]any)
		list := dig(hops, "next_hop_list")
		indexes := make([]string, 0, len(list))
		for i := range list {
			indexes = append(indexes, i)
		}
		sort.Slice(indexes, func(a, b int) bool { return number(indexes[a]) < number(indexes[b]) })
		for _, i := range indexes {
			hop, _ := list[i].(map[string]any)
			r.NextHops = append(r.NextHops, nextHop{IP: text(hop["next_hop"]), Interface: text(hop["outgoing_interface"]), Active: true})
		}
		names := make([]string, 0)
		for name := range dig(hops, "outgoing_interface") {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			r.NextHops = append(r.NextHops, nextHop{Interface: name, Active: true})
		}
		table[prefix] = append(table[prefix], r)
	}
	return table
}

// dig follows keys through nested maps, returning the map at the end or
// nil when one is missing.
func dig(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}

// text is v as a string, or empty.
func text(v any) string {
	s, _ := v.(string)
	return s
}

// number is v as an int: a parsed number of any width, or digits.
func number(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

// readFRRRoutes reads show ip route json.
func readFRRRoutes(ctx context.Context, conn *remoteexec.Conn) (map[string][]route, error) {
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

// Package batfish: what the model's answers say, and the assertions held
// to them.
package batfish

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	bf "github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// model is one run's network on the service.
type model struct {
	client  *bf.Client
	network string
}

// syntactic are the lines that mean nothing of their own, which Batfish
// reports unrecognised in an FRR file and nobody need accept.
var syntactic = map[string]bool{"exit": true, "end": true, "!": true}

// parsedStatuses are the file statuses a model can stand on. Anything else
// (FAILED, EMPTY, UNKNOWN, IGNORED) means a device is missing from it.
var parsedStatuses = map[string]bool{"PASSED": true, "PARTIALLY_UNRECOGNIZED": true}

// consistency reads how Batfish parsed each file: each file's status,
// every unrecognised line with the reason it was accepted, what the model
// therefore does not model, and what refuses the check.
func (m *model) consistency(ctx context.Context, req request) (parse, unrecognized []any, notModelled []string, refused []string, err error) {
	status, err := m.ask(ctx, baseSnapshot, "fileParseStatus", nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	seen := map[string]bool{}
	for _, r := range status.Rows {
		device := fileDevice(str(r["File_Name"]))
		s := str(r["Status"])
		seen[device] = true
		parse = append(parse, map[string]any{"device": device, "status": s})
		if !parsedStatuses[s] {
			refused = append(refused, fmt.Sprintf("Batfish could not parse %s (%s)", device, s))
		}
	}
	for _, d := range req.devices {
		if !seen[d.name] {
			refused = append(refused, fmt.Sprintf("Batfish reported nothing for %s", d.name))
		}
	}
	warnings, err := m.ask(ctx, baseSnapshot, "parseWarning", nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, r := range warnings.Rows {
		device := fileDevice(str(r["Filename"]))
		text := strings.TrimSpace(str(r["Text"]))
		line := num(r["Line"])
		entry := map[string]any{"device": device, "line": line, "text": text}
		reason, ok := req.accept[text]
		if !ok {
			reason, ok = req.accept[withoutSecret(text)]
		}
		switch {
		case syntactic[text]:
			entry["accepted"] = "it has no meaning of its own"
		case ok:
			entry["accepted"] = reason
			notModelled = append(notModelled, fmt.Sprintf("%s line %d: %s (%s)", device, line, text, reason))
		default:
			refused = append(refused, fmt.Sprintf("%s line %d (%q) was not recognised by the model, and accept_unrecognized does not accept it", device, line, text))
		}
		unrecognized = append(unrecognized, entry)
	}
	for _, d := range req.devices {
		uses := unmodelledIn(d.format, d.config)
		features := make([]string, 0, len(uses))
		for f := range uses {
			features = append(features, f)
		}
		sort.Strings(features)
		for _, f := range features {
			use := uses[f]
			text := netcfg.Mask(use.text)
			if reason, ok := req.acceptFeature[f]; ok {
				notModelled = append(notModelled, fmt.Sprintf("%s uses %s (line %d: %s): %s (%s)", d.name, f, use.line, text, coverageWhy(f), reason))
				continue
			}
			refused = append(refused, fmt.Sprintf("%s uses %s (line %d: %q), which the model does not model: %s; accept it by name in accept_unrecognized with a reason", d.name, f, use.line, text, coverageWhy(f)))
		}
	}
	issues, err := m.ask(ctx, baseSnapshot, "initIssues", nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, r := range issues.Rows {
		t, details := str(r["Type"]), strings.TrimSpace(str(r["Details"]))
		if t == "Parse warning" {
			continue
		}
		// A convert error or red flag means part of a device is missing
		// from the model (a route map that did not convert), so the gate
		// cannot stand on it unaccepted (the branch review's F12).
		lower := strings.ToLower(t)
		if strings.Contains(lower, "error") || strings.Contains(lower, "redflag") {
			if reason, ok := req.accept[details]; ok {
				notModelled = append(notModelled, fmt.Sprintf("%s: %s (%s)", t, details, reason))
			} else {
				refused = append(refused, fmt.Sprintf("Batfish reports %s: %q, so part of the network is not in the model; accept it by that text in accept_unrecognized with a reason", t, details))
			}
			continue
		}
		notModelled = append(notModelled, fmt.Sprintf("%s: %s", t, details))
	}
	if parse == nil {
		parse = []any{}
	}
	if unrecognized == nil {
		unrecognized = []any{}
	}
	if notModelled == nil {
		notModelled = []string{}
	}
	return parse, unrecognized, notModelled, refused, nil
}

// result is one assertion's outcome.
type result struct {
	scenario, kind, detail string
	passed                 bool
}

// row is the result as the task returns it.
func (r result) row() map[string]any {
	scenario := r.scenario
	if scenario == "" {
		scenario = "as given"
	}
	return map[string]any{"scenario": scenario, "kind": r.kind, "passed": r.passed, "detail": r.detail}
}

// String is the result in a sentence.
func (r result) String() string {
	if r.scenario == "" {
		return r.kind + ": " + r.detail
	}
	return r.scenario + ": " + r.kind + ": " + r.detail
}

// holdAll holds snapshot to each assertion.
func (m *model) holdAll(ctx context.Context, snapshot, scenario string, assertions []assertion) ([]result, error) {
	var out []result
	for _, a := range assertions {
		passed, detail, err := m.hold(ctx, snapshot, a)
		if err != nil {
			return nil, err
		}
		out = append(out, result{scenario: scenario, kind: a.kind, passed: passed, detail: detail})
	}
	return out, nil
}

// hold holds snapshot to one assertion.
func (m *model) hold(ctx context.Context, snapshot string, a assertion) (bool, string, error) {
	switch a.kind {
	case kindOSPF:
		return m.holdSessions(ctx, snapshot, "OSPF", "ospfSessionCompatibility", "Session_Status", "Interface", "Remote_Interface")
	case kindBGP:
		return m.holdSessions(ctx, snapshot, "BGP", "bgpSessionStatus", "Established_Status", "Node", "Remote_IP")
	case kindRoute, kindNoRoute:
		return m.holdRoute(ctx, snapshot, a)
	case kindReach:
		return m.holdReach(ctx, snapshot, a)
	case kindRoutesMatch:
		return m.holdRoutesMatch(ctx, snapshot, a)
	case kindQuestion:
		return m.holdQuestion(ctx, snapshot, a)
	}
	return false, "", fmt.Errorf("%s: no assertion %q", checkFQCN, a.kind)
}

// holdSessions: every session of a protocol the model finds (a row of
// question, its status in column) is established, and there is one.
func (m *model) holdSessions(ctx context.Context, snapshot, protocol, question, column, local, remote string) (bool, string, error) {
	t, err := m.ask(ctx, snapshot, question, nil)
	if err != nil {
		return false, "", err
	}
	if len(t.Rows) == 0 {
		return false, "the model has no " + protocol + " session", nil
	}
	var bad []string
	for _, r := range t.Rows {
		if s := str(r[column]); s != "ESTABLISHED" {
			bad = append(bad, fmt.Sprintf("%s to %s is %s", cellName(r[local]), cellName(r[remote]), s))
		}
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, ", "), nil
	}
	return true, fmt.Sprintf("%d %s sessions established", len(t.Rows), protocol), nil
}

// holdRoute: node has (or, for no_route, has not) a route to prefix,
// through the next hops and protocol named.
func (m *model) holdRoute(ctx context.Context, snapshot string, a assertion) (bool, string, error) {
	vars := map[string]any{"nodes": a.node, "network": a.prefix}
	if a.vrf != "" {
		vars["vrfs"] = a.vrf
	}
	t, err := m.ask(ctx, snapshot, "routes", vars)
	if err != nil {
		return false, "", err
	}
	node := a.node
	if a.vrf != "" {
		node += " (VRF " + a.vrf + ")"
	}
	var hops, protocols []string
	for _, r := range t.Rows {
		if str(r["Network"]) != a.prefix || (a.vrf != "" && str(r["VRF"]) != a.vrf) {
			continue
		}
		protocols = append(protocols, protocolFamily(str(r["Protocol"])))
		if ip := nextHopIP(r); ip != "" {
			hops = append(hops, ip)
		}
	}
	if a.kind == kindNoRoute {
		if len(protocols) > 0 {
			return false, fmt.Sprintf("%s routes %s (%s)", node, a.prefix, strings.Join(protocols, ", ")), nil
		}
		return true, fmt.Sprintf("%s has no route to %s", node, a.prefix), nil
	}
	if len(protocols) == 0 {
		return false, fmt.Sprintf("%s has no route to %s", node, a.prefix), nil
	}
	if a.protocol != "" && !slices.Contains(protocols, a.protocol) {
		return false, fmt.Sprintf("%s routes %s by %s, not %s", node, a.prefix, strings.Join(protocols, ", "), a.protocol), nil
	}
	for _, h := range a.nextHops {
		if !slices.Contains(hops, h) {
			return false, fmt.Sprintf("%s routes %s through %s, not %s", node, a.prefix, orNone(hops), h), nil
		}
	}
	return true, fmt.Sprintf("%s routes %s by %s through %s", node, a.prefix, strings.Join(protocols, ", "), orNone(hops)), nil
}

// deliveredDispositions are the trace endings that count as delivered.
var deliveredDispositions = map[string]bool{"ACCEPTED": true, "DELIVERED_TO_SUBNET": true, "EXITS_NETWORK": true}

// holdReach: a flow from a.from to a.dst is delivered (or not), and every
// path goes through the nodes named and around the ones to avoid.
func (m *model) holdReach(ctx context.Context, snapshot string, a assertion) (bool, string, error) {
	t, err := m.ask(ctx, snapshot, "traceroute", map[string]any{"startLocation": a.from, "headers": map[string]any{"dstIps": a.dst}})
	if err != nil && strings.HasPrefix(a.from, enterPrefix) && strings.Contains(err.Error(), "TERMINATEDABNORMALLY") {
		// Measured on the pinned Batfish (2026-10-25): a trace of a packet
		// entering an interface in a VRF other than default crashes it,
		// while the same router sending from that interface traces.
		return false, "", fmt.Errorf("%w; Batfish cannot trace a packet entering that interface (it does this for an interface in a VRF), so ask from %s, the router sending from it, instead",
			err, strings.TrimSuffix(strings.TrimPrefix(a.from, enterPrefix), ")"))
	}
	if err != nil {
		return false, "", err
	}
	var paths []string
	var problems []string
	traces := 0
	for _, r := range t.Rows {
		list, _ := r["Traces"].([]any)
		for _, tr := range list {
			trace, _ := tr.(map[string]any)
			traces++
			disposition := str(trace["disposition"])
			nodes := traceNodes(trace)
			paths = append(paths, strings.Join(nodes, ">")+" "+disposition)
			if deliveredDispositions[disposition] != a.delivered {
				problems = append(problems, fmt.Sprintf("a path ends %s", disposition))
			}
			if a.acceptedBy != "" && (disposition != "ACCEPTED" || len(nodes) == 0 || nodes[len(nodes)-1] != a.acceptedBy) {
				problems = append(problems, fmt.Sprintf("a path is not answered by %s", a.acceptedBy))
			}
			for _, n := range a.through {
				if !slices.Contains(nodes, n) {
					problems = append(problems, "a path does not go through "+n)
				}
			}
			for _, n := range a.avoids {
				if slices.Contains(nodes, n) {
					problems = append(problems, "a path goes through "+n)
				}
			}
		}
	}
	if traces == 0 {
		return false, fmt.Sprintf("the model traces no path from %s to %s", a.from, a.dst), nil
	}
	detail := fmt.Sprintf("from %s to %s: %s", a.from, a.dst, strings.Join(paths, "; "))
	if len(problems) > 0 {
		return false, strings.Join(dedupe(problems), ", ") + " (" + detail + ")", nil
	}
	return true, detail, nil
}

// compared are the protocols a live table and the model are compared on:
// the kernel's own routes (a container's default route) and FRR's local
// ones are not in a configuration, so the model cannot hold them.
var compared = map[string]bool{"connected": true, "static": true, "ospf": true, "bgp": true, "isis": true, "eigrp": true, "rip": true}

// holdRoutesMatch: the model's routes for node, by prefix and next hop,
// equal the routes net.frr.facts read from the router live.
func (m *model) holdRoutesMatch(ctx context.Context, snapshot string, a assertion) (bool, string, error) {
	vars := map[string]any{"nodes": a.node}
	if a.vrf != "" {
		vars["vrfs"] = a.vrf
	}
	t, err := m.ask(ctx, snapshot, "routes", vars)
	if err != nil {
		return false, "", err
	}
	model := map[string]map[string]bool{}
	for _, r := range t.Rows {
		protocol := protocolFamily(str(r["Protocol"]))
		if !compared[protocol] || slices.Contains(a.ignore, str(r["Network"])) || (a.vrf != "" && str(r["VRF"]) != a.vrf) {
			continue
		}
		key := str(r["Network"]) + " " + protocol
		if model[key] == nil {
			model[key] = map[string]bool{}
		}
		if ip := nextHopIP(r); ip != "" {
			model[key][ip] = true
		}
	}
	live := map[string]map[string]bool{}
	for prefix, entries := range a.liveRoutes {
		if slices.Contains(a.ignore, prefix) {
			continue
		}
		list, _ := entries.([]any)
		for _, e := range list {
			route, _ := e.(map[string]any)
			if selected, _ := route["selected"].(bool); !selected || !compared[str(route["protocol"])] {
				continue
			}
			key := prefix + " " + str(route["protocol"])
			if live[key] == nil {
				live[key] = map[string]bool{}
			}
			hops, _ := route["nexthops"].([]any)
			for _, h := range hops {
				hop, _ := h.(map[string]any)
				if active, _ := hop["active"].(bool); active && str(hop["ip"]) != "" {
					live[key][str(hop["ip"])] = true
				}
			}
		}
	}
	var diffs []string
	for key, hops := range live {
		mh, ok := model[key]
		switch {
		case !ok:
			diffs = append(diffs, "live, not modelled: "+key)
		case !sameSet(hops, mh):
			diffs = append(diffs, fmt.Sprintf("%s through %s live, %s modelled", key, keys(hops), keys(mh)))
		}
	}
	for key := range model {
		if _, ok := live[key]; !ok {
			diffs = append(diffs, "modelled, not live: "+key)
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		return false, fmt.Sprintf("%s's routes differ from the model: %s", a.node, strings.Join(diffs, "; ")), nil
	}
	return true, fmt.Sprintf("%s's %d routes match the model", a.node, len(live)), nil
}

// ask asks a question, naming it in the error.
func (m *model) ask(ctx context.Context, snapshot, question string, vars map[string]any) (bf.Table, error) {
	t, err := m.client.Ask(ctx, m.network, snapshot, question, vars)
	if err != nil {
		return bf.Table{}, fmt.Errorf("%s: the model could not answer %s, so nothing is known: %w", checkFQCN, question, err)
	}
	return t, nil
}

// holdQuestion: the rows of a Batfish question whose cells equal where's
// number between min_rows and max_rows.
func (m *model) holdQuestion(ctx context.Context, snapshot string, a assertion) (bool, string, error) {
	t, err := m.ask(ctx, snapshot, a.question, a.variables)
	if err != nil {
		return false, "", err
	}
	matched := 0
	for _, r := range t.Rows {
		ok := true
		for col, want := range a.where {
			if renderCell(r[col]) != renderCell(want) {
				ok = false
				break
			}
		}
		if ok {
			matched++
		}
	}
	detail := fmt.Sprintf("%d of %s's %d rows match", matched, a.question, len(t.Rows))
	if matched < a.minRows || (a.maxRows >= 0 && matched > a.maxRows) {
		want := fmt.Sprintf("at least %d", a.minRows)
		if a.maxRows >= 0 {
			want = fmt.Sprintf("%d to %d", a.minRows, a.maxRows)
		}
		return false, detail + ", want " + want, nil
	}
	return true, detail, nil
}

// Package firewall implements win.firewall.rule: a Windows Defender
// Firewall rule made, changed, removed or read over WinRM, in the machine's
// own rule store. Like win.schtask, its script is a constant
// (firewallScript) and every value reaches it as JSON on standard input.
package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const fqcnFirewallRule = "win.firewall.rule"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnFirewallRule,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportWinRM},
			RequiredCapabilities: []capability.Name{capability.NameWinRM},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A rule this run made is removed by its undo; a rule it changed gets back the earlier value of each setting it changed; " +
					"a rule it removed is made again with every setting it had, though not in the group it belonged to, which a rule " +
					"cannot be put back into. Its display name and description are text the device holds, withheld from the journal, so " +
					"an undo restoring either replays only from the task's own rollback: list.",
				Inverses: []sdk.InverseSpec{{FQCN: fqcnFirewallRule, Record: recorded, Withhold: []string{fwDisplayName, fwDescription}}},
			},
			Doc: firewallRuleDoc(),
		},
		Invoke: Rule,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return firewallRule(ctx, rc, d, p, true)
		},
	})
}

// recorded are the undo's parameters the run journal may keep: the rule's
// name and every setting but its display name and description.
var recorded = append([]string{paramName, paramState}, fwFields[2:]...)

// ruleAsIs is the script's answer: a rule as Windows holds it.
type ruleAsIs struct {
	Exists        bool     `json:"exists"`
	Policy        bool     `json:"policy"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Group         string   `json:"group"`
	Enabled       string   `json:"enabled"`
	Direction     string   `json:"direction"`
	Action        string   `json:"action"`
	Profiles      string   `json:"profiles"`
	Protocol      string   `json:"protocol"`
	LocalPort     []string `json:"local_port"`
	RemotePort    []string `json:"remote_port"`
	LocalAddress  []string `json:"local_address"`
	RemoteAddress []string `json:"remote_address"`
	Program       string   `json:"program"`
	Service       string   `json:"service"`
}

// canonical is the rule's settings in the form parseRule gives a call's.
func (a ruleAsIs) canonical() map[string]any {
	profiles := slices.Clone(profileNames)
	if !strings.EqualFold(a.Profiles, "any") {
		profiles = nil
		for _, p := range strings.Split(a.Profiles, ",") {
			if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
				profiles = append(profiles, p)
			}
		}
		slices.Sort(profiles)
	}
	protocol := strings.ToLower(a.Protocol)
	if canon, ok := protocols[protocol]; ok {
		protocol = canon
	}
	out := map[string]any{
		fwDisplayName: a.DisplayName, fwDescription: a.Description, fwEnabled: strings.EqualFold(a.Enabled, "true"),
		fwDirection: strings.ToLower(a.Direction), fwAction: strings.ToLower(a.Action), fwProfiles: profiles, fwProtocol: protocol,
		fwProgram: strings.ToLower(a.Program), fwService: strings.ToLower(a.Service),
	}
	for f, list := range map[string][]string{fwLocalPort: a.LocalPort, fwRemotePort: a.RemotePort, fwLocalAddress: a.LocalAddress, fwRemoteAddress: a.RemoteAddress} {
		canon, err := canonicalList(f, list)
		if err != nil {
			// A value this method does not write (a port keyword it does not
			// know): compared as Windows wrote it, so it differs from any asked.
			canon = nil
			for _, v := range list {
				canon = append(canon, strings.ToLower(v))
			}
			slices.Sort(canon)
		}
		out[f] = canon
	}
	return out
}

// asParams is the rule's settings as a call would give them, which is how
// an undo restores them: every value a single one, each list as one
// comma-separated string, so the run journal can keep it.
func (a ruleAsIs) asParams() map[string]any {
	c := a.canonical()
	out := map[string]any{fwDisplayName: a.DisplayName, fwDescription: a.Description, fwEnabled: c[fwEnabled],
		fwDirection: c[fwDirection], fwAction: c[fwAction], fwProtocol: c[fwProtocol], fwProgram: a.Program, fwService: a.Service}
	if p := c[fwProfiles].([]string); reflect.DeepEqual(p, profileNames) {
		out[fwProfiles] = "any"
	} else {
		out[fwProfiles] = strings.Join(p, ",")
	}
	for _, f := range []string{fwLocalPort, fwRemotePort, fwLocalAddress, fwRemoteAddress} {
		out[f] = strings.Join(c[f].([]string), ",")
	}
	return out
}

// ruleDifferences names each setting r asks for that the rule does not
// have, in fwFields' order.
func ruleDifferences(a ruleAsIs, r rule) ([]string, []string) {
	have := a.canonical()
	var fields, diffs []string
	for _, f := range fwFields {
		want, asked := r.want[f]
		if !asked || reflect.DeepEqual(have[f], want) {
			continue
		}
		fields = append(fields, f)
		diffs = append(diffs, fmt.Sprintf("%s is %v, not %v", f, have[f], want))
	}
	return fields, diffs
}

// callRule runs the script for op on r's rule, with set's settings, and
// reads its answer.
func callRule(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, opts winrmexec.Options, op, name string, set map[string]any) (ruleAsIs, error) {
	in, err := json.Marshal(map[string]any{"op": op, "name": name, "set": set})
	if err != nil {
		return ruleAsIs{}, err
	}
	res, err := run(ctx, target, auth, firewallScript, string(asciiJSON(in)), opts)
	if err != nil {
		return ruleAsIs{}, err
	}
	if res.ExitCode != 0 {
		return ruleAsIs{}, fmt.Errorf("%s exited %d: %s", op, res.ExitCode, bounded(res.Stderr))
	}
	if len(res.Stdout) > maxAnswer {
		return ruleAsIs{}, fmt.Errorf("the answer to %s is over %d bytes", op, maxAnswer)
	}
	var a ruleAsIs
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &a); err != nil {
		return ruleAsIs{}, fmt.Errorf("the answer to %s is not the rule's description: %w", op, err)
	}
	return a, nil
}

// Rule implements win.firewall.rule.
func Rule(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return firewallRule(ctx, rc, device, params, false)
}

func firewallRule(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnFirewallRule, err)
	}
	r, err := parseRule(params)
	if err != nil {
		return fail(err)
	}
	target, auth, opts, err := session(rc, device)
	if err != nil {
		return fail(err)
	}
	before, err := callRule(ctx, target, auth, opts, "query", r.name, nil)
	if err != nil {
		return fail(err)
	}
	if !before.Exists && before.Policy && r.state != "query" {
		return fail(fmt.Errorf("rule %q is set by Group Policy, not kept by this machine; change it in the policy", r.name))
	}
	op, fields, diffs, err := plan(before, r)
	if err != nil {
		return fail(err)
	}
	stats := map[string]any{"name": r.name, "existed": before.Exists, "group": before.Group, "differences": toAny(diffs), "rule": map[string]any{}}
	if before.Exists {
		stats["rule"] = before.asParams()
	}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	if check || op == "" {
		return collection.Result{Changed: op != ""}, nil
	}
	set := map[string]any{}
	for _, f := range fields {
		set[f] = r.send[f]
	}
	if op == "create" {
		set = r.send
		if _, named := set[fwDisplayName]; !named {
			set[fwDisplayName] = r.name
		}
	}
	after, err := callRule(ctx, target, auth, opts, op, r.name, set)
	if err != nil {
		return fail(err)
	}
	if after.Exists != (op != "remove") {
		return fail(fmt.Errorf("the rule is %s after %s", map[bool]string{true: "there", false: "not there"}[after.Exists], op))
	}
	if _, left := ruleDifferences(after, r); op != "remove" && len(left) > 0 {
		return fail(fmt.Errorf("the rule still differs after %s: %s", op, strings.Join(left, "; ")))
	}
	return collection.Result{Changed: true}, sdk.RecordInverse(rc, ruleUndo(op, r.name, before, fields))
}

// plan decides what a call does to the rule as it is: create, set the
// fields that differ, remove, or nothing (""), with the differences named.
func plan(a ruleAsIs, r rule) (op string, fields, diffs []string, err error) {
	switch {
	case r.state == "query":
		return "", nil, nil, nil
	case r.state == "absent":
		if a.Exists {
			return "remove", nil, []string{"the rule exists"}, nil
		}
		return "", nil, nil, nil
	}
	protocol, _ := r.want[fwProtocol].(string)
	if protocol == "" {
		protocol = "any"
		if a.Exists {
			protocol = a.canonical()[fwProtocol].(string)
		}
	}
	for _, f := range []string{fwLocalPort, fwRemotePort} {
		if ports, ok := r.want[f].([]string); ok && !reflect.DeepEqual(ports, []string{"any"}) && protocol != "tcp" && protocol != "udp" {
			return "", nil, nil, fmt.Errorf("%s needs protocol tcp or udp, and the rule's is %s", f, protocol)
		}
	}
	if !a.Exists {
		return "create", nil, []string{"the rule does not exist"}, nil
	}
	fields, diffs = ruleDifferences(a, r)
	if len(fields) == 0 {
		return "", nil, nil, nil
	}
	return "set", fields, diffs, nil
}

// ruleUndo is what puts the rule back as it was before op.
func ruleUndo(op, name string, before ruleAsIs, fields []string) sdk.Inverse {
	switch op {
	case "create":
		return sdk.Inverse{FQCN: fqcnFirewallRule, Params: map[string]any{paramName: name, paramState: "absent"},
			Description: "Remove firewall rule " + name + ", which this run made."}
	case "remove":
		p := before.asParams()
		p[paramName], p[paramState] = name, "present"
		return sdk.Inverse{FQCN: fqcnFirewallRule, Params: p, Description: "Make firewall rule " + name + " again as it was before this run removed it."}
	}
	all := before.asParams()
	p := map[string]any{paramName: name, paramState: "present"}
	for _, f := range fields {
		p[f] = all[f]
	}
	return sdk.Inverse{FQCN: fqcnFirewallRule, Params: p, Description: "Put back " + strings.Join(fields, ", ") + " of firewall rule " + name + "."}
}

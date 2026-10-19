// win.firewall.profile: one of Windows Defender Firewall's three profiles
// (domain, private, public) set or read over WinRM: whether the firewall is
// on, what it does with traffic no rule matches, and what it logs.
package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnProfile = "win.firewall.profile"

// The profile's settings this method manages, as its parameters name them.
const (
	pfEnabled     = "enabled"
	pfInbound     = "default_inbound_action"
	pfOutbound    = "default_outbound_action"
	pfLogAllowed  = "log_allowed"
	pfLogBlocked  = "log_blocked"
	pfLogFile     = "log_file"
	pfLogMaxSizeK = "log_max_size_kb"
)

// pfFields are the managed settings, in the order a difference is named.
var pfFields = []string{pfEnabled, pfInbound, pfOutbound, pfLogAllowed, pfLogBlocked, pfLogFile, pfLogMaxSizeK}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnProfile,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportWinRM},
			RequiredCapabilities: []capability.Name{capability.NameWinRM},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that changed the profile undoes to the earlier value of each setting it changed. A setting Windows held as " +
					"NotConfigured is put back as the value it had in effect, which behaves the same.",
				Inverses: []sdk.InverseSpec{{FQCN: fqcnProfile, Record: append([]string{paramName}, pfFields...)}},
			},
			Doc: profileDoc(),
		},
		Invoke: Profile,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return firewallProfile(ctx, rc, d, p, true)
		},
	})
}

// profileAsIs is the script's answer: the profile as the machine keeps it
// (Persistent) and as it is in force (Active, Group Policy's included).
type profileAsIs struct {
	Persistent map[string]string `json:"persistent"`
	Active     map[string]string `json:"active"`
}

// profileDefaults are what a NotConfigured setting does.
var profileDefaults = map[string]string{pfEnabled: "true", pfInbound: "block", pfOutbound: "allow", pfLogAllowed: "false", pfLogBlocked: "false"}

// effective is a profile's settings as they behave, in a call's form:
// NotConfigured read as what it does, words in lower case, the log file as
// written.
func effective(raw map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range pfFields {
		v := raw[f]
		if f != pfLogFile {
			v = strings.ToLower(v)
		}
		if strings.EqualFold(v, "notconfigured") || v == "" {
			if d, ok := profileDefaults[f]; ok {
				v = d
			}
		}
		out[f] = v
	}
	return out
}

// profileCall is a call: the profile and the settings it gives, in their
// compared form (want) and as sent (send).
type profileCall struct {
	name       string
	want, send map[string]string
}

// parseProfile reads a call, holding every value to its form.
func parseProfile(params map[string]any) (profileCall, error) {
	c := profileCall{want: map[string]string{}, send: map[string]string{}}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return c, err
	}
	if c.name = strings.ToLower(name); !slices.Contains(profileNames, c.name) {
		return c, fmt.Errorf("%s %q is domain, private or public", paramName, name)
	}
	for _, f := range []string{pfEnabled, pfLogAllowed, pfLogBlocked} {
		if _, given := params[f]; !given {
			continue
		}
		on, err := sdk.BoolParamOr(params, f, false)
		if err != nil {
			return c, err
		}
		c.want[f], c.send[f] = strconv.FormatBool(on), map[bool]string{true: "True", false: "False"}[on]
	}
	for _, f := range []string{pfInbound, pfOutbound} {
		v := strings.ToLower(sdk.StringParam(params, f))
		if v == "" {
			continue
		}
		if v != "allow" && v != "block" {
			return c, fmt.Errorf("%s is allow or block", f)
		}
		c.want[f], c.send[f] = v, strings.ToUpper(v[:1])+v[1:]
	}
	if v, err := sdk.OptionalStringParam(params, pfLogFile); err != nil {
		return c, err
	} else if _, given := params[pfLogFile]; given {
		if v == "" || len(v) > 260 || strings.ContainsAny(v, "\"*?<>|\x00\r\n") {
			return c, fmt.Errorf("%s %q is not a file path", pfLogFile, v)
		}
		c.want[pfLogFile], c.send[pfLogFile] = v, v
	}
	if n, given, err := sdk.IntParam(params, pfLogMaxSizeK); err != nil {
		return c, err
	} else if given {
		if n < 1 || n > 32767 {
			return c, fmt.Errorf("%s is 1 to 32767 kilobytes", pfLogMaxSizeK)
		}
		c.want[pfLogMaxSizeK], c.send[pfLogMaxSizeK] = strconv.Itoa(n), strconv.Itoa(n)
	}
	return c, nil
}

// profileDifferences names each setting c asks for that has differs from,
// in pfFields' order. The log file compares without regard to case.
func profileDifferences(has map[string]string, c profileCall) (fields, diffs []string) {
	for _, f := range pfFields {
		want, asked := c.want[f]
		if !asked || has[f] == want || (f == pfLogFile && strings.EqualFold(has[f], want)) {
			continue
		}
		fields = append(fields, f)
		diffs = append(diffs, fmt.Sprintf("%s is %q, not %q", f, has[f], want))
	}
	return fields, diffs
}

// callProfile runs the profile script for op, with set's settings.
func callProfile(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, op, name string, set map[string]string) (profileAsIs, error) {
	target, auth, opts, err := session(rc, device)
	if err != nil {
		return profileAsIs{}, err
	}
	in, err := json.Marshal(map[string]any{"op": op, "name": name, "set": set})
	if err != nil {
		return profileAsIs{}, err
	}
	res, err := run(ctx, target, auth, profileScript, string(asciiJSON(in)), opts)
	if err != nil {
		return profileAsIs{}, err
	}
	if res.ExitCode != 0 {
		return profileAsIs{}, fmt.Errorf("%s exited %d: %s", op, res.ExitCode, bounded(res.Stderr))
	}
	if len(res.Stdout) > maxAnswer {
		return profileAsIs{}, fmt.Errorf("the answer to %s is over %d bytes", op, maxAnswer)
	}
	var a profileAsIs
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &a); err != nil || a.Persistent == nil || a.Active == nil {
		return profileAsIs{}, fmt.Errorf("the answer to %s is not the profile's description: %v", op, err)
	}
	return a, nil
}

// Profile implements win.firewall.profile.
func Profile(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return firewallProfile(ctx, rc, device, params, false)
}

func firewallProfile(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnProfile, err)
	}
	c, err := parseProfile(params)
	if err != nil {
		return fail(err)
	}
	before, err := callProfile(ctx, rc, device, "query", c.name, nil)
	if err != nil {
		return fail(err)
	}
	kept, active := effective(before.Persistent), effective(before.Active)
	fields, diffs := profileDifferences(kept, c)
	overridden := policyOverrides(active, kept, c)
	stats := map[string]any{"name": c.name, "settings": toAnyMap(kept), "in_force": toAnyMap(active), "differences": toAny(diffs), "overridden": toAny(overridden)}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	if len(fields) == 0 && len(overridden) > 0 {
		return fail(fmt.Errorf("Group Policy sets %s on the %s profile, so the machine's own setting has no effect", strings.Join(overridden, ", "), c.name))
	}
	if check || len(fields) == 0 {
		return collection.Result{Changed: len(fields) > 0}, nil
	}
	set := map[string]string{}
	for _, f := range fields {
		set[f] = c.send[f]
	}
	after, err := callProfile(ctx, rc, device, "set", c.name, set)
	if err != nil {
		return fail(err)
	}
	if _, left := profileDifferences(effective(after.Persistent), c); len(left) > 0 {
		return fail(fmt.Errorf("the profile still differs after the change: %s", strings.Join(left, "; ")))
	}
	undo := map[string]any{paramName: c.name}
	for _, f := range fields {
		undo[f] = asParam(f, kept[f])
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: fqcnProfile, Params: undo,
		Description: "Put back " + strings.Join(fields, ", ") + " of the " + c.name + " firewall profile."}); err != nil {
		return fail(err)
	}
	if overridden := policyOverrides(effective(after.Active), effective(after.Persistent), c); len(overridden) > 0 {
		return fail(fmt.Errorf("the change was made, and Group Policy sets %s on the %s profile, so it has no effect", strings.Join(overridden, ", "), c.name))
	}
	return collection.Result{Changed: true}, nil
}

// policyOverrides names the settings c asks for that are in force with
// another value than the machine's own: set by Group Policy over it. A
// setting not yet changed, in force as the machine keeps it, is not one.
func policyOverrides(active, kept map[string]string, c profileCall) []string {
	fields, _ := profileDifferences(active, c)
	var out []string
	for _, f := range fields {
		if active[f] != kept[f] {
			out = append(out, f)
		}
	}
	return out
}

// asParam is a setting's compared value as a call gives it.
func asParam(field, v string) any {
	switch field {
	case pfEnabled, pfLogAllowed, pfLogBlocked:
		return v == "true"
	case pfLogMaxSizeK:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return v
}

// toAnyMap is a profile's settings as a return value, each in a call's
// form.
func toAnyMap(m map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = asParam(k, v)
	}
	return out
}

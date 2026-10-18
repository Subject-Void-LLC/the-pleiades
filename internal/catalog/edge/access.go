// edge.access: an Access application's audience, issuer, settings and
// policies against what is expected.
package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters.
const (
	paramApp           = "app"
	paramExactPolicies = "exact_policies"
)

// The fields expect may name for an application, and for a policy.
var (
	appExpect    = []string{"audience", "issuer", "domain", "type", "session_duration", "policies"}
	policyExpect = []string{"name", "decision", "precedence", "include", "exclude", "require"}
)

func init() { method(fqcnAccess, accessDoc(), Access) }

// Access implements edge.access.
func Access(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnAccess, err)
	}
	named, err := sdk.RequiredStringParam(params, paramApp)
	if err != nil {
		return fail(err)
	}
	expect, err := expectMap(params, appExpect...)
	if err != nil {
		return fail(err)
	}
	wantPolicies, err := expectedPolicies(expect["policies"])
	if err != nil {
		return fail(err)
	}
	exact, err := sdk.BoolParamOr(params, paramExactPolicies, false)
	if err != nil {
		return fail(err)
	}
	// exact_policies compares against expect.policies; with none it would
	// compare nothing and pass whatever policies the application has.
	if exact && wantPolicies == nil {
		return fail(fmt.Errorf("%s needs %s.policies, the policies the application must hold and no others", paramExactPolicies, paramExpect))
	}
	c, acct, err := account(rc, device)
	if err != nil {
		return fail(err)
	}
	app, err := c.App(ctx, acct, named)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	issuer, err := c.Issuer(ctx, acct)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	var diffs []string
	for _, f := range []struct{ key, got string }{
		{"audience", app.Audience}, {"issuer", issuer}, {"domain", app.Domain}, {"type", app.Type}, {"session_duration", app.SessionDuration},
	} {
		raw, set := expect[f.key]
		if !set {
			continue
		}
		want, ok := raw.(string)
		if !ok {
			return fail(fmt.Errorf("%s.%s is text", paramExpect, f.key))
		}
		if !strings.EqualFold(want, f.got) || (f.key != "domain" && f.key != "issuer" && want != f.got) {
			diffs = append(diffs, fmt.Sprintf("%s is %q, not %q", f.key, f.got, want))
		}
	}
	if wantPolicies != nil {
		diffs = append(diffs, comparePolicies(app.Policies, wantPolicies, exact)...)
	}
	listed := make([]any, len(app.Policies))
	for i, p := range app.Policies {
		listed[i] = map[string]any{"name": p.Name, "decision": p.Decision, "precedence": p.Precedence}
	}
	return finish(rc, fqcnAccess, map[string]any{
		"id": app.ID, "name": app.Name, "domain": app.Domain, "type": app.Type, "audience": app.Audience,
		"issuer": issuer, "session_duration": app.SessionDuration, "policies": listed,
	}, diffs)
}

// expectedPolicies reads expect.policies: a list of policies, each named
// once, nil when absent.
func expectedPolicies(raw any) (map[string]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s.policies is a list of {name, decision, include, exclude, require, precedence}", paramExpect)
	}
	out := map[string]map[string]any{}
	for i, item := range list {
		p, ok := item.(map[string]any)
		name, _ := p["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s.policies[%d] names no policy", paramExpect, i)
		}
		for k := range p {
			allowed := false
			for _, a := range policyExpect {
				allowed = allowed || a == k
			}
			if !allowed {
				return nil, fmt.Errorf("%s.policies[%d].%s is not one of %s", paramExpect, i, k, strings.Join(policyExpect, ", "))
			}
		}
		if d, set := p["decision"]; set {
			if _, ok := d.(string); !ok {
				return nil, fmt.Errorf("%s.policies[%d].decision is text (allow, deny, non_identity, bypass)", paramExpect, i)
			}
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s.policies names %s twice", paramExpect, name)
		}
		out[name] = p
	}
	return out, nil
}

// comparePolicies lists how got differs from want: a policy missing, one
// changed (its decision, precedence or a rule, rules compared as JSON),
// and, when exact, one present and not expected.
func comparePolicies(got []cfapi.Policy, want map[string]map[string]any, exact bool) []string {
	byName := map[string][]cfapi.Policy{}
	for _, p := range got {
		byName[p.Name] = append(byName[p.Name], p)
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	var diffs []string
	for _, n := range names {
		w, have := want[n], byName[n]
		switch {
		case len(have) == 0:
			diffs = append(diffs, fmt.Sprintf("policy %q is missing", n))
			continue
		case len(have) > 1:
			diffs = append(diffs, fmt.Sprintf("%d policies are named %q", len(have), n))
			continue
		}
		p := have[0]
		if d, set := w["decision"].(string); set && d != p.Decision {
			diffs = append(diffs, fmt.Sprintf("policy %q decides %s, not %s", n, p.Decision, d))
		}
		if pr, set := w["precedence"]; set && fmt.Sprint(pr) != fmt.Sprint(p.Precedence) {
			diffs = append(diffs, fmt.Sprintf("policy %q has precedence %d, not %v", n, p.Precedence, pr))
		}
		for _, rule := range []struct {
			key string
			got json.RawMessage
		}{{"include", p.Include}, {"exclude", p.Exclude}, {"require", p.Require}} {
			if w, set := w[rule.key]; set && !sameJSON(w, rule.got) {
				diffs = append(diffs, fmt.Sprintf("policy %q's %s rules are %s, not %s", n, rule.key, canonical(rule.got), mustJSON(w)))
			}
		}
	}
	if exact {
		var extra []string
		for n := range byName {
			if _, expected := want[n]; !expected {
				extra = append(extra, n)
			}
		}
		sort.Strings(extra)
		for _, n := range extra {
			diffs = append(diffs, fmt.Sprintf("policy %q is not expected", n))
		}
	}
	return diffs
}

// sameJSON reports whether want, a decoded YAML value, is the same JSON as
// got; an absent or null rule list is the same as an empty one.
func sameJSON(want any, got json.RawMessage) bool {
	return canonical([]byte(mustJSON(want))) == canonical(got)
}

// canonical is JSON re-encoded with sorted keys, and [] for null or none.
func canonical(raw []byte) string {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "[]"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	return mustJSON(v)
}

// mustJSON encodes v, which is decoded JSON or YAML and always encodes.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

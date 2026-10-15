// gate.list: a project's checks, validated and classified, none run.
package gate

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnList,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion: ">=0.2.0",
			Status:        collection.StatusImplemented,
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				ReadOnly:   true,
				Notes:      "It reads the manifest and runs nothing, so there is nothing to undo.",
			},
			Doc: listDoc(),
		},
		Invoke: List,
		// Nothing in the manifest runs, so a check is the task itself.
		Check: List,
	})
}

// List implements gate.list.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := parseRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}
	conn, err := connectFor(ctx, rc, device, params, fqcnList)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	m, err := loadManifest(ctx, conn, req.manifest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}
	plan, err := m.Plan(req.selection)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}

	checks := make([]any, len(plan))
	for i, p := range plan {
		checks[i] = listEntry(req.manifest, device, p)
	}
	for key, value := range map[string]any{
		statChecks:   checks,
		statKinds:    kindEntries(m),
		statSelected: countSelected(plan),
	} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// listEntry describes one planned check.
func listEntry(manifest string, device inventory.InventoryItem, p gate.Planned) map[string]any {
	c := p.Check
	runs := "dir " + dirOf(manifest, c)
	if c.Image != "" {
		runs = "image " + c.Image
	}
	reason := p.Reason
	runnable := true
	if why := engineRefusal(device, c); why != "" {
		runnable = false
		reason = strings.TrimLeft(reason+"; "+why, "; ")
	}
	return map[string]any{
		"name":      c.Name,
		"kind":      c.Kind,
		"heavy":     c.Heavy,
		"serial":    c.Serial,
		"timeout_s": int64(time.Duration(p.Timeout).Seconds()),
		"runs":      runs,
		"requires":  requiresText(c.Requires),
		"selected":  p.Selected,
		"reason":    reason,
		"runnable":  runnable,
	}
}

// requiresText is a check's preconditions as written, for a person.
func requiresText(r gate.Requires) string {
	var parts []string
	for _, group := range []struct {
		name   string
		values []string
	}{{"commands", r.Commands}, {"env", r.Env}, {"files", r.Files}, {"tcp", r.TCP}} {
		if len(group.values) > 0 {
			parts = append(parts, group.name+" "+strings.Join(group.values, ","))
		}
	}
	if r.OS != "" {
		parts = append(parts, "os "+r.OS)
	}
	if r.Engine {
		parts = append(parts, "engine")
	}
	return strings.Join(parts, "; ")
}

// kindEntries is every kind m may use.
func kindEntries(m *gate.Manifest) []any {
	var kinds []any
	for _, k := range gate.BuiltinKinds() {
		kinds = append(kinds, map[string]any{"name": k, "builtin": true, "description": ""})
	}
	for _, k := range m.Kinds {
		kinds = append(kinds, map[string]any{"name": k.Name, "builtin": false, "description": k.Description})
	}
	slices.SortFunc(kinds, func(a, b any) int {
		return strings.Compare(a.(map[string]any)["name"].(string), b.(map[string]any)["name"].(string))
	})
	return kinds
}

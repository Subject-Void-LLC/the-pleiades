// Package lab: virt.cml.lab.list, every lab a CML controller's account can
// see. Its registration's Doc is generated from internal/forge/catalogdata.
package lab

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.lab.list",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"https",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CMLAPICapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It only reads the controller.",
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Lists the labs on a CML controller.",
				Description: "Reads every lab the controller's account can see, with its title, state and node count, and changes nothing. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read.",
				Returns: []collection.ReturnField{
					{Name: "labs", Type: "list", Returned: "always", Description: "Each lab as id, title, state (DEFINED_ON_CORE, STARTED or STOPPED) and nodes (its node count), by title."},
				},
				Examples: []collection.Example{
					{Name: "List the labs", RunbookYAML: "- name: List the controller's labs\n  virt.cml.lab.list: {}\n  register: labs\n"},
				},
				SeeAlso: []string{"virt.cml.lab.info"},
			},
		},
		Invoke: List,
		Check:  List,
	})
}

// List implements "virt.cml.lab.list", and is its own check, since it
// only reads.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	const fqcn = "virt.cml.lab.list"
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	labs, err := client.Labs(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	sort.Slice(labs, func(i, j int) bool {
		return labs[i].Title < labs[j].Title || labs[i].Title == labs[j].Title && labs[i].ID < labs[j].ID
	})
	out := make([]any, 0, len(labs))
	for _, l := range labs {
		s := labStats(l)
		s["nodes"] = l.NodeCount
		out = append(out, s)
	}
	return collection.Result{}, rc.SetStat("labs", out)
}

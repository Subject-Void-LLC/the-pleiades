// Command triage is the external Collection the ticket runbook release gate
// (Phase 117a) approves and runs: a device-less program that reads a
// ticket's title and body and says which configuration item it names and
// how severe it is. It is the "triage by a script" step of the owner's
// scenario, built the way an operator would build one, on pkg/external.
package main

import (
	"context"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ciLine finds the configuration item a ticket body names on a line of its
// own, "ci: <name>", keeping whatever follows the colon as written.
var ciLine = regexp.MustCompile(`(?m)^ci:\s*(.*)$`)

// triage reads params' title and body and records ci and severity.
func triage(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	title, _ := params["title"].(string)
	body, _ := params["body"].(string)
	ci := ""
	if m := ciLine.FindStringSubmatch(body); m != nil {
		ci = strings.TrimSpace(m[1])
	}
	severity := "low"
	if strings.Contains(strings.ToLower(title), "down") {
		severity = "high"
	}
	if err := rc.SetStat("ci", ci); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{}, rc.SetStat("severity", severity)
}

func main() {
	external.Main(collection.Descriptor{
		Name: "gate117.ticket.triage",
		Manifest: collection.Manifest{
			Status: collection.StatusImplemented,
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteController,
				Device: collection.DeviceNone,
			},
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "It reads its parameters and changes nothing."},
			Doc: collection.Doc{
				Summary: "Says which configuration item a ticket names and how severe it is.",
				Params: []collection.Param{
					{Name: "title", Type: "string", Required: true, Description: "The ticket's title."},
					{Name: "body", Type: "string", Required: true, Description: "The ticket's body, naming its item on a ci: line."},
				},
				Returns: []collection.ReturnField{
					{Name: "ci", Type: "string", Returned: "always", Description: "The configuration item the body names, or empty."},
					{Name: "severity", Type: "string", Returned: "always", Description: "high when the title says something is down, low otherwise."},
				},
			},
		},
		Invoke: triage,
	})
}

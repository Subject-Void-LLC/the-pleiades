// Command pleiades: the grant warnings a run reports, taken from the shared
// validation's report.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantRunWarnings is report's grant warnings (validate.GrantRuleName) as
// the run's own warnings of kind grant, at its start, each naming the
// device by name. The rest of a report's warnings are about the runbook as
// a file, which `pleiades validate` shows, and a run never has.
func grantRunWarnings(report validate.Report, items []pkginventory.InventoryItem) []clireport.RunWarning {
	names := make(map[pkginventory.DeviceID]string, len(items))
	for _, item := range items {
		names[item.ID()] = item.Name()
	}
	var out []clireport.RunWarning
	for _, w := range report.Warnings {
		if w.RuleName != validate.GrantRuleName {
			continue
		}
		warning := clireport.RunWarning{Kind: clireport.WarnGrant, When: "start", Message: w.Message}
		if name, ok := names[w.Device]; ok {
			warning.Devices = []string{name}
		}
		out = append(out, warning)
	}
	return out
}

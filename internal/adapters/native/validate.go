// Package native: the plan-time validation a dispatched runbook passes on
// the Runner before any of its tasks runs.
package native

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// validateDispatch runs every registered validation rule against dag as
// it is about to run on device in mode, resolving targets through the
// same singleDeviceResolver the executor gets, and returns the findings
// as one error, or nil when there are none. The findings name tasks,
// methods, parameter names and the device, never a parameter's value.
//
// It also returns the report's grant warnings (validate.GrantWarnings),
// the one kind of warning a Runner reports, as `pleiades run` does: a grant
// of an external program's capability on the device that grants nothing,
// or counts only with a warning. The Runner holds the definitions its
// loaded programs gave, so it can say which grant lapsed and why, where the
// Controller, which loads none, cannot.
func validateDispatch(dag *engine.DAG, device inventory.InventoryItem, mode collection.Mode) ([]string, error) {
	report := validate.Validate(validate.WorldView{
		Items:    []inventory.InventoryItem{device},
		DAG:      dag,
		Mode:     mode,
		Resolver: singleDeviceResolver{device: device},
	})
	var warnings []string
	for _, w := range report.Warnings {
		if w.RuleName == validate.GrantRuleName {
			warnings = append(warnings, w.Message)
		}
	}
	if report.HasErrors() {
		return warnings, fmt.Errorf("it fails validation, so no task ran:\n%s", report.String())
	}
	return warnings, nil
}

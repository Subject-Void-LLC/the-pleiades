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
func validateDispatch(dag *engine.DAG, device inventory.InventoryItem, mode collection.Mode) error {
	report := validate.Validate(validate.WorldView{
		Items:    []inventory.InventoryItem{device},
		DAG:      dag,
		Mode:     mode,
		Resolver: singleDeviceResolver{device: device},
	})
	if report.HasErrors() {
		return fmt.Errorf("it fails validation, so no task ran:\n%s", report.String())
	}
	return nil
}

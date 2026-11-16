// Package yangfacts: the yang.* facts a gNMI read records. gNMI's
// Capabilities RPC lists the models a server implements, which is not a YANG
// library (owner decision 2026-11-14): no namespace, a model's own version in
// place of a revision when it is no date, and nothing that makes the list
// every module the device has. So a gNMI read records the module list, its
// count and its completeness, always partial, and never yang.modules_digest,
// yang.digest_rule or yang.content_id: a digest under the same key computed
// from a list that is no library would invite exactly the comparison
// yang.digest_rule exists to prevent. gNMI's own digest of its model list is
// gnmi.models_digest.
package yangfacts

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// GNMIKeys are the yang.* facts a gNMI read records, in order.
var GNMIKeys = []string{FactModules, FactModuleCount, FactCompleteness}

// GNMIValues returns the yang.* facts modules give (pkg/gnmi's
// Capabilities.YANGModules), completeness always partial, and a warning for
// a list past its bound and for the models unread names.
func GNMIValues(modules []yanglib.Module, unread []string) (map[string]any, []string, error) {
	values := map[string]any{
		FactModuleCount:  len(modules),
		FactCompleteness: string(yanglib.Partial),
	}
	list, err := ModulesValue(modules)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if list != nil {
		values[FactModules] = list
	} else if len(modules) > 0 {
		warnings = append(warnings, fmt.Sprintf("the device lists %d models, more than %s holds in 60 KiB; recorded by count only", len(modules), FactModules))
	}
	if len(unread) > 0 {
		quoted := make([]string, 0, min(len(unread), 5))
		for _, name := range unread[:min(len(unread), 5)] {
			quoted = append(quoted, fmt.Sprintf("%q", name))
		}
		warnings = append(warnings, fmt.Sprintf("%d of the device's gNMI models name no YANG module and are left out of %s: %s", len(unread), FactModules, strings.Join(quoted, ", ")))
	}
	return values, warnings, nil
}

// GNMIReturns documents the yang.* facts a gNMI read records; when is their
// Returned condition.
func GNMIReturns(when string) []collection.ReturnField {
	return []collection.ReturnField{
		{Name: FactModules, Type: "list of string", Returned: when + ", when the list fits in 60 KiB", Description: "Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library."},
		{Name: FactModuleCount, Type: "int", Returned: when, Description: "How many modules yang.modules lists."},
		{Name: FactCompleteness, Type: "string", Returned: when, Description: "Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete."},
	}
}

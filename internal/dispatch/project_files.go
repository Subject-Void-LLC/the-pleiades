// Package dispatch: the Controller's refusal of a runbook whose task names a
// file in the project, made before anything is published.
//
// A Runner is handed the runbook, never the project, so a method reading or
// writing a project file (pleiades.builtin.project.write, file.copy's src,
// net.yang.schema's dest, left to its default or not) can only fail there.
// The Runner's validation refuses such a task (internal/validate's
// ProjectFileRule), but only once the dispatch has crossed the stream. Fan-out
// refuses the job first, by the same rule (collection.ProjectFiles) in the
// same words, reading each method's parameters from the registry or, on a
// Controller, the module catalog it installed, as admission does. A task under
// a condition of its own is not among runbook.Runbook.Calls, as it is not
// among the methods admission checks, and stays the Runner's to refuse where
// its condition holds.
package dispatch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ErrProjectFile marks a runbook refused on the Walk tier because one of its
// tasks names a file in the project, which only the pleiades CLI holds.
var ErrProjectFile = errors.New("a task names a file in the project, which only the pleiades CLI holds")

// RefuseProjectFiles returns an error wrapping ErrProjectFile that names every
// task of rb, and each parameter, naming a project file, or nil when none
// does. A method neither registered nor in the installed catalog (an external
// Collection's) is left to the Runner, which loads it.
func RefuseProjectFiles(rb *runbook.Runbook) error {
	if rb == nil {
		return nil
	}
	fqcns := make([]string, 0, len(rb.Calls))
	for fqcn := range rb.Calls {
		fqcns = append(fqcns, fqcn)
	}
	sort.Strings(fqcns)
	var refusals []string
	for _, fqcn := range fqcns {
		manifest, ok := collection.ManifestOf(fqcn)
		if !ok {
			continue
		}
		for _, call := range rb.Calls[fqcn] {
			for _, f := range collection.ProjectFiles(manifest.Doc, call.Params) {
				refusals = append(refusals, f.Refusal(call.Label()+" ("+fqcn+")"))
			}
		}
	}
	if len(refusals) == 0 {
		return nil
	}
	return fmt.Errorf("runbook %q refused before dispatch: %w: %s", rb.ID, ErrProjectFile, strings.Join(refusals, "; "))
}

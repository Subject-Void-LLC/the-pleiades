// Package runbook registers the native runbook launch kind: the platform's
// own typed automation format, executed by the native adapter.
//
// It is reachable only because internal/launch/builtins.go blank-imports
// it. A kind package nothing imports never registers, which is
// FAILURE_PATTERNS.md #52 and the reason that file exists.
package runbook

import (
	"fmt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	rbsource "github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// Kind is this kind's registry key, and the value stored on a template.
const Kind = "runbook"

// Adapter names the execution adapter that runs it: internal/adapters/native,
// which resolves the runbook to a compiled DAG and runs it through the same
// engine.Executor stack the Crawl-tier CLI uses.
const Adapter = "native"

func init() {
	launch.MustRegister(launch.Descriptor{
		Kind:       Kind,
		Label:      "Runbook",
		BadgeClass: "badge-ok",
		Summary:    "This platform's own typed automation format, executed natively against the device.",
		Adapter:    Adapter,
		Fields: []launch.FieldSpec{
			{
				Name: launch.ModeField, Type: launch.TypeChoice,
				Choices: []string{string(collection.ModeExecute), string(collection.ModeCheck)},
				Label:   "MODE",
				Help:    "execute changes devices; check asks every task what it would change and changes nothing. A check can be asked for at any level, and nothing can turn one back into a real run.",
			},
			{
				Name: "limit", Type: launch.TypeString,
				Label: "LIMIT",
				Help:  "Narrow this run to a subset of the inventory, by device name. Empty runs against everything the inventory holds.",
			},
			{
				Name: "verbosity", Type: launch.TypeInt, Min: 0, Max: 4,
				Label: "VERBOSITY",
				Help:  "How much detail the run logs, 0 to 4. Ansible's own scale, so a number means the same thing either side of a migration.",
			},
			{
				Name: "forks", Type: launch.TypeInt, Min: 1, Max: 1000,
				Label: "FORKS",
				Help:  "How many devices are worked on at once.",
			},
			{
				Name: "timeout", Type: launch.TypeInt, Min: 0, Max: 86400,
				Label: "TIMEOUT",
				Help:  "Seconds before a task is abandoned. Zero means no timeout, which is the platform default rather than an omission.",
			},
			{
				Name: "extra_vars", Type: launch.TypeMap,
				Label: "EXTRA VARIABLES",
				Help:  "Variables the runbook reads. These merge across layers rather than replacing, so a launch adding one keeps the template's others.",
			},
			{
				Name: "labels", Type: launch.TypeStringList,
				Label: "LABELS",
				Help:  "Free-text markers for finding this run later.",
			},
		},
		ValidateDefinition: validateRunbookID,
	})
}

// validateRunbookID checks a runbook id is one the resolver could resolve.
//
// The grammar is internal/runbook.ValidID, imported rather than restated.
// This validator used to state its own, and the two disagreed: it accepted
// ids up to 253 characters where the resolver's bound is 64, so a template
// could be saved whose launch could only ever fail. One grammar with two
// statements is how the playbook kind shipped a worse version of the same
// defect (its two statements were entirely disjoint), and the only fix
// that stays fixed is one statement.
//
// Shape only, not existence: importing the grammar is not importing the
// filesystem, and whether the id names a real runbook stays a question for
// whoever holds the source (the launch catalog at create, the dispatch
// worker at fan-out). The shape still matters in its own right: a runbook
// id reaches a NATS subject by concatenation (FAILURE_PATTERNS.md #18 and
// #81) and a filesystem path through filepath.Join (#78), and this is the
// boundary a template crosses.
func validateRunbookID(reference string) error {
	id := strings.TrimSpace(reference)
	if id == "" {
		return fmt.Errorf("a runbook template needs a runbook id")
	}
	if rbsource.ValidID(id) {
		return nil
	}

	// Refused either way; the rest is diagnosis.
	if len(id) > 64 {
		return fmt.Errorf("runbook id is longer than 64 characters")
	}
	if strings.ContainsAny(id, `/\.*>`+"\x00") {
		// Dots and slashes are what turn an id into a path; the NATS
		// wildcards are what turn a subject into every subject.
		return fmt.Errorf("runbook id %q contains a character that is not allowed in one: it reaches both a subject name and a file path", id)
	}
	return fmt.Errorf("runbook id %q contains a character that is not a letter, digit, hyphen or underscore", id)
}

// Package runbook registers the native runbook launch kind: the platform's
// own typed automation format, executed by the native adapter.
//
// It is reachable only because internal/launch/builtins.go blank-imports
// it. A kind package nothing imports never registers, which is
// FAILURE_PATTERNS.md #52 and the reason that file exists.
package runbook

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// Kind is this kind's registry key, and the value stored on a template.
const Kind = "runbook"

// Adapter names the execution adapter that runs it: internal/adapters/native,
// which resolves the runbook to a compiled DAG and runs it through the same
// engine.Executor stack the Walk-tier CLI uses.
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

// validateRunbookID checks a runbook id is one that could name a runbook.
//
// Shape only, not existence: this package does not import the runbook
// source, so it cannot say whether a runbook is there. What it can say is
// that an id is not the kind of string that has already caused real damage
// here. A runbook id reaches a NATS subject by concatenation
// (FAILURE_PATTERNS.md #18 and #81) and a filesystem path through
// filepath.Join (#78), and both were fixed at their own boundaries. This is
// the boundary a template crosses, so it refuses the same class of value
// rather than trusting that every downstream boundary is still guarded.
func validateRunbookID(reference string) error {
	id := strings.TrimSpace(reference)
	switch {
	case id == "":
		return fmt.Errorf("a runbook template needs a runbook id")
	case len(id) > 253:
		return fmt.Errorf("runbook id is longer than 253 characters")
	case strings.ContainsAny(id, `/\.*>`+"\x00"):
		// Dots and slashes are what turn an id into a path; the NATS
		// wildcards are what turn a subject into every subject.
		return fmt.Errorf("runbook id %q contains a character that is not allowed in one: it reaches both a subject name and a file path", id)
	}

	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("runbook id %q contains %q, which is not a letter, digit, hyphen or underscore", id, r)
		}
	}
	return nil
}

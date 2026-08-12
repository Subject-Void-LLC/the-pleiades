// Package playbook registers the Ansible playbook launch kind: an
// unconverted playbook, run through the sandboxed legacy adapter.
//
// It is the migration on-ramp made launchable. A playbook lands unchanged
// and can be scheduled, surveyed and audited exactly like a native runbook,
// which is the whole promise of the Ansible superset positioning; it
// converts to a native collection later, and the template it was launched
// from keeps its name, its access and its history when it does.
//
// It is reachable only because internal/launch/builtins.go blank-imports it
// (FAILURE_PATTERNS.md #52).
package playbook

import (
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// Kind is this kind's registry key.
const Kind = "playbook"

// Adapter names the execution adapter that runs it: internal/adapters/legacy,
// which invokes a real ansible-playbook inside a sandbox.
const Adapter = "legacy"

func init() {
	launch.MustRegister(launch.Descriptor{
		Kind:       Kind,
		Label:      "Playbook",
		BadgeClass: "badge-changed",
		Summary:    "An unconverted Ansible playbook, run through the sandboxed legacy adapter.",
		Adapter:    Adapter,
		Fields: []launch.FieldSpec{
			{
				Name: "limit", Type: launch.TypeString,
				Label: "LIMIT",
				Help:  "Ansible's own --limit. Narrows this run to a subset of the inventory.",
			},
			{
				Name: "verbosity", Type: launch.TypeInt, Min: 0, Max: 4,
				Label: "VERBOSITY",
				Help:  "Ansible's -v through -vvvv.",
			},
			{
				Name: "forks", Type: launch.TypeInt, Min: 1, Max: 1000,
				Label: "FORKS",
				Help:  "Ansible's --forks: how many hosts are worked on at once.",
			},
			{
				Name: "timeout", Type: launch.TypeInt, Min: 0, Max: 86400,
				Label: "TIMEOUT",
				Help:  "Seconds before the run is abandoned. Zero means no timeout.",
			},
			{
				Name: "extra_vars", Type: launch.TypeMap,
				Label: "EXTRA VARIABLES",
				Help:  "Ansible's --extra-vars. These merge across layers rather than replacing.",
			},
			{
				Name: "labels", Type: launch.TypeStringList,
				Label: "LABELS",
				Help:  "Free-text markers for finding this run later.",
			},
			// The two fields this kind has and the native one does not,
			// which is the concrete reason the field set is per kind rather
			// than a fixed set of columns. Tags are an Ansible concept with
			// no native equivalent, and declaring them on both kinds would
			// put a control on the runbook launch form that nothing reads.
			{
				Name: "tags", Type: launch.TypeStringList,
				Label: "TAGS",
				Help:  "Ansible's --tags: run only the tasks carrying these tags.",
			},
			{
				Name: "skip_tags", Type: launch.TypeStringList,
				Label: "SKIP TAGS",
				Help:  "Ansible's --skip-tags: run everything except the tasks carrying these tags.",
			},
		},
		ValidateDefinition: validatePlaybookPath,
	})
}

// validatePlaybookPath checks a playbook reference is a path that stays
// inside the project it belongs to.
//
// This is the boundary, and it is the one that matters most in this file.
// The reference is a filesystem path supplied by whoever may create a
// template, and it is handed to a process that reads it. An absolute path
// or a traversal would let a template author read any file the runner can
// reach, which is a class of value this repository has already had to fix
// once at a different boundary (FAILURE_PATTERNS.md #78).
func validatePlaybookPath(reference string) error {
	raw := strings.TrimSpace(reference)
	switch {
	case raw == "":
		return fmt.Errorf("a playbook template needs a playbook path")
	case len(raw) > 1024:
		return fmt.Errorf("playbook path is longer than 1024 characters")
	case strings.ContainsRune(raw, 0):
		return fmt.Errorf("playbook path contains a null byte")
	case strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, `\`):
		return fmt.Errorf("playbook path %q is absolute: a playbook is named relative to its own project", raw)
	case strings.Contains(raw, `\`):
		return fmt.Errorf("playbook path %q uses backslashes: paths here are forward-slashed on every platform", raw)
	}

	// Cleaned and compared, rather than searched for "..", so a path that
	// merely contains those two characters inside a directory name is
	// allowed while one that actually climbs is not.
	cleaned := path.Clean(raw)
	if cleaned != raw {
		return fmt.Errorf("playbook path %q is not in its simplest form: write it as %q", raw, cleaned)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("playbook path %q climbs out of its project", raw)
	}

	if ext := path.Ext(cleaned); ext != ".yml" && ext != ".yaml" {
		// Refused rather than assumed, because the alternative is handing
		// ansible-playbook a file that is not a playbook and reporting
		// whatever it says about it as a job failure.
		return fmt.Errorf("playbook path %q does not name a YAML file", raw)
	}
	return nil
}

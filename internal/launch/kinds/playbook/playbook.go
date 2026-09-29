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
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pbsource "github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
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
				Help:  "How many of this job's hosts run at once; the next starts as each finishes. Also passed to ansible-playbook as --forks. Empty runs them all at once.",
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
			// The two fields this kind has and the native one does not yet,
			// which is the concrete reason the field set is per kind rather
			// than a fixed set of columns. Native runbooks have tags too
			// (engine.Select), but the Runner does not apply a filter yet,
			// so declaring them on the runbook kind would put a control on
			// its launch form that nothing reads.
			//
			// Named job_tags rather than tags, matching AWX's own field name
			// rather than this project's earlier choice, because an import
			// maps AWX's ask_tags_on_launch onto Prompts by exact field name
			// and a mismatched name made that mapping lossy
			// (AWX_PARITY_ROADMAP.md Section 1.2).
			{
				Name: "job_tags", Type: launch.TypeStringList,
				Label: "JOB TAGS",
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

// validatePlaybookPath checks a playbook reference is a project-relative
// path the resolver could resolve.
//
// The grammar itself is internal/playbook.ValidateReference, imported
// rather than restated, and both halves of that sentence matter. Imported,
// because two independent statements of one contract do not drift apart
// eventually, they start apart: these two did, and every definition a
// template could store was one no dispatch could resolve.
//
// And a PATH, because that is what an Ansible playbook reference is. AWX
// stores "tripplite_python/tripplite_config.yml" here, populated from a
// scan of the project's own tree. An earlier pass reconciled the two
// grammars onto the flat-id half instead, which made the platform unable
// to name any playbook a real customer owns: internally consistent, and
// useless (FAILURE_PATTERNS.md #113).
//
// Shape only, not existence, the same split the runbook kind draws: this
// package imports the grammar, never the filesystem. Whether the path
// names a real playbook is checked where the source lives, at template
// create (the launch catalog) and again at fan-out (the dispatch worker's
// definition source).
func validatePlaybookPath(reference string) error {
	if strings.TrimSpace(reference) == "" {
		return fmt.Errorf("a playbook template needs a playbook path")
	}
	return pbsource.ValidateReference(reference)
}

package playbook_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
)

// This file covers the playbook kind's definition rule, which is a security
// boundary rather than a formatting preference.
//
// The reference is a filesystem path supplied by whoever may create a
// template, and it is handed to a process that reads it. An absolute path
// or a traversal would let a template author read any file the runner can
// reach. FAILURE_PATTERNS.md #78 is the same class of value reaching
// filepath.Join at a different boundary, fixed there; this is the boundary
// a template crosses.

// validate reaches the rule the way the platform does: through the
// registered descriptor, not by calling an unexported function. A rule that
// is correct but not wired to the descriptor protects nothing.
func validate(t *testing.T, reference string) error {
	t.Helper()

	d, ok := launch.Lookup(playbook.Kind)
	if !ok {
		t.Fatalf("the %q kind is not registered, so nothing can launch one", playbook.Kind)
	}
	if d.ValidateDefinition == nil {
		t.Fatal("the playbook kind declares no definition rule, so any path would be accepted")
	}
	return d.ValidateDefinition(reference)
}

func TestPlaybookPath_RefusesWhatWouldEscapeItsProject(t *testing.T) {
	refused := map[string]string{
		"empty":              "",
		"absolute":           "/etc/shadow",
		"absolute windows":   `\\server\share\play.yml`,
		"traversal":          "../../../etc/passwd.yml",
		"traversal in place": "playbooks/../../secrets.yml",
		"backslashes":        `playbooks\patch.yml`,
		"null byte":          "playbooks/patch.yml\x00.txt",
		"not yaml":           "playbooks/patch.sh",
		"no extension":       "playbooks/patch",
		"unclean":            "playbooks/./patch.yml",
		"trailing slash":     "playbooks/patch.yml/",
	}

	for name, reference := range refused {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, reference); err == nil {
				t.Errorf("the playbook kind accepted %q", reference)
			}
		})
	}
}

func TestPlaybookPath_AcceptsAnOrdinaryPlaybook(t *testing.T) {
	accepted := []string{
		"site.yml",
		"playbooks/patch.yml",
		"playbooks/network/edge.yaml",
		// A directory whose name contains dots is not a traversal, and
		// refusing it would be the over-broad rule a "contains .." check
		// produces.
		"playbooks/v1.2.3/patch.yml",
	}

	for _, reference := range accepted {
		t.Run(reference, func(t *testing.T) {
			if err := validate(t, reference); err != nil {
				t.Errorf("the playbook kind refused %q: %v", reference, err)
			}
		})
	}
}

func TestPlaybookKind_IsRegisteredWithItsOwnAdapterAndFields(t *testing.T) {
	d, ok := launch.Lookup(playbook.Kind)
	if !ok {
		t.Fatalf("the %q kind is not registered", playbook.Kind)
	}

	if d.Adapter != playbook.Adapter {
		t.Errorf("the playbook kind runs on adapter %q, want %q", d.Adapter, playbook.Adapter)
	}

	// tags and skip_tags are the concrete reason the field set is per kind
	// rather than a fixed set of columns: they are Ansible concepts with no
	// native equivalent, and declaring them on the runbook kind would put a
	// control on its launch form that nothing reads.
	for _, name := range []string{"tags", "skip_tags"} {
		if _, ok := d.Field(name); !ok {
			t.Errorf("the playbook kind has no %q field", name)
		}
	}
}

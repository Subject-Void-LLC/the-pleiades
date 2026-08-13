package playbook_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	pbsource "github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
)

// This file covers the playbook kind's definition rule, which is a security
// boundary rather than a formatting preference: the reference reaches a
// filesystem path through filepath.Join in the resolver, and it is supplied
// by whoever may create a template (FAILURE_PATTERNS.md #78 is the same
// class of value at a different boundary).
//
// The rule is the RESOLVER'S own grammar, imported, and it is a PATH
// grammar because that is what an Ansible playbook reference is: AWX
// stores "tripplite_python/tripplite_config.yml" in this field, populated
// from a scan of the project's tree.
//
// Two earlier versions of this file were wrong in opposite directions.
// First the kind and the resolver stated the contract independently and
// disagreed completely (paths here, flat ids there), so nothing savable
// could run. Then the two were reconciled onto the FLAT-ID half, and this
// file's own test list grew entries asserting that "site.yml" and
// "playbooks/patch" must be refused: the tidy answer, and one that made
// the platform unable to name any playbook a real customer owns
// (FAILURE_PATTERNS.md #113).

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
		t.Fatal("the playbook kind declares no definition rule, so any reference would be accepted")
	}
	return d.ValidateDefinition(reference)
}

func TestPlaybookDefinition_RefusesWhatTheResolverWouldRefuse(t *testing.T) {
	refused := map[string]string{
		"empty":              "",
		"absolute":           "/etc/shadow",
		"absolute windows":   `\\server\share\play.yml`,
		"traversal":          "../../../etc/passwd.yml",
		"traversal in place": "playbooks/../../secrets.yml",
		"backslashes":        `playbooks\patch.yml`,
		"null byte":          "playbooks/patch.yml\x00.txt",
		"unclean":            "playbooks/./patch.yml",
		"trailing slash":     "playbooks/patch.yml/",
		// Not YAML: handing ansible-playbook a file that is not a
		// playbook reports its complaint as a job failure.
		"not yaml":     "playbooks/patch.sh",
		"no extension": "playbooks/patch",
		"too long":     strings.Repeat("a", 1030) + ".yml",
	}

	for name, reference := range refused {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, reference); err == nil {
				t.Errorf("the playbook kind accepted %q", reference)
			}
		})
	}
}

func TestPlaybookDefinition_AcceptsWhatAWXActuallyStores(t *testing.T) {
	for _, reference := range []string{
		// The exact shape a production Ascender job template carries.
		"tripplite_python/tripplite_config.yml",
		"site.yml",
		"playbooks/network/edge.yaml",
		"upgrade-ios.yml",
		// A directory whose name contains dots is not a traversal, and
		// refusing it would be the over-broad rule a "contains .." check
		// produces.
		"playbooks/v1.2.3/patch.yml",
	} {
		t.Run(reference, func(t *testing.T) {
			if err := validate(t, reference); err != nil {
				t.Errorf("the playbook kind refused %q: %v", reference, err)
			}
		})
	}
}

// TestPlaybookDefinition_AgreesWithTheResolver is the regression pin for
// the disjoint-grammar defect itself: every reference this kind accepts
// must be one the resolver's grammar accepts, and vice versa. It holds by
// construction now (the kind delegates to pbsource.ValidateReference), and this test
// is what fails if somebody ever unbundles them again.
func TestPlaybookDefinition_AgreesWithTheResolver(t *testing.T) {
	corpus := []string{
		"", "site", "site.yml", "site.yaml", "playbooks/site", "playbooks/site.yml",
		"tripplite_python/tripplite_config.yml", "a_b-c.yml",
		"../climb.yml", `back\slash.yml`, "x", "deep/a/b/c/play.yaml",
		strings.Repeat("y", 1020) + ".yml", strings.Repeat("y", 1030) + ".yml",
		"UPPER.YML", "dot.ted.yml", "spa ce.yml", "trailing/",
	}
	for _, reference := range corpus {
		kindAccepts := validate(t, reference) == nil
		resolverAccepts := pbsource.ValidReference(strings.TrimSpace(reference))
		if kindAccepts != resolverAccepts {
			t.Errorf("reference %q: kind accepts=%v, resolver accepts=%v; the two grammars have diverged again",
				reference, kindAccepts, resolverAccepts)
		}
	}
}

// TestPlaybookDefinition_NamesTheMistake pins the diagnosis messages,
// because "invalid playbook path" tells an author nothing they can act
// on.
func TestPlaybookDefinition_NamesTheMistake(t *testing.T) {
	for _, tc := range []struct{ reference, want string }{
		{"playbooks/patch.sh", "YAML"},
		{"/etc/shadow.yml", "absolute"},
		{"../secrets.yml", "climbs"},
		{"playbooks/./patch.yml", "playbooks/patch.yml"},
	} {
		err := validate(t, tc.reference)
		if err == nil {
			t.Errorf("%q was accepted", tc.reference)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("refusing %q says %q, which does not mention %q", tc.reference, err, tc.want)
		}
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

	// job_tags and skip_tags are the concrete reason the field set is per
	// kind rather than a fixed set of columns: they are Ansible concepts
	// with no native equivalent, and declaring them on the runbook kind
	// would put a control on its launch form that nothing reads.
	for _, name := range []string{"job_tags", "skip_tags"} {
		if _, ok := d.Field(name); !ok {
			t.Errorf("the playbook kind has no %q field", name)
		}
	}
}

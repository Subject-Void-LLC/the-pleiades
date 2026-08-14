package managed_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// awxManagedNamespaces is every namespace AWX registers as a managed
// credential type, from the two entry-point groups in awx-plugins'
// pyproject.toml: awx_plugins.managed_credentials and its .supported
// sibling.
//
// It is written down here rather than derived, because the point of the
// test below is to compare this platform's catalog against AWX's, and a
// list derived from our own catalog would compare it against itself. When
// AWX adds a type, this list gains an entry and the test names the
// namespace nobody has decided about yet.
var awxManagedNamespaces = []string{
	// awx_plugins.managed_credentials
	"ssh", "scm", "vault", "controller", "kubernetes_bearer_token",
	"registry", "galaxy_api_token", "net",
	// awx_plugins.managed_credentials.supported
	"aws", "openstack", "vmware", "satellite6", "bitbucket_dc_token",
	"gce", "azure_rm", "github_token", "gitlab_token", "insights",
	"rhv", "gpg_public_key", "terraform", "hcp_terraform",
}

// TestTheCatalogCoversEveryAWXManagedType is the completeness statement.
//
// Shipped plus declared-not-implemented must be exactly AWX's own set, in
// both directions. A namespace AWX has and we do not mention at all is the
// failure that matters: an operator importing it gets "no such credential
// type", which reads as a typo in their own export rather than as a gap in
// this platform. A namespace we claim and AWX does not have is the other
// direction, and it means a managed type has been invented under a name an
// AWX export could later collide with.
func TestTheCatalogCoversEveryAWXManagedType(t *testing.T) {
	t.Parallel()

	ours := map[string]string{}
	for _, ct := range managed.Types() {
		ours[ct.Namespace] = "shipped"
	}
	for _, ni := range managed.DeclaredNotImplemented() {
		if where, dup := ours[ni.Namespace]; dup {
			t.Errorf("namespace %q is both %s and declared-not-implemented", ni.Namespace, where)
		}
		ours[ni.Namespace] = "declared"
	}

	awx := make(map[string]bool, len(awxManagedNamespaces))
	for _, ns := range awxManagedNamespaces {
		awx[ns] = true
		if _, known := ours[ns]; !known {
			t.Errorf("AWX manages %q and this catalog neither ships it nor declares it: an import of that type would report an unknown namespace", ns)
		}
	}
	for ns := range ours {
		if !awx[ns] {
			t.Errorf("this catalog claims the namespace %q, which AWX does not manage", ns)
		}
	}
}

// TestEveryShippedTypeParsesAndValidates is what licenses mustParse's
// panic. The data is compiled in, so if it validates here it validates in
// every binary built from this tree.
func TestEveryShippedTypeParsesAndValidates(t *testing.T) {
	t.Parallel()

	eng := render.New()
	types := managed.Types()
	if len(types) == 0 {
		t.Fatal("the catalog ships no credential types at all")
	}

	for _, ct := range types {
		t.Run(ct.Namespace, func(t *testing.T) {
			t.Parallel()

			if err := ct.Validate(eng); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !ct.Managed {
				t.Error("a shipped type is not marked managed, so nothing would stop an operator editing it")
			}
			if ct.Description == "" {
				t.Error("no description: this is the only text the credential form shows about what the type is for")
			}
		})
	}
}

// TestShippedTypesAreSortedAndUnique covers the ordering the reconcile
// depends on to produce a startup log that reads the same way twice.
func TestShippedTypesAreSortedAndUnique(t *testing.T) {
	t.Parallel()

	types := managed.Types()
	namespaces := make([]string, 0, len(types))
	for _, ct := range types {
		namespaces = append(namespaces, ct.Namespace)
	}

	if !sort.StringsAreSorted(namespaces) {
		t.Errorf("Types() = %v, want sorted by namespace", namespaces)
	}
	for i := 1; i < len(namespaces); i++ {
		if namespaces[i] == namespaces[i-1] {
			t.Errorf("Types() lists %q twice", namespaces[i])
		}
	}
}

// TestDeclaredNotImplementedNamesWhatIsMissing checks that every entry
// carries enough for an operator to act on, since the whole reason these
// are declared rather than absent is that the reason is the useful part.
func TestDeclaredNotImplementedNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	for _, ni := range managed.DeclaredNotImplemented() {
		t.Run(ni.Namespace, func(t *testing.T) {
			t.Parallel()

			if ni.Name == "" {
				t.Error("no display name, so a report would not read the way the operator's own AWX does")
			}
			if !ni.Kind.Valid() {
				t.Errorf("kind %q is not in the closed vocabulary", ni.Kind)
			}
			if ni.Reason == "" {
				t.Error("no reason")
			}
			if len(ni.Detail) < 40 {
				t.Errorf("detail %q is too short to name the specific thing that is missing", ni.Detail)
			}
		})
	}
}

// TestCheckNamespaceDistinguishesUnknownFromUnimplemented covers the
// asymmetry that makes CheckNamespace useful to an import: an unknown
// namespace is a custom credential type, which is the case this platform
// supports best, so it is not an error.
func TestCheckNamespaceDistinguishesUnknownFromUnimplemented(t *testing.T) {
	t.Parallel()

	if err := managed.CheckNamespace("custom_api_token"); err != nil {
		t.Errorf("CheckNamespace(a custom namespace) = %v, want nil: a custom type is the supported case", err)
	}
	if err := managed.CheckNamespace("aws"); err != nil {
		t.Errorf("CheckNamespace(%q) = %v, want nil", "aws", err)
	}

	err := managed.CheckNamespace("gce")
	if !errors.Is(err, managed.ErrNotImplemented) {
		t.Fatalf("CheckNamespace(%q) = %v, want one matching ErrNotImplemented", "gce", err)
	}
	// The message has to carry the reason, because "not implemented" alone
	// tells an operator nothing about whether to wait, work around it, or
	// convert the playbook.
	for _, want := range []string{"gce", "Google Compute Engine", "python"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CheckNamespace(gce) message %q does not mention %q", err, want)
		}
	}
}

// TestNoShippedTypeUsesAnInjectorTheNativePathRefuses is a real constraint
// rather than a style check.
//
// A managed type is reconciled into every deployment, so one declaring an
// env or file injector would be offered on templates whose kind runs on the
// native path and refused at bind time. Shipping a type that half the
// platform cannot use is worse than declaring it not implemented, because
// the refusal arrives after somebody has filled in a credential.
func TestNoShippedTypeUsesAnInjectorTheNativePathRefuses(t *testing.T) {
	t.Parallel()

	for _, ct := range managed.Types() {
		if len(ct.Injectors.Env) == 0 && len(ct.Injectors.File) == 0 {
			continue
		}
		// The cloud types are legacy-path types by construction: an Ansible
		// module reads the environment and there is nowhere else it looks.
		// The check is that this is a deliberate property of the KIND
		// rather than something that drifted in.
		if ct.Kind != credtype.KindCloud {
			t.Errorf("type %q is kind %q and injects env or file, which the native path refuses: only cloud types should need that",
				ct.Namespace, ct.Kind)
		}
	}
}

// TestHasAnswersTheYesOrNoQuestion covers the accessor the import command
// classifies with, in both directions and against a declared-but-unbuilt
// namespace, which is the case a naive "do we know this name" check would
// get wrong.
func TestHasAnswersTheYesOrNoQuestion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		namespace string
		want      bool
	}{
		{"aws", true},
		{"ssh", true},
		// Declared and not implemented is NOT shipped. An import that
		// treated it as shipped would report nothing wrong and then find
		// no type to bind against.
		{"gce", false},
		{"custom_api_token", false},
	}

	for _, tt := range tests {
		if got := managed.Has(tt.namespace); got != tt.want {
			t.Errorf("Has(%q) = %v, want %v", tt.namespace, got, tt.want)
		}
	}
}

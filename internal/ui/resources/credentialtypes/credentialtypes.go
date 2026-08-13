// Package credentialtypes is the Credential Types view, registered as declared.
//
// The administrative surface behind every bound credential: an inputs
// schema saying what secrets a type holds, and injectors saying how they
// reach a run (environment, extra variables, files). A customer's
// playbooks read the variables these inject, which is why this is the
// gap that decides whether a migration is possible at all.
//
// Declared rather than omitted, for the reason governance.go gives: the
// navigation is a statement about the shape of the product, and an AWX
// operator evaluating this platform reads the sidebar before anything
// else. Declared rather than faked, because a view rendering an empty
// table over nothing is indistinguishable from a working view with no
// records, which is the ambiguity this project has shipped twice.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package credentialtypes

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "credential-types"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "kind", Label: "KIND", Kind: view.KindBadge, InList: true},
	{Name: "inputs", Label: "INPUTS", Kind: view.KindText, InList: true},
	{Name: "injectors", Label: "INJECTORS", Kind: view.KindText, InList: true},
	{Name: "managed", Label: "MANAGED", Kind: view.KindBadge, InList: true},
}

// Register adds the declared Credential Types view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Credential Types",
		NavLabel: "CREDENTIAL TYPES",
		NavOrder: 140,
		NavGroup: view.NavGroupAdministration,
		Summary:  "What a credential holds, and how its secrets reach the automation.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}

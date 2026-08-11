// Package credentials is the Credentials view, registered as declared.
//
// It is declared for two independent reasons, and either alone would be
// enough. The first is that it has no backing port: PLAN Section 17.4's
// real CredentialStore -- rotation, Vault, PFX -- is unbuilt, and
// internal/credential is the Walk tier's file-backed store rather than a
// control-plane one.
//
// The second is the one worth writing down. Even a name-only listing would
// be a disclosure: the set of credential names in a deployment tells a
// reader which vendors are present, which devices are managed by what, and
// which accounts exist to be attacked. That is reconnaissance, and it does
// not become safe by omitting the secret values. So this view will not list
// names either when it is implemented -- it will be built around a
// specific, authorized lookup rather than a browsable catalog.
//
// The whole cost of a declared view is this file. The shared template
// renders the honest panel from Status alone.
package credentials

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "credentials"

// fields declare the shape a real implementation would fill.
//
// There is deliberately no field for a secret value, and there never will
// be. A Field renders into a table, a form and a detail list, so declaring
// one for a secret would be three separate places it could reach a page.
var fields = []view.Field{
	{Name: "reference", Label: "REFERENCE", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "kind", Label: "KIND", Kind: view.KindText, InList: true},
	{Name: "rotated", Label: "LAST ROTATED", Kind: view.KindTimestamp, InList: true},
}

// Register adds the declared Credentials view.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Credentials",
		NavLabel: "CREDENTIALS",
		NavOrder: 60,
		Summary:  "Managed secrets and their rotation state.",
		Status:   view.StatusDeclared,
		IDField:  "reference",
		Fields:   fields,
	})
}

// Package labels is the Labels view, registered as declared.
//
// It is registered rather than omitted because the shape is real and named
// in AWX_PARITY_ROADMAP.md B3: a first-class entity with its own view and a
// filter, replacing the free-text label list a runbook and a launch field
// each already carry (internal/runbook.Runbook.Labels, and each launch
// kind's own "labels" FieldSpec). It is declared rather than implemented
// because nothing backs it as an entity yet -- no ent schema, no store, no
// endpoint -- and a view that rendered an empty table over nothing would be
// indistinguishable from a working view with no records yet, the ambiguity
// this project has shipped twice and the reason view.Register refuses a
// declared descriptor that carries handlers.
//
// The whole cost of a declared view is this file. The shared template
// renders the honest panel from Status alone.
package labels

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "labels"

// fields declare the shape a real implementation would fill. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true},
	{Name: "templates", Label: "TEMPLATES", Kind: view.KindText, InList: true},
}

// Register adds the declared Labels view.
//
// It takes no dependencies, because it has none: there is no port to adapt.
// When B3 builds the real entity, this signature grows and the descriptor
// gains handlers and a Status of implemented -- and nothing else in the UI
// changes.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Labels",
		NavLabel: "LABELS",
		// Just after Templates, where AWX puts it: labels exist to organize
		// and filter the thing an operator touches daily, so the nav entry
		// for finding them belongs beside it rather than off in
		// Administration with the settings nobody visits often.
		NavOrder: 45,
		NavGroup: view.NavGroupResources,
		Summary:  "Free-text markers for finding and filtering templates and runs.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
		// No Ops. There is no endpoint to name, and naming one that does
		// not exist is precisely what Register refuses.
	})
}

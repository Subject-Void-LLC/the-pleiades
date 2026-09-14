// This file is the Inputs tab of a credential type: the read-only table of
// the fields a credential of the type holds.
//
// It shows what the list column can only summarise -- each input's id,
// label, type, and whether it is secret and required -- on the type's own
// detail page. Authoring an input is a metadata write and lands separately;
// see the package doc comment for why the injector document in particular
// stays off a free-form control.
package credentialtypes

import (
	"context"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// inputsTitle is the section heading.
const inputsTitle = "Inputs"

// inputColumns are the read-only columns the Inputs table renders. The
// secret flag is a badge because it is the one column that changes what a
// reader is responsible for; the rest are plain text.
var inputColumns = []view.Field{
	{Name: "id", Label: "ID", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true},
	{Name: "type", Label: "TYPE", Kind: view.KindText, InList: true},
	{
		Name: "secret", Label: "SECRET", Kind: view.KindBadge, InList: true,
		BadgeClass: secretClass,
	},
	{Name: "required", Label: "REQUIRED", Kind: view.KindText, InList: true},
}

// secretClass marks the secret inputs, the ones whose value is encrypted at
// rest and never shown again.
func secretClass(value string) string {
	if value == "yes" {
		return "badge-changed"
	}
	return "badge-neutral"
}

// inputsSection is the Inputs tab: the type's input schema as a table.
func inputsSection(store credstore.TypeReader) view.Section {
	return view.Section{
		Title:   inputsTitle,
		Summary: "The fields a credential of this type holds. A secret input is encrypted at rest and never shown again.",
		Status:  view.StatusImplemented,
		Fields:  inputColumns,
		Empty:   "This credential type has no inputs yet.",
		Rows:    inputRows(store),
	}
}

// inputRows projects a type's input schema onto section rows. A row's id is
// the input's own id.
func inputRows(store credstore.TypeReader) func(context.Context, string) ([]view.Row, error) {
	return func(ctx context.Context, parentID string) ([]view.Row, error) {
		// Empty parent: an input belongs to a type, not to the collection,
		// so the section on the list page shows its empty state rather than
		// every input in the system.
		if parentID == "" {
			return nil, nil
		}
		numeric, err := strconv.Atoi(parentID)
		if err != nil {
			return nil, nil
		}
		ct, err := store.GetType(ctx, numeric)
		if err != nil {
			return nil, err
		}

		required := make(map[string]bool, len(ct.Inputs.Required))
		for _, id := range ct.Inputs.Required {
			required[id] = true
		}

		out := make([]view.Row, 0, len(ct.Inputs.Fields))
		for _, f := range ct.Inputs.Fields {
			out = append(out, view.Row{
				ID: f.ID,
				Cells: view.Cells{
					"id":       f.ID,
					"label":    f.Label,
					"type":     inputTypeLabel(f.Type),
					"secret":   yesNo(f.Secret),
					"required": yesNo(required[f.ID]),
				},
			})
		}
		return out, nil
	}
}

// inputTypeLabel names an input's type, defaulting an empty one to string,
// which is how the schema itself reads it.
func inputTypeLabel(t credtype.InputType) string {
	if t == credtype.InputBoolean {
		return "boolean"
	}
	return "string"
}

// yesNo renders a boolean cell.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

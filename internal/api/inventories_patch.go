// This file is the body PATCH /inventories/{id} accepts, and how it is folded
// over the stored inventory.
//
// PATCH is a partial update, as the template, access, project and credential
// PATCHes are (templates_patch.go, access_patch.go, projects_patch.go,
// credentials_patch.go): a field the body leaves out keeps its stored value
// and a field it names is applied. An empty description clears it, and an
// empty member list empties that half of the membership. The route used to
// decode its create body, so a name or description the body left out was
// written back empty: a PATCH naming only the devices answered 500 for the
// name it blanked, and one naming only the name erased the description
// (FAILURE_PATTERNS 1075).
//
// The rules are the shared ones in patch.go. A null on any field is refused,
// naming it: a null member list used to read as absent and answer 200. An
// unknown key, a read-only one such as owner or device_count among them, a
// field named twice and a body that is not an object are malformed. The
// organization is fixed: a different one is refused, naming it, where it used
// to be ignored with a 200 (FAILURE_PATTERNS 1055), and the stored one is
// accepted as an echo.
//
// The handler hands the store a fold, which the store runs on the inventory
// it reads under the row's write lock and then writes (SetStore.UpdateWith),
// holding the result to the rules a create is held to: a name that is not
// blank, no NUL, members that exist and belong to the inventory's own
// organization. Two PATCHes of one inventory are serialized by that lock,
// and each folds over what the other wrote, so a PATCH that does not name
// the members cannot put back a member another removed. A list a PATCH does
// name replaces the stored one whole, so of two PATCHes naming the same
// field the second's value wins, even when its caller read the inventory
// before the first; there is no version check.
package api

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// inventoryNoun names an inventory in the shared PATCH refusals (patch.go).
const inventoryNoun = "an inventory"

// inventoryPatchDTO is the body PATCH /inventories/{id} accepts: every field
// of the create body, each a pointer so an absent key keeps its stored value.
type inventoryPatchDTO struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	// Organization is fixed: moving an inventory between tenants would
	// silently re-scope every role binding pointing at it, which is a
	// migration rather than an edit. It is accepted only as the stored value;
	// see fixedFieldRefusal.
	Organization *int `json:"organization"`

	// Groups and Devices, when named, replace that half of the membership
	// wholesale, so [] empties it.
	Groups  *[]int `json:"groups"`
	Devices *[]int `json:"devices"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *inventoryPatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields inventoryPatchDTO
	decoded, nulls, err := decodePatch[fields](data, inventoryNoun)
	if err != nil {
		return err
	}
	*d = inventoryPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// fixedFieldRefusal is the 400 message for a body that asks to move the
// inventory to another organization, or "" when it does not.
func (d inventoryPatchDTO) fixedFieldRefusal(stored inventory.Set) string {
	if d.Organization != nil && *d.Organization != stored.OrganizationID {
		return fixedFieldMessage("organization", inventoryNoun, "create the inventory in the other organization instead")
	}
	return ""
}

// fold is the change the store runs on the inventory it read under the
// row's lock: refuse a move to another organization, else apply the fields
// the body named.
func (d inventoryPatchDTO) fold(stored inventory.Set) (inventory.Set, error) {
	if refusal := d.fixedFieldRefusal(stored); refusal != "" {
		return inventory.Set{}, patchRefusal(refusal)
	}
	patchField(&stored.Name, d.Name)
	patchField(&stored.Description, d.Description)
	patchField(&stored.GroupIDs, d.Groups)
	patchField(&stored.DeviceIDs, d.Devices)
	return stored, nil
}

// This file is the bodies the four access PATCH routes accept
// (organizations, teams, contacts and role bindings), and how each is folded
// over the stored record.
//
// Each is a partial update, as PATCH /templates/{id} is (templates_patch.go):
// a field the body leaves out keeps its stored value and a field it names is
// applied. An empty value clears a field that can be empty, and false is a
// real value, so {"frozen": false} is the explicit unfreeze. Each route used
// to decode its create body instead, so a field the body left out was written
// back empty: a PATCH naming only an organization's name unfroze it and
// erased its change-management fields (FAILURE_PATTERNS 1075), and one naming
// only a grant's role sent its effect empty.
//
// The rules are the template PATCH's, through the shared helpers in patch.go.
// A null on any of a resource's own fields is refused, naming the field,
// because a pointer alone reads a null as absent (see templatePatchDTO for
// why refusing beats either reading). An unknown key, and a body that is not
// an object, are refused as malformed. And the fields no update can change
// are refused when they differ from the stored ones and accepted when they
// equal them: a team's organization, a contact's owner (organization and
// team), and a grant's team and scope (team, scope_type and scope_id). Those
// were decoded and ignored, answering 200 for a re-point that changed nothing
// (FAILURE_PATTERNS 1055).
//
// The handler reads the stored record, folds the body over it and hands the
// whole merged record to the same store method a full update calls, so every
// check the store makes still runs on what would be saved: a blank name, a
// taken one, an unknown marking, role or effect, a contact nobody can reach,
// and the last system-scope grant edited into a deny. The store methods are
// unchanged; the UI's edit forms call them with full records.
//
// Concurrent PATCHes are last-writer-wins, as they were: each reads the
// stored record, folds its fields over it and writes the result, with no
// version check, so of two at once the second's write covers the first's for
// every field.
package api

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// The nouns the shared PATCH refusals name each resource by (patch.go).
const (
	organizationNoun = "an organization"
	teamNoun         = "a team"
	contactNoun      = "a contact"
	bindingNoun      = "a role binding"
)

// patchField copies *value into *dst when the body named the field, and
// leaves *dst as stored when it did not.
func patchField[T any](dst *T, value *T) {
	if value != nil {
		*dst = *value
	}
}

// organizationPatchDTO is the body PATCH /organizations/{id} accepts: every
// field of the create body, each a pointer so an absent key keeps its stored
// value. Like the create body it carries no attestation, so a body naming one
// is refused as an unknown key.
type organizationPatchDTO struct {
	Name           *string                `json:"name"`
	Description    *string                `json:"description"`
	Classification *access.Classification `json:"classification"`
	ChangeWindow   *string                `json:"change_window"`
	Frozen         *bool                  `json:"frozen"`
	FreezeReason   *string                `json:"freeze_reason"`
	CostCentre     *string                `json:"cost_centre"`
	TicketKey      *string                `json:"ticket_key"`
	CMDBID         *string                `json:"cmdb_id"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *organizationPatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields organizationPatchDTO
	decoded, nulls, err := decodePatch[fields](data, organizationNoun)
	if err != nil {
		return err
	}
	*d = organizationPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// apply folds the fields the body named over stored. The attestation is
// carried along untouched, and the store does not write it on an update.
func (d organizationPatchDTO) apply(stored access.Organization) access.Organization {
	patchField(&stored.Name, d.Name)
	patchField(&stored.Description, d.Description)
	patchField(&stored.Classification, d.Classification)
	patchField(&stored.ChangeWindow, d.ChangeWindow)
	patchField(&stored.Frozen, d.Frozen)
	patchField(&stored.FreezeReason, d.FreezeReason)
	patchField(&stored.CostCentre, d.CostCentre)
	patchField(&stored.TicketKey, d.TicketKey)
	patchField(&stored.CMDBID, d.CMDBID)
	return stored
}

// teamPatchDTO is the body PATCH /teams/{id} accepts.
type teamPatchDTO struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	// Organization is fixed: moving a team between tenants would silently
	// re-scope every grant it holds, which is a migration rather than an
	// edit. It is accepted only as the stored value; see fixedFieldRefusal.
	Organization *int `json:"organization"`

	// Users, when named, replaces the membership wholesale, so [] empties it.
	Users *[]int `json:"users"`

	// nulls is organizationPatchDTO.nulls for this body.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *teamPatchDTO) UnmarshalJSON(data []byte) error {
	type fields teamPatchDTO
	decoded, nulls, err := decodePatch[fields](data, teamNoun)
	if err != nil {
		return err
	}
	*d = teamPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// fixedFieldRefusal is the 400 message for a body that asks to move the team
// to another organization, or "" when it does not.
func (d teamPatchDTO) fixedFieldRefusal(stored access.Team) string {
	if d.Organization != nil && *d.Organization != stored.OrganizationID {
		return fixedFieldMessage("organization", teamNoun, "create a team in the other organization instead")
	}
	return ""
}

// apply folds the fields the body named over stored.
func (d teamPatchDTO) apply(stored access.Team) access.Team {
	patchField(&stored.Name, d.Name)
	patchField(&stored.Description, d.Description)
	patchField(&stored.UserIDs, d.Users)
	return stored
}

// contactPatchDTO is the body PATCH /contacts/{id} accepts.
type contactPatchDTO struct {
	Name  *string             `json:"name"`
	Role  *access.ContactRole `json:"role"`
	Email *string             `json:"email"`
	Phone *string             `json:"phone"`
	URL   *string             `json:"url"`
	Notes *string             `json:"notes"`

	// Order is a position, where zero means first, so it is a pointer for
	// the same reason every other field is.
	Order *int `json:"order"`

	// Organization and Team are the owner, which is fixed: moving a contact
	// between owners is deleting one and creating another, and an
	// accountability record that quietly changed what it answers for would
	// defeat the attestation beside it. Each is accepted only as the stored
	// value, zero for the one the contact does not name.
	Organization *int `json:"organization"`
	Team         *int `json:"team"`

	// nulls is organizationPatchDTO.nulls for this body.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *contactPatchDTO) UnmarshalJSON(data []byte) error {
	type fields contactPatchDTO
	decoded, nulls, err := decodePatch[fields](data, contactNoun)
	if err != nil {
		return err
	}
	*d = contactPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// contactRemedy is what a refused change to a contact's owner says to do.
const contactRemedy = "delete the contact and create one for the other owner instead"

// fixedFieldRefusal is the 400 message for a body that asks to change the
// contact's owner, or "" when it does not.
func (d contactPatchDTO) fixedFieldRefusal(stored access.Contact) string {
	switch {
	case d.Organization != nil && *d.Organization != stored.OrganizationID:
		return fixedFieldMessage("organization", contactNoun, contactRemedy)
	case d.Team != nil && *d.Team != stored.TeamID:
		return fixedFieldMessage("team", contactNoun, contactRemedy)
	}
	return ""
}

// apply folds the fields the body named over stored.
func (d contactPatchDTO) apply(stored access.Contact) access.Contact {
	patchField(&stored.Name, d.Name)
	patchField(&stored.Role, d.Role)
	patchField(&stored.Email, d.Email)
	patchField(&stored.Phone, d.Phone)
	patchField(&stored.URL, d.URL)
	patchField(&stored.Notes, d.Notes)
	patchField(&stored.Order, d.Order)
	return stored
}

// bindingPatchDTO is the body PATCH /bindings/{id} accepts. Only the role
// and the effect can change.
type bindingPatchDTO struct {
	Role   *auth.Role   `json:"role"`
	Effect *auth.Effect `json:"effect"`

	// Team, ScopeType and ScopeID say who holds the grant and what it
	// covers, which are fixed: re-pointing a grant is indistinguishable from
	// revoking one and issuing another, and an audit trail showing a grant
	// quietly changing what it covers is one nobody can reconstruct. Each is
	// accepted only as the stored value; a system grant's scope_id is 0.
	Team      *int            `json:"team"`
	ScopeType *auth.ScopeType `json:"scope_type"`
	ScopeID   *int            `json:"scope_id"`

	// nulls is organizationPatchDTO.nulls for this body.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *bindingPatchDTO) UnmarshalJSON(data []byte) error {
	type fields bindingPatchDTO
	decoded, nulls, err := decodePatch[fields](data, bindingNoun)
	if err != nil {
		return err
	}
	*d = bindingPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// bindingRemedy is what a refused re-point of a grant says to do.
const bindingRemedy = "revoke the grant and issue another instead"

// fixedFieldRefusal is the 400 message for a body that asks to re-point the
// grant, or "" when it does not.
func (d bindingPatchDTO) fixedFieldRefusal(stored access.Binding) string {
	switch {
	case d.Team != nil && *d.Team != stored.TeamID:
		return fixedFieldMessage("team", bindingNoun, bindingRemedy)
	case d.ScopeType != nil && *d.ScopeType != stored.ScopeType:
		return fixedFieldMessage("scope_type", bindingNoun, bindingRemedy)
	case d.ScopeID != nil && *d.ScopeID != stored.ScopeID:
		return fixedFieldMessage("scope_id", bindingNoun, bindingRemedy)
	}
	return ""
}

// apply folds the fields the body named over stored.
func (d bindingPatchDTO) apply(stored access.Binding) access.Binding {
	patchField(&stored.Role, d.Role)
	patchField(&stored.Effect, d.Effect)
	return stored
}

// This file is the credential type create, edit and delete path: the
// metadata a type carries (name, description, kind, namespace, owner) and
// the write port behind it.
//
// The store is the authority on whether a type is well formed. CreateType
// and UpdateType both run credtype.CredentialType.Validate against the real
// render engine before persisting, and both refuse a managed row, so this
// file re-implements none of that: it maps a submission onto the domain
// type, lets the store judge it, and turns a refusal into a message against
// the control that caused it. A second opinion here could only disagree
// with the one an API create is already held to.
package credentialtypes

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// kindOptions offers the closed kind vocabulary the binding rule keys on,
// read from credtype rather than restated here so a new kind cannot appear
// in the domain and be missing from the form.
func kindOptions(context.Context) ([]view.Option, error) {
	kinds := credtype.Kinds()
	out := make([]view.Option, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, view.Option{Label: string(k), Value: string(k)})
	}
	return out, nil
}

// orgOptions offers the tenants a custom type may belong to.
func orgOptions(orgs inventory.OrganizationLister) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		found, err := orgs.ListOrganizations(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(found))
		for _, org := range found {
			out = append(out, view.Option{Label: org.Name, Value: strconv.Itoa(org.ID)})
		}
		return out, nil
	}
}

// bindType parses a metadata submission onto the domain type.
//
// Namespace and organization are immutable, so view.Values answers empty
// for them on an edit and the writer's Update reads the stored row for
// both: the namespace because the schema will not change it regardless, the
// owner because moving a type between tenants silently re-scopes who can
// reach it and the store has no parameter for the change.
func bindType(v view.Values) (credstore.CredentialType, view.FieldErrors) {
	errs := view.FieldErrors{}
	ct := credstore.CredentialType{
		CredentialType: credtype.CredentialType{
			Name:        strings.TrimSpace(v.Get("name")),
			Description: strings.TrimSpace(v.Get("description")),
			Kind:        credtype.Kind(strings.TrimSpace(v.Get("kind"))),
		},
	}
	if !v.Editing() {
		ct.Namespace = strings.TrimSpace(v.Get("namespace"))
		orgID, err := strconv.Atoi(strings.TrimSpace(v.Get("organization")))
		if err != nil || orgID <= 0 {
			errs.Add("organization", "Choose an organization.")
		}
		ct.OrganizationID = orgID
	}
	return ct, errs
}

// formValues prefills one type's edit form. The organization is the id the
// select posts, not the owner name the list column shows, which is why the
// row and this map disagree on that one field.
func formValues(ct credstore.CredentialType) map[string]string {
	return map[string]string{
		"name":         ct.Name,
		"description":  ct.Description,
		"kind":         string(ct.Kind),
		"namespace":    ct.Namespace,
		"organization": strconv.Itoa(ct.OrganizationID),
	}
}

// writer is the credential type write port.
type writer struct{ store credstore.Store }

// Create stores a new custom type against the chosen tenant.
func (w writer) Create(ctx context.Context, ct credstore.CredentialType) (string, error) {
	created, err := w.store.CreateType(ctx, ct.OrganizationID, ct.CredentialType)
	if err != nil {
		return "", writeFault(err)
	}
	return strconv.Itoa(created.ID), nil
}

// Update rewrites a type's metadata, carrying its inputs and injectors
// forward unchanged.
//
// UpdateType writes both wholesale, so passing the empty schema this
// metadata form has no controls for would blank a type's inputs and
// injectors on a rename, turning an edit into data loss. The section
// editors are the only writers that change those.
func (w writer) Update(ctx context.Context, id string, ct credstore.CredentialType) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("credentialtypes: unreadable credential type id %q: %w", id, err)
	}
	stored, err := w.store.GetType(ctx, numeric)
	if err != nil {
		return err
	}
	ct.Inputs = stored.Inputs
	ct.Injectors = stored.Injectors
	if _, err := w.store.UpdateType(ctx, numeric, ct.CredentialType); err != nil {
		return writeFault(err)
	}
	return nil
}

// Delete removes a custom type. The store refuses one still in use by a
// credential and one this platform ships, and those refusals reach the page
// as they are: a managed type offers no delete control, so only the in-use
// refusal is reachable here, and it names a count worth reading.
func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("credentialtypes: unreadable credential type id %q: %w", id, err)
	}
	return w.store.DeleteType(ctx, numeric)
}

// writeFault turns a store refusal into a message against the control that
// caused it, so a duplicate name or a malformed namespace is answered on
// the box that was wrong rather than with a 500.
func writeFault(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return view.FieldFault{Field: "name", Message: "A credential type with this name already exists in this organization."}
	case strings.Contains(msg, "namespace"):
		return view.FieldFault{Field: "namespace", Message: msg}
	case strings.Contains(msg, "kind"):
		return view.FieldFault{Field: "kind", Message: msg}
	default:
		return err
	}
}

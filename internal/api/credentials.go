package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The credential HTTP surface.
//
// # What this file cannot do
//
// It cannot return a secret, and not because the code below is careful.
// CredentialHandler holds a credstore.Store, whose read projection has no
// field a plaintext value could occupy: a secret input arrives here already
// replaced by a redaction marker. The interface that can decrypt one lives
// in internal/credstore/resolve, and internal/archtest's
// TestAPINeverImportsTheCredentialResolver fails the build if this package
// ever imports it.
//
// So a handler added later cannot leak a secret by forgetting to redact.
// It can only leak one by adding an import the build refuses. That is the
// whole reason the store was split into two packages, and this file is the
// side of the boundary that had to be kept honest.

// CredentialHandler serves credential types, credentials, and the bindings
// that decide what a template runs as.
type CredentialHandler struct {
	store credstore.Store

	// engine renders a type's injectors for the preview endpoint. It is
	// the same one the store validates writes with, so a preview cannot
	// accept a template the write would refuse.
	engine render.Engine

	// templates resolves the template a binding names, so a bind can be
	// refused when the template's execution path cannot honour the
	// credential's injectors. Optional: without it, the bind-time check is
	// skipped and the adapter's own run-time backstop is what refuses.
	templates TemplateReader
}

// CredentialHandlerOption configures optional collaborators.
type CredentialHandlerOption func(*CredentialHandler)

// WithBindingTemplates supplies the port the bind-time injector check reads.
//
// Optional so the several harnesses that build this handler keep compiling,
// and because its absence degrades to something safe rather than something
// wrong: internal/adapters/native refuses an unsupported injection at run
// time regardless. What this option buys is the refusal arriving when the
// operator is looking at the binding form, rather than on the first job
// they launch afterwards.
func WithBindingTemplates(templates TemplateReader) CredentialHandlerOption {
	return func(h *CredentialHandler) { h.templates = templates }
}

// NewCredentialHandler returns a handler over a store.
//
// It takes credstore.Store rather than a narrower reader because it both
// reads and writes. What it deliberately does NOT take is a resolver; see
// this file's own comment.
func NewCredentialHandler(store credstore.Store, engine render.Engine, opts ...CredentialHandlerOption) *CredentialHandler {
	if store == nil {
		panic("api: NewCredentialHandler requires a credential store")
	}
	if engine == nil {
		panic("api: NewCredentialHandler requires a render engine")
	}
	h := &CredentialHandler{store: store, engine: engine}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// credentialTypeDTO is one credential type on the wire.
type credentialTypeDTO struct {
	LinkSet

	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Kind         string `json:"kind"`
	Namespace    string `json:"namespace"`
	Managed      bool   `json:"managed"`
	Organization int    `json:"organization,omitempty"`

	Inputs    credtype.InputSchema `json:"inputs"`
	Injectors credtype.Injectors   `json:"injectors"`
}

// credentialTypeListDTO is a page of them.
type credentialTypeListDTO struct {
	LinkSet
	CredentialTypes []credentialTypeDTO `json:"credential_types"`
}

// credentialDTO is one credential on the wire.
//
// There is no field here for a secret input's VALUE, and there never will
// be one. Inputs carries whatever the store's projection produced, which
// replaces every secret with a marker before this type is ever constructed.
type credentialDTO struct {
	LinkSet

	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	CredentialType          int    `json:"credential_type"`
	CredentialTypeName      string `json:"credential_type_name"`
	CredentialTypeNamespace string `json:"credential_type_namespace"`
	Kind                    string `json:"kind"`
	Organization            int    `json:"organization"`

	Inputs   map[string]string `json:"inputs"`
	External map[string]string `json:"external,omitempty"`

	Templates []int `json:"templates"`
}

// credentialListDTO is a page of them.
type credentialListDTO struct {
	LinkSet
	Credentials []credentialDTO `json:"credentials"`
}

// inputSourceDTO is one binding on the wire.
//
// There is no field here for a secret VALUE, and there never will be one,
// for the same reason credentialDTO has none: this describes WHERE a value
// lives, and the value itself is never stored by this platform at all.
type inputSourceDTO struct {
	ID                        int               `json:"id"`
	InputID                   string            `json:"input_id"`
	SourceCredential          int               `json:"source_credential"`
	SourceCredentialName      string            `json:"source_credential_name"`
	SourceCredentialNamespace string            `json:"source_credential_namespace,omitempty"`
	Metadata                  map[string]string `json:"metadata,omitempty"`
}

// inputSourceListDTO is a credential's whole set of bindings.
type inputSourceListDTO struct {
	LinkSet
	InputSources []inputSourceDTO `json:"input_sources"`
}

// inputSourceWriteDTO is one binding as a caller sends it.
type inputSourceWriteDTO struct {
	InputID          string            `json:"input_id"`
	SourceCredential int               `json:"source_credential"`
	Metadata         map[string]string `json:"metadata"`
}

// inputSourceSetDTO is the body the replace accepts.
type inputSourceSetDTO struct {
	InputSources []inputSourceWriteDTO `json:"input_sources"`
}

// credentialTypeWriteDTO is the body create and update accept.
type credentialTypeWriteDTO struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Kind         string               `json:"kind"`
	Namespace    string               `json:"namespace"`
	Organization int                  `json:"organization"`
	Inputs       credtype.InputSchema `json:"inputs"`
	Injectors    credtype.Injectors   `json:"injectors"`
}

// credentialWriteDTO is the body create and update accept.
type credentialWriteDTO struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	CredentialType int               `json:"credential_type"`
	Organization   int               `json:"organization"`
	Inputs         map[string]string `json:"inputs"`
	External       map[string]string `json:"external"`
}

// credentialBindingWriteDTO is the body the binding replacement accepts.
//
// Named for its own resource rather than "binding", which access_bindings.go
// already uses for RBAC role bindings. Two unrelated things called a
// binding in one package is how the wrong one gets decoded into.
type credentialBindingWriteDTO struct {
	Credentials []int `json:"credentials"`
}

// ListCredentialTypes serves GET /credential-types.
func (h *CredentialHandler) ListCredentialTypes(w http.ResponseWriter, r *http.Request) {
	orgID, ok := optionalIntQuery(w, r, "organization")
	if !ok {
		return
	}

	types, err := h.store.ListTypes(r.Context(), orgID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	out := credentialTypeListDTO{CredentialTypes: make([]credentialTypeDTO, 0, len(types))}
	for _, ct := range types {
		out.CredentialTypes = append(out.CredentialTypes, credentialTypeToDTO(ct))
	}
	Respond(w, r, http.StatusOK, &out)
}

// GetCredentialType serves GET /credential-types/{id}.
func (h *CredentialHandler) GetCredentialType(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential type")
	if !ok {
		return
	}

	ct, err := h.store.GetType(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialTypeToDTO(ct)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateCredentialType serves POST /credential-types.
func (h *CredentialHandler) CreateCredentialType(w http.ResponseWriter, r *http.Request) {
	var body credentialTypeWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Organization <= 0 {
		RespondError(w, r, http.StatusBadRequest, "organization is required: a custom credential type belongs to exactly one tenant")
		return
	}

	ct, err := h.store.CreateType(r.Context(), body.Organization, writeDTOToType(body))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialTypeToDTO(ct)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateCredentialType serves PATCH /credential-types/{id}.
func (h *CredentialHandler) UpdateCredentialType(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential type")
	if !ok {
		return
	}

	var body credentialTypeWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	ct, err := h.store.UpdateType(r.Context(), id, writeDTOToType(body))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialTypeToDTO(ct)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteCredentialType serves DELETE /credential-types/{id}.
func (h *CredentialHandler) DeleteCredentialType(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential type")
	if !ok {
		return
	}

	if err := h.store.DeleteType(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCredentials serves GET /credentials.
func (h *CredentialHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	orgID, ok := optionalIntQuery(w, r, "organization")
	if !ok {
		return
	}
	if orgID <= 0 {
		// Required rather than defaulted, because a default of "all" would
		// make one missing query parameter a cross-tenant listing of every
		// credential's name, type and external references.
		RespondError(w, r, http.StatusBadRequest, "organization is required")
		return
	}

	creds, err := h.store.ListCredentials(r.Context(), orgID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	out := credentialListDTO{Credentials: make([]credentialDTO, 0, len(creds))}
	for _, c := range creds {
		out.Credentials = append(out.Credentials, credentialToDTO(c))
	}
	Respond(w, r, http.StatusOK, &out)
}

// GetCredential serves GET /credentials/{id}.
func (h *CredentialHandler) GetCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential")
	if !ok {
		return
	}

	cred, err := h.store.GetCredential(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialToDTO(cred)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateCredential serves POST /credentials.
func (h *CredentialHandler) CreateCredential(w http.ResponseWriter, r *http.Request) {
	var body credentialWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Organization <= 0 || body.CredentialType <= 0 {
		RespondError(w, r, http.StatusBadRequest, "organization and credential_type are both required")
		return
	}

	cred, err := h.store.CreateCredential(r.Context(),
		body.Organization, body.CredentialType, body.Name, body.Description, body.Inputs, body.External)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialToDTO(cred)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateCredential serves PATCH /credentials/{id}.
func (h *CredentialHandler) UpdateCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential")
	if !ok {
		return
	}

	var body credentialWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	cred, err := h.store.UpdateCredential(r.Context(), id, body.Name, body.Description, body.Inputs, body.External)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialToDTO(cred)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteCredential serves DELETE /credentials/{id}.
func (h *CredentialHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential")
	if !ok {
		return
	}

	if err := h.store.DeleteCredential(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTemplateCredentials serves GET /templates/{id}/credentials.
func (h *CredentialHandler) ListTemplateCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "template")
	if !ok {
		return
	}

	creds, err := h.store.TemplateCredentials(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	out := credentialListDTO{Credentials: make([]credentialDTO, 0, len(creds))}
	for _, c := range creds {
		out.Credentials = append(out.Credentials, credentialToDTO(c))
	}
	Respond(w, r, http.StatusOK, &out)
}

// SetTemplateCredentials serves PUT /templates/{id}/credentials.
//
// The whole set is replaced, so an empty list unbinds everything. A
// conflicting set is refused with 409 and both credentials named, because
// the caller has to choose which one to drop and "conflict" alone does not
// help them choose.
func (h *CredentialHandler) SetTemplateCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "template")
	if !ok {
		return
	}

	var body credentialBindingWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	if err := h.checkInjectable(r.Context(), id, body.Credentials); err != nil {
		h.fail(w, r, err)
		return
	}

	if err := h.store.SetTemplateCredentials(r.Context(), id, body.Credentials); err != nil {
		h.fail(w, r, err)
		return
	}

	creds, err := h.store.TemplateCredentials(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	out := credentialListDTO{Credentials: make([]credentialDTO, 0, len(creds))}
	for _, c := range creds {
		out.Credentials = append(out.Credentials, credentialToDTO(c))
	}
	Respond(w, r, http.StatusOK, &out)
}

// ListCredentialInputSources serves GET /credentials/{id}/input-sources.
func (h *CredentialHandler) ListCredentialInputSources(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential")
	if !ok {
		return
	}

	sources, err := h.store.ListCredentialInputSources(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	Respond(w, r, http.StatusOK, inputSourcesToDTO(sources))
}

// SetCredentialInputSources serves PUT /credentials/{id}/input-sources.
//
// The whole set is replaced, so an empty list makes every input read from
// stored values again. Every refusal happens in the store rather than here,
// deliberately: a second writer reaching the store directly must meet the
// same rules, and a check in a handler is a check one caller can miss.
func (h *CredentialHandler) SetCredentialInputSources(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential")
	if !ok {
		return
	}

	var body inputSourceSetDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	bindings := make([]credstore.InputSourceBinding, 0, len(body.InputSources))
	for _, in := range body.InputSources {
		bindings = append(bindings, credstore.InputSourceBinding{
			InputID:            in.InputID,
			SourceCredentialID: in.SourceCredential,
			Metadata:           in.Metadata,
		})
	}

	sources, err := h.store.SetCredentialInputSources(r.Context(), id, bindings)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	Respond(w, r, http.StatusOK, inputSourcesToDTO(sources))
}

// inputSourcesToDTO projects a credential's bindings onto the wire.
func inputSourcesToDTO(sources []credstore.InputSource) *inputSourceListDTO {
	out := inputSourceListDTO{InputSources: make([]inputSourceDTO, 0, len(sources))}
	for _, s := range sources {
		out.InputSources = append(out.InputSources, inputSourceDTO{
			ID:                        s.ID,
			InputID:                   s.InputID,
			SourceCredential:          s.SourceCredentialID,
			SourceCredentialName:      s.SourceCredentialName,
			SourceCredentialNamespace: s.SourceCredentialNamespace,
			Metadata:                  s.Metadata,
		})
	}
	return &out
}

// checkInjectable refuses a binding whose credential types the template's
// execution path cannot honour.
//
// It is the bind-time half of a two-part refusal. The run-time half lives in
// internal/adapters/native and fires on any payload that reaches it,
// whatever route it took. Two checks rather than one because they answer
// different needs: this one tells the operator while they are still looking
// at the form and can fix it in a second, and that one guarantees the rule
// holds for a binding made before this check existed, or by a Controller
// running an older build.
//
// A handler with no template reader wired skips this entirely, and the
// run-time backstop is what refuses. That degradation is safe, which is
// what makes the option optional.
func (h *CredentialHandler) checkInjectable(ctx context.Context, templateID int, credentialIDs []int) error {
	if h.templates == nil || len(credentialIDs) == 0 {
		return nil
	}

	tmpl, err := h.templates.Get(ctx, templateID)
	if err != nil {
		// Not this check's job to report a missing template. The store's
		// own write below produces the right error for that, and reporting
		// it here too would give one condition two different responses
		// depending on whether this option happened to be wired.
		return nil //nolint:nilerr // deliberate: see comment
	}
	descriptor, err := tmpl.Descriptor()
	if err != nil {
		// A kind this build no longer registers cannot be checked against.
		// The launch path already refuses such a template with a message
		// about the kind, which is the useful error.
		return nil //nolint:nilerr // deliberate: see comment
	}

	for _, id := range credentialIDs {
		cred, err := h.store.GetCredential(ctx, id)
		if err != nil {
			return err
		}
		ct, err := h.store.GetType(ctx, cred.TypeID)
		if err != nil {
			return err
		}
		// The rule lives in internal/adapters/routing, with one
		// implementation and two callers: this one, and the run-time
		// backstop in internal/adapters/native. See that file for why.
		if err := routing.CheckInjectable(
			descriptor.Adapter, ct.Name, sortedNames(ct.Injectors.Env), ct.Injectors.FileLabels()); err != nil {
			return err
		}
	}
	return nil
}

// sortedNames returns a map's keys, sorted, so a refusal names the same
// variable on every call.
func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestCredentialType serves POST /credential-types/{id}/test.
//
// It renders the type's injectors against values the caller supplies and
// reports the SHAPE of what would be produced: which environment
// variables, which extra-variable keys, which file labels. No value is
// returned and no stored credential is read, so this discloses nothing
// about any credential.
//
// It earns its place because the alternative is that an author discovers a
// miswritten injector when an operator launches a job, which is both the
// worst moment and the hardest one to attribute correctly.
func (h *CredentialHandler) TestCredentialType(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential type")
	if !ok {
		return
	}

	var body credentialTestDTO
	// A body is optional: previewing with no values at all is meaningful
	// for a type whose inputs all declare defaults, and refusing an empty
	// request would make the simplest case the awkward one.
	if r.ContentLength > 0 && !decodeJSON(w, r, &body) {
		return
	}

	ct, err := h.store.GetType(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	preview, err := ct.Injectors.Preview(ct.Inputs, h.engine, body.Inputs)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	out := credentialPreviewDTO{
		Env:        nonNilStrings(preview.Env),
		ExtraVars:  nonNilStrings(preview.ExtraVars),
		FileLabels: nonNilStrings(preview.FileLabels),
	}
	Respond(w, r, http.StatusOK, &out)
}

// SetCredentialTypeInputs serves PUT /credential-types/{id}/inputs.
//
// It replaces the type's input schema and nothing else. The store's own
// UpdateType writes the whole type, so this reads the stored one first and
// carries its metadata and injectors forward, which is what keeps a schema
// edit from blanking the injector document beside it. Every rule the store
// enforces on a full update -- a valid identifier, no duplicate, no secret
// carrying a default, no injector left referencing an input this removes,
// no managed type -- still applies, because it is the same update.
func (h *CredentialHandler) SetCredentialTypeInputs(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r, "credential type")
	if !ok {
		return
	}

	var body credentialTypeInputsDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	stored, err := h.store.GetType(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	updated := stored.CredentialType
	updated.Inputs = body.Inputs

	ct, err := h.store.UpdateType(r.Context(), id, updated)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	dto := credentialTypeToDTO(ct)
	Respond(w, r, http.StatusOK, &dto)
}

// credentialTypeInputsDTO is the body PUT /credential-types/{id}/inputs
// accepts: the new input schema, on its own.
type credentialTypeInputsDTO struct {
	Inputs credtype.InputSchema `json:"inputs"`
}

// credentialTestDTO is the preview request body.
type credentialTestDTO struct {
	// Inputs are dummy values, keyed by input id. They are never stored
	// and never returned.
	Inputs map[string]string `json:"inputs"`
}

// credentialPreviewDTO is what the preview answers with: keys only.
type credentialPreviewDTO struct {
	LinkSet

	Env        []string `json:"env"`
	ExtraVars  []string `json:"extra_vars"`
	FileLabels []string `json:"file_labels"`
}

// nonNilStrings returns a slice that marshals as [] rather than null.
//
// The same contract launch's ignored_fields already draws: a key that
// vanished when the list was empty would make "this type injects no
// environment variables" indistinguishable from "this server does not
// report them".
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// fail maps a store error onto a status code.
//
// Every branch is a sentinel this package's own dependency defines rather
// than a string match, so a reworded error message cannot silently turn a
// 404 into a 500. The default is 500 on purpose: an unrecognized error is
// a server fault until somebody classifies it, and guessing 400 would tell
// a caller their request was wrong when it was not.
func (h *CredentialHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, credstore.ErrNotFound):
		RespondError(w, r, http.StatusNotFound, err.Error())
	case errors.Is(err, credstore.ErrExists):
		RespondError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, credstore.ErrManaged):
		RespondError(w, r, http.StatusForbidden, err.Error())
	case errors.Is(err, credstore.ErrInUse):
		RespondError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, credstore.ErrCrossOrganization):
		RespondError(w, r, http.StatusForbidden, err.Error())
	case errors.Is(err, credtype.ErrBindingConflict):
		RespondError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, credtype.ErrLookupCycle):
		// 409 for the same reason ErrUnsupportedInjection is one below:
		// the request is well formed and every record it names exists.
		// What conflicts is the resulting GRAPH. The message names the
		// credentials on the loop, because "conflict" alone does not tell
		// the caller which binding to drop.
		RespondError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, credtype.ErrLookupDepth):
		// 409 as well, and deliberately not 400. The binding being added
		// is legal on its own; it is legal only because of how deep the
		// chain it joins already is, which is state rather than syntax.
		RespondError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, credtype.ErrInvalidType), errors.Is(err, credtype.ErrInvalidCredential):
		RespondError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, routing.ErrUnsupportedInjection):
		// 409 rather than 400: the request is well formed and both records
		// exist. What conflicts is the pair, exactly like a binding
		// conflict two branches up, and the message names which injector
		// targets are the difficulty so the operator can act on it.
		RespondError(w, r, http.StatusConflict, err.Error())
	default:
		RespondError(w, r, http.StatusInternalServerError, "the request could not be completed")
	}
}

// credentialTypeToDTO converts a stored type for the wire.
func credentialTypeToDTO(ct credstore.CredentialType) credentialTypeDTO {
	return credentialTypeDTO{
		ID:           ct.ID,
		Name:         ct.Name,
		Description:  ct.Description,
		Kind:         string(ct.Kind),
		Namespace:    ct.Namespace,
		Managed:      ct.Managed,
		Organization: ct.OrganizationID,
		Inputs:       ct.Inputs,
		Injectors:    ct.Injectors,
	}
}

// credentialToDTO converts a stored credential for the wire.
//
// Inputs is copied through exactly as the store produced it, which is
// already redacted. There is deliberately no redaction step here: doing it
// twice would suggest the store's own projection was optional, and the
// place a reader looks for the guarantee should be the one place that
// makes it.
func credentialToDTO(c credstore.Credential) credentialDTO {
	return credentialDTO{
		ID:                      c.ID,
		Name:                    c.Name,
		Description:             c.Description,
		CredentialType:          c.TypeID,
		CredentialTypeName:      c.TypeName,
		CredentialTypeNamespace: c.TypeNamespace,
		Kind:                    string(c.Kind),
		Organization:            c.OrganizationID,
		Inputs:                  c.Inputs,
		External:                c.External,
		Templates:               c.TemplateIDs,
	}
}

// writeDTOToType converts a request body into the domain type.
func writeDTOToType(body credentialTypeWriteDTO) credtype.CredentialType {
	return credtype.CredentialType{
		Name:        body.Name,
		Description: body.Description,
		Kind:        credtype.Kind(body.Kind),
		Namespace:   body.Namespace,
		Inputs:      body.Inputs,
		Injectors:   body.Injectors,
	}
}

// parsePathID reads the {id} path parameter, naming what it identifies so
// the error tells a caller which id was wrong on a route that carries more
// than one kind.
func parsePathID(w http.ResponseWriter, r *http.Request, what string) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, what+" id must be a positive integer")
		return 0, false
	}
	return id, true
}

// optionalIntQuery reads a query parameter that may be absent.
func optionalIntQuery(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		RespondError(w, r, http.StatusBadRequest, name+" must be a positive integer")
		return 0, false
	}
	return n, true
}

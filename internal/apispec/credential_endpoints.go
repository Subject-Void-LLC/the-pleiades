package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// The credential surface: credential types, credentials, and the bindings
// that decide what a template runs as.
//
// Nothing in this file returns a secret value, and that is not a promise
// this document makes on behalf of the handlers. The handlers hold a store
// whose read projection has no field a plaintext secret could occupy, and
// the interface that can decrypt one lives in a package the API layer is
// forbidden from importing. The schemas below describe what a caller
// actually gets, which is a redaction marker.

// inputFieldSchema is one field a credential of a given type holds.
var inputFieldSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":    stringSchema("The identifier an injector template references. Lowercase letters, digits and underscores, starting with a letter."),
		"label": stringSchema("What a form shows above the control."),
		"type":  stringSchema("\"string\" or \"boolean\". Absent means string."),
		"secret": map[string]any{
			"type": "boolean",
			"description": "Whether this field's value is a secret. It is the single most consequential flag here: " +
				"it decides encryption at rest and whether the value reads back as a marker or as itself.",
		},
		"multiline": map[string]any{"type": "boolean", "description": "Whether a form should render a text area."},
		"format":    stringSchema("An optional content hint: ssh_private_key, url, or vault_id."),
		"choices": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "An optional bounded value set.",
		},
		"default":   stringSchema("The value used when a credential omits this field. Refused on a secret field: a default secret is a credential stored in the type itself, readable by anybody who may edit the type."),
		"help_text": stringSchema("Explanatory text a form shows beneath the control."),
		"ask_at_runtime": map[string]any{
			"type": "boolean",
			"description": "Whether this input is prompted at launch rather than stored. An answer to one is used for " +
				"a single run and never persisted, which is also why a template binding such a credential cannot be relaunched.",
		},
	},
}

// inputsSchema is a credential type's whole input schema.
var inputsSchema = map[string]any{
	"type":        "object",
	"description": "What a credential of this type holds.",
	"properties": map[string]any{
		"fields": map[string]any{
			"type":        "array",
			"items":       inputFieldSchema,
			"description": "The inputs, in the order a form should show them.",
		},
		"required": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "The input ids a credential must supply. An input with a default, or one prompted at launch, satisfies this without being stored.",
		},
	},
}

// injectorsSchema is how a credential's inputs reach a running job.
var injectorsSchema = map[string]any{
	"type": "object",
	"description": "Where this type's values go at run time. Every VALUE is a template over the type's own input ids, " +
		"for example \"{{ api_token }}\". No KEY is ever templated. Every template is compiled when the type is " +
		"saved, and one naming an input the type does not declare is refused here rather than at somebody's launch.",
	"properties": map[string]any{
		"env": map[string]any{
			"type": "object",
			"description": "Environment variables. A name that would let an injector change how the run executes " +
				"(PATH, LD_PRELOAD, PYTHONPATH, ANSIBLE_CONFIG and others) is refused: the customer's playbook runs " +
				"inside the same container, so setting one of those is code execution inside the run the credential was meant to authenticate.",
		},
		"extra_vars": map[string]any{
			"type":        "object",
			"description": "Extra variables, which may nest. A leaf is a template; a literal number or boolean is passed through unrendered.",
		},
		"file": map[string]any{
			"type": "object",
			"description": "Generated files. Either the single-file spelling, one entry keyed \"template\", addressed " +
				"back as {{ tower.filename }}; or the multi-file spelling, entries keyed \"template.<label>\", " +
				"addressed as {{ tower.filename.<label> }}. The two are mutually exclusive within one type.",
		},
	},
}

// credentialTypeSchema is one credential type as this API returns it.
var credentialTypeSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":          map[string]any{"type": "integer"},
		"name":        stringSchema("What a reader picks it by, unique within an organization."),
		"description": stringSchema(""),
		"kind": stringSchema("The coarse grouping: ssh, vault, net, scm, cloud, token, insights, external, " +
			"kubernetes, galaxy, cryptography or registry. It is what the binding rule keys on."),
		"namespace": stringSchema("The stable identifier an import keys on to decide whether this type already exists. Immutable."),
		"managed": map[string]any{
			"type": "boolean",
			"description": "Whether this platform ships the type. A managed type cannot be edited or deleted, which an " +
				"import must respect rather than recreating the built-ins as custom types.",
		},
		"organization": map[string]any{"type": "integer", "description": "The owning tenant. Absent for a managed type, which belongs to nobody and is usable by everybody."},
		"inputs":       inputsSchema,
		"injectors":    injectorsSchema,
		"_links":       linksSchema(),
	},
}

// credentialSchema is one credential as this API returns it.
var credentialSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":                        map[string]any{"type": "integer"},
		"name":                      stringSchema("What a reader picks it by, unique within an organization."),
		"description":               stringSchema(""),
		"credential_type":           map[string]any{"type": "integer"},
		"credential_type_name":      stringSchema("The type's name, carried so a reader does not need a second request."),
		"credential_type_namespace": stringSchema("The type's stable identifier."),
		"kind":                      stringSchema("The type's kind, carried for the same reason."),
		"organization":              map[string]any{"type": "integer"},
		"inputs": map[string]any{
			"type": "object",
			"description": "The credential's values, keyed by input id. A value for an input the type marks secret " +
				"reads back as \"$encrypted$\" and never as itself. There is no endpoint, parameter or header that " +
				"returns the real value: it is decrypted only at dispatch, inside the process that injects it. " +
				"Submitting the marker back on an update leaves the stored value alone, so editing an unrelated " +
				"field does not destroy a secret.",
		},
		"external": map[string]any{
			"type": "object",
			"description": "External secret-manager references, keyed by input id, resolved just in time before a run. " +
				"NOT redacted: a reference is a pointer to a secret rather than a secret, and hiding it would make " +
				"\"which credentials point at this mount\" unanswerable during a migration.",
		},
		"templates": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "integer"},
			"description": "The templates this credential is bound to.",
		},
		"_links": linksSchema(),
	},
}

// ListCredentialTypes is GET /credential-types.
var ListCredentialTypes = Endpoint{
	Name:    "list_credential_types",
	Method:  http.MethodGet,
	Pattern: "/credential-types",
	Scope:   auth.ScopeCredentialRead,
	Rel:     auth.RelCollection,
	Summary: "List credential types",
	Description: "Returns the types an organization can use: its own custom ones plus every managed type. A query " +
		"filtered to one tenant would hide the entire built-in catalog, since a managed type belongs to nobody.",
	Params: []Param{
		{Name: "organization", In: "query", Required: false, Type: "integer", Description: "Whose custom types to include alongside the managed ones."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The visible credential types.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credential_types": map[string]any{"type": "array", "items": credentialTypeSchema},
				"_links":           linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "organization is not a valid integer.", Schema: errorSchema("")},
	},
}

// GetCredentialType is GET /credential-types/{id}.
var GetCredentialType = Endpoint{
	Name:        "get_credential_type",
	Method:      http.MethodGet,
	Pattern:     "/credential-types/{id}",
	Scope:       auth.ScopeCredentialRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one credential type",
	Description: "Returns one type with its full input schema and injector document.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The type's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The credential type.", Schema: credentialTypeSchema},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential type with that id.", Schema: errorSchema("")},
	},
}

// CreateCredentialType is POST /credential-types.
var CreateCredentialType = Endpoint{
	Name:    "create_credential_type",
	Method:  http.MethodPost,
	Pattern: "/credential-types",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelCreate,
	Summary: "Create a credential type",
	Description: "Defines a new custom credential type. Every injector template is compiled here, and one referencing " +
		"an input the type does not declare is refused, so a template that cannot render is never stored.",
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":     "object",
		"required": []any{"name", "kind", "namespace", "organization"},
		"properties": map[string]any{
			"name":         stringSchema("Unique within the organization."),
			"description":  stringSchema(""),
			"kind":         stringSchema("One of the twelve kinds."),
			"namespace":    stringSchema("The stable identifier. Immutable once set."),
			"organization": map[string]any{"type": "integer", "description": "The owning tenant."},
			"inputs":       inputsSchema,
			"injectors":    injectorsSchema,
		},
	},
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created type.", Schema: credentialTypeSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or the type does not validate.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name or namespace is already taken.", Schema: errorSchema("")},
	},
}

// UpdateCredentialType is PATCH /credential-types/{id}.
var UpdateCredentialType = Endpoint{
	Name:    "update_credential_type",
	Method:  http.MethodPatch,
	Pattern: "/credential-types/{id}",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a credential type",
	Description: "Replaces a custom type's editable fields. The namespace is immutable and a supplied one is ignored " +
		"rather than refused, so a form that round-trips every field it rendered does not fail on a field it was " +
		"never allowed to change. A managed type is refused outright.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The type's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        stringSchema(""),
			"description": stringSchema(""),
			"kind":        stringSchema(""),
			"inputs":      inputsSchema,
			"injectors":   injectorsSchema,
		},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated type.", Schema: credentialTypeSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or the type does not validate.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The type is managed and cannot be modified.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential type with that id.", Schema: errorSchema("")},
	},
}

// DeleteCredentialType is DELETE /credential-types/{id}.
var DeleteCredentialType = Endpoint{
	Name:    "delete_credential_type",
	Method:  http.MethodDelete,
	Pattern: "/credential-types/{id}",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelDelete,
	Summary: "Delete a credential type",
	Description: "Removes a custom type. Refused while credentials still use it, with a count: a credential whose " +
		"type vanished cannot be injected, and discovering that at somebody's launch is worse than being refused here.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The type's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusForbidden, Description: "The type is managed and cannot be deleted.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "Credentials still use this type.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential type with that id.", Schema: errorSchema("")},
	},
}

// ListCredentials is GET /credentials.
var ListCredentials = Endpoint{
	Name:        "list_credentials",
	Method:      http.MethodGet,
	Pattern:     "/credentials",
	Scope:       auth.ScopeCredentialRead,
	Rel:         auth.RelCollection,
	Summary:     "List credentials",
	Description: "Returns one organization's credentials, with every secret input redacted.",
	Params: []Param{
		{Name: "organization", In: "query", Required: true, Type: "integer", Description: "Whose credentials to list."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The organization's credentials.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credentials": map[string]any{"type": "array", "items": credentialSchema},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "organization is missing or not a valid integer.", Schema: errorSchema("")},
	},
}

// GetCredential is GET /credentials/{id}.
var GetCredential = Endpoint{
	Name:        "get_credential",
	Method:      http.MethodGet,
	Pattern:     "/credentials/{id}",
	Scope:       auth.ScopeCredentialRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one credential",
	Description: "Returns one credential with every secret input redacted.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The credential's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The credential.", Schema: credentialSchema},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential with that id.", Schema: errorSchema("")},
	},
}

// CreateCredential is POST /credentials.
var CreateCredential = Endpoint{
	Name:    "create_credential",
	Method:  http.MethodPost,
	Pattern: "/credentials",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelCreate,
	Summary: "Create a credential",
	Description: "Stores a new credential. Values are supplied in full here and are encrypted before they reach the " +
		"database; the response returns them redacted, which is the only form any read ever produces.",
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":     "object",
		"required": []any{"name", "credential_type", "organization"},
		"properties": map[string]any{
			"name":            stringSchema("Unique within the organization."),
			"description":     stringSchema(""),
			"credential_type": map[string]any{"type": "integer", "description": "The type this credential is of. Immutable once set."},
			"organization":    map[string]any{"type": "integer", "description": "The owning tenant."},
			"inputs":          map[string]any{"type": "object", "description": "The values, keyed by input id."},
			"external":        map[string]any{"type": "object", "description": "External secret-manager references, keyed by input id."},
		},
	},
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created credential, redacted.", Schema: credentialSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or the values do not satisfy the type.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in the organization.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential type with that id.", Schema: errorSchema("")},
	},
}

// UpdateCredential is PATCH /credentials/{id}.
var UpdateCredential = Endpoint{
	Name:    "update_credential",
	Method:  http.MethodPatch,
	Pattern: "/credentials/{id}",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a credential",
	Description: "Replaces a credential's values. An input submitted as \"$encrypted$\" is left at whatever is stored, " +
		"which is what lets a form round-trip: it renders the marker for a secret, an operator edits an unrelated " +
		"field, and submitting does not overwrite the secret with the marker text. Submit a real value to rotate one.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The credential's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        stringSchema(""),
			"description": stringSchema(""),
			"inputs":      map[string]any{"type": "object"},
			"external":    map[string]any{"type": "object"},
		},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated credential, redacted.", Schema: credentialSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or the values do not satisfy the type.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential with that id.", Schema: errorSchema("")},
	},
}

// DeleteCredentialEndpoint is DELETE /credentials/{id}.
//
// Named with the suffix because DeleteCredential would collide with the
// store method every reader of this package also has in scope.
var DeleteCredentialEndpoint = Endpoint{
	Name:    "delete_credential",
	Method:  http.MethodDelete,
	Pattern: "/credentials/{id}",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelDelete,
	Summary: "Delete a credential",
	Description: "Removes a credential and every binding to it. Templates that bound it survive, holding nothing: " +
		"the alternative, refusing while it is bound, would mean a compromised credential could not be removed " +
		"until every template using it had been edited first.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The credential's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusNotFound, Description: "No credential with that id.", Schema: errorSchema("")},
	},
}

// ListTemplateCredentials is GET /templates/{id}/credentials.
var ListTemplateCredentials = Endpoint{
	Name:        "list_template_credentials",
	Method:      http.MethodGet,
	Pattern:     "/templates/{id}/credentials",
	Scope:       auth.ScopeTemplateRead,
	Rel:         auth.RelCollection,
	Summary:     "List a template's credentials",
	Description: "Returns what this template runs as, with every secret input redacted.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The bound credentials.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credentials": map[string]any{"type": "array", "items": credentialSchema},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
	},
}

// SetTemplateCredentials is PUT /templates/{id}/credentials.
//
// The scope is credential:write rather than template:write on purpose. A
// template author decides what runs; whoever binds a credential decides
// what it runs AS, which is the higher privilege of the two.
var SetTemplateCredentials = Endpoint{
	Name:    "set_template_credentials",
	Method:  http.MethodPut,
	Pattern: "/templates/{id}/credentials",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelUpdate,
	Summary: "Replace a template's credentials",
	Description: "Replaces the whole set, so an empty list unbinds everything. At most one credential of each kind " +
		"may be bound, except vault credentials, which may repeat while each carries a distinct vault identifier. " +
		"A conflicting set is refused with both credentials named, since the caller has to choose which to drop.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":     "object",
		"required": []any{"credentials"},
		"properties": map[string]any{
			"credentials": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "The credential ids to bind. Order is preserved, which matters for vault credentials.",
			},
		},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The bound credentials, redacted.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credentials": map[string]any{"type": "array", "items": credentialSchema},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "The body is malformed.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "Two of the supplied credentials cannot be bound together.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id, or one credential does not exist.", Schema: errorSchema("")},
	},
}

// TestCredentialType is POST /credential-types/{id}/test.
//
// It renders a type's injectors against caller-supplied dummy values and
// reports the SHAPE of what it would produce: which environment variables,
// which extra-variable keys, which file labels. Every value is masked.
//
// It earns its place because an author otherwise finds out their injector
// document is wrong when an operator launches a job. It touches no stored
// credential, so it discloses nothing about one.
var TestCredentialType = Endpoint{
	Name:    "test_credential_type",
	Method:  http.MethodPost,
	Pattern: "/credential-types/{id}/test",
	Scope:   auth.ScopeCredentialWrite,
	Rel:     auth.RelExecute,
	Summary: "Test a credential type's injectors",
	Description: "Renders this type's injectors against values the caller supplies, and reports what it would " +
		"produce with every value masked. No stored credential is read and nothing is written. It is how an author " +
		"finds a miswritten injector before a run does.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The type's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{"type": "object", "description": "Dummy values keyed by input id. These are not stored."},
		},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "What the injectors would produce.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"env":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The environment variable names."},
				"extra_vars":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The extra-variable keys, dotted for nested ones."},
				"file_labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The generated file labels. One empty entry means the single-file spelling."},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or a template could not render against the supplied values.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No credential type with that id.", Schema: errorSchema("")},
	},
}

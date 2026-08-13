// Package credtype models a credential type and how its inputs reach a
// running job.
//
// PLAN.md Section 29 states the shape in one line: credential types are
// data. A type declares an input schema (ordered fields with an identifier,
// a label, a type, a secret flag, a format, choices, a default, and a
// required set) and an injector document, both as data, so an administrator
// adds a credential type without a code change.
//
// # Why the JSON tags are AWX's own
//
// Every struct here decodes an AWX credential type directly, with no
// translation layer and no intermediate DTO. That is deliberate and it is
// the single most important property of this package.
//
// AWX_PARITY.md calls credential types the biggest gap in the migration
// story, and the reason is concrete: a customer's playbook reads the
// environment variables their credential type injects, so a type that does
// not import is a playbook that does not run. A translation layer between
// AWX's field names and ours would be a place for a mapping to be wrong,
// and a wrong mapping in a credential type is a silent authentication
// failure attributed to the wrong subsystem. corpus_test.go decodes a real
// production Ascender response straight into these types, which is the
// proof rather than the claim.
//
// # What is deliberately not here
//
// Nothing in this package touches storage, encryption, or the database.
// internal/ent imports these types for its own field.JSON schemas, so this
// package must never import internal/ent or internal/credstore, or the two
// form a cycle. Persistence lives in internal/credstore, and the one
// interface that can return a decrypted input value lives in a subpackage
// below it that internal/api is structurally forbidden from importing.
package credtype

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// Errors this package returns.
var (
	// ErrInvalidType reports a credential type that is not well formed:
	// a bad input schema, an injector document that names something the
	// schema does not declare, or a malformed identifier.
	ErrInvalidType = errors.New("credtype: credential type is not valid")

	// ErrInvalidCredential reports a credential whose values do not
	// satisfy its own type's schema.
	ErrInvalidCredential = errors.New("credtype: credential is not valid")
)

// Kind is AWX's coarse grouping of credential types.
//
// It is not a free-form label. The binding rule in PLAN.md Section 29.3
// keys on it: at most one credential per type on a definition, except
// vault credentials, which may repeat when each carries a distinct vault
// identifier. So the vocabulary being closed is what makes that rule
// expressible at all.
//
// The values are AWX's own spellings, for the same import-fidelity reason
// the JSON tags are.
type Kind string

// The kind vocabulary, matching AWX's own.
const (
	// KindSSH is AWX's machine credential: a username with a password or
	// an SSH key. It is the one kind that maps onto a Go object rather
	// than onto env/extra_vars/file, because the native transport consumes
	// it directly.
	KindSSH Kind = "ssh"

	// KindVault is an Ansible Vault password. It is the one kind exempt
	// from the one-per-type binding rule, because a playbook can
	// legitimately need several vault passwords, distinguished by id.
	KindVault Kind = "vault"

	// KindNet is a network device credential.
	KindNet Kind = "net"

	// KindSCM is a source control credential.
	KindSCM Kind = "scm"

	// KindCloud is the largest group and the one most custom types land
	// in: anything that authenticates to a remote API.
	KindCloud Kind = "cloud"

	// KindToken is an OAuth-style token credential.
	KindToken Kind = "token"

	// KindInsights is Red Hat Insights.
	KindInsights Kind = "insights"

	// KindExternal is a credential whose values come from an external
	// secret manager rather than from this platform's own storage.
	KindExternal Kind = "external"

	// KindKubernetes is a Kubernetes or OpenShift credential.
	KindKubernetes Kind = "kubernetes"

	// KindGalaxy is an Ansible Galaxy or Automation Hub credential.
	KindGalaxy Kind = "galaxy"

	// KindCryptography is a signing or verification key.
	KindCryptography Kind = "cryptography"

	// KindRegistry is a container registry credential.
	KindRegistry Kind = "registry"
)

// validKinds is the closed vocabulary, as a set.
var validKinds = map[Kind]bool{
	KindSSH: true, KindVault: true, KindNet: true, KindSCM: true,
	KindCloud: true, KindToken: true, KindInsights: true, KindExternal: true,
	KindKubernetes: true, KindGalaxy: true, KindCryptography: true, KindRegistry: true,
}

// Kinds returns the closed kind vocabulary, sorted. It exists so a form or
// an API schema can offer the real set rather than restating it.
func Kinds() []Kind {
	out := make([]Kind, 0, len(validKinds))
	for k := range validKinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports whether k is in the closed vocabulary.
func (k Kind) Valid() bool { return validKinds[k] }

// namespacePattern constrains a credential type's stable identifier.
//
// The namespace is what an AWX import keys on to decide whether a type
// already exists, so it has to be stable, unique, and safe to put in a URL
// and a database index.
var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// CredentialType is one credential type: an input schema plus an injector
// document, both data.
//
// The JSON tags are AWX's own field names. An AWX export decodes into this
// struct directly; see the package comment for why that matters more than
// it looks like it should.
type CredentialType struct {
	// Name is the human-readable name, unique within an organization.
	Name string `json:"name"`

	// Description is free text.
	Description string `json:"description,omitempty"`

	// Kind is the coarse grouping the binding rule keys on.
	Kind Kind `json:"kind"`

	// Namespace is the stable identifier. AWX gives every managed type
	// one, and the parity corpus shows a custom type carrying one too, so
	// this is required rather than managed-only.
	Namespace string `json:"namespace"`

	// Managed reports whether this platform ships the type.
	//
	// A managed type cannot be edited, which an import must respect rather
	// than recreating the built-ins as custom types. That is AWX's own
	// rule and tests/parity/fields_related.go records it as the reason
	// this field is not merely informational.
	Managed bool `json:"managed"`

	// Inputs is the schema: what a credential of this type holds.
	Inputs InputSchema `json:"inputs"`

	// Injectors is how those inputs reach a running job.
	Injectors Injectors `json:"injectors"`
}

// Validate reports whether the type is well formed, compiling every
// injector template through eng.
//
// Compiling at validation time rather than at launch time is the whole
// point of taking an engine here. Architecture Principle 5 says type safety
// moves left: an author who writes an injector referencing an input the
// type does not declare should learn about it when they save the type, not
// when an operator launches a job at three in the morning.
func (ct CredentialType) Validate(eng render.Engine) error {
	if eng == nil {
		return fmt.Errorf("%w: a render engine is required to validate injector templates", ErrInvalidType)
	}
	if ct.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidType)
	}
	if !ct.Kind.Valid() {
		return fmt.Errorf("%w: kind %q is not one of %v", ErrInvalidType, ct.Kind, Kinds())
	}
	if !namespacePattern.MatchString(ct.Namespace) {
		return fmt.Errorf(
			"%w: namespace %q must be lowercase letters, digits and underscores, starting with a letter",
			ErrInvalidType, ct.Namespace)
	}

	if err := ct.Inputs.Validate(); err != nil {
		return err
	}
	return ct.Injectors.Validate(ct.Inputs, eng)
}

// VaultIdentifierInput is the input id an Ansible Vault credential carries
// its vault label in.
//
// It is AWX's own name for the field, and the binding rule reads it: two
// vault credentials may be bound to one template only when each names a
// distinct identifier, because that is what lets ansible-playbook tell
// their --vault-id arguments apart.
const VaultIdentifierInput = "vault_id"

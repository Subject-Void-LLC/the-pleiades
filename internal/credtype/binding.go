package credtype

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The binding rule: which credentials may be attached to one definition at
// the same time.
//
// PLAN.md Section 29.3 states it in one sentence: at most one credential
// per type on a definition, except vault credentials, which may repeat when
// each carries a distinct vault identifier.
//
// It cannot be a database constraint, and that is worth being precise about
// rather than treating as an implementation detail. The rule is about the
// joined row's TYPE's KIND, plus, for vault, a value stored inside the
// joined row's own ENCRYPTED inputs. No dialect can express a constraint
// over a value it cannot read. So this function is the one implementation,
// with two callers: the store, before the write, and the API handler, so a
// caller gets a conflict naming both credentials rather than an opaque
// store error.
//
// The residual is that a direct SQL writer can still violate it. That is
// the same class as the cross-tenant note already on Template's inventory
// edge, and it is recorded in both places rather than left implicit.

// ErrBindingConflict reports a set of credentials that cannot all be bound
// to one definition at once.
var ErrBindingConflict = errors.New("credtype: these credentials cannot be bound to the same definition")

// Bound is one credential being considered for a binding.
//
// It carries only what the rule reads, rather than a whole credential,
// because the caller that has to run this check at the API layer should not
// need to hold decrypted inputs to do it. VaultIdentifier is the one value
// that comes from inside the inputs, and the store extracts it once.
type Bound struct {
	// CredentialID identifies the credential.
	CredentialID int

	// CredentialName is used in the conflict message. An operator reading
	// "these two conflict" needs the names, not the ids.
	CredentialName string

	// Kind is the credential's type's kind, which is what the rule groups
	// on.
	Kind Kind

	// VaultIdentifier is the value of the vault_id input, and is
	// meaningful only when Kind is KindVault. An empty string is a real
	// value there rather than an absent one: it is Ansible's default vault
	// identity, and a definition can have at most one of those, exactly as
	// it can have at most one of any other identity.
	VaultIdentifier string

	// PresentsCertificate says this credential supplies a client
	// certificate, and is meaningful only when Kind is KindCryptography.
	//
	// It is a fact about the credential's VALUES rather than its type,
	// which is why it is a field here rather than something this package
	// derives from Kind. KindCryptography covers signing keys as well as
	// client certificates, and only the latter competes for the single
	// machine identity a run authenticates as. PresentsCertificateFor
	// computes it from the one place that knows.
	PresentsCertificate bool
}

// PresentsCertificateFor reports whether a credential's values make it a
// client-certificate identity.
//
// It is exported so a caller assembling Bound values can fill
// PresentsCertificate without reimplementing the test, and it asks the same
// question the injector asks: evidence, not kind. A cryptography credential
// with neither a certificate nor a bundle is a signing key or something
// like it, and competes for nothing.
func PresentsCertificateFor(cred Credential) bool {
	if cred.Type.Kind != KindCryptography {
		return false
	}
	return cred.Inputs[CertificateInputCertificate] != "" || cred.Inputs[CertificateInputPFX] != ""
}

// CheckBinding reports whether every credential in bound can be attached to
// one definition at the same time.
//
// The error names both conflicting credentials and says which rule they
// violate, because the operator seeing it has to choose which one to
// remove, and "conflict" alone does not help them choose.
func CheckBinding(bound []Bound) error {
	// Vault is keyed by identifier; every other kind is keyed by the kind
	// alone, which is what makes vault the exception rather than a special
	// case scattered through the loop.
	seen := make(map[string]Bound, len(bound))

	for _, b := range bound {
		key := string(b.Kind)
		if b.Kind == KindVault {
			key = string(KindVault) + "\x00" + b.VaultIdentifier
		}

		previous, clash := seen[key]
		if !clash {
			seen[key] = b
			continue
		}

		if b.Kind == KindVault {
			return fmt.Errorf(
				"%w: %q and %q are both vault credentials with the identifier %s, and a definition can carry at most one vault credential per identifier",
				ErrBindingConflict, previous.CredentialName, b.CredentialName, describeVaultIdentifier(b.VaultIdentifier))
		}
		return fmt.Errorf(
			"%w: %q and %q are both %s credentials, and a definition can carry at most one credential of each kind",
			ErrBindingConflict, previous.CredentialName, b.CredentialName, b.Kind)
	}

	return checkOneMachineIdentity(seen)
}

// checkOneMachineIdentity refuses a definition that binds more than one
// thing capable of being the identity a run authenticates as.
//
// # Why this is not covered by the per-kind rule above
//
// That rule gives each KIND its own slot, and it is right to: a machine
// credential and a network credential are different kinds and coexist
// happily. A client certificate is a different kind again (cryptography),
// so the per-kind rule sees no conflict and accepts the binding, while
// Combine refuses the same pair at fan-out because an artifact may carry
// exactly one machine identity.
//
// The gap that leaves is the one worth closing. Without this, the write
// succeeds and the failure appears later, to whoever LAUNCHES the template,
// against a device, as an injection conflict naming two credentials they
// may not have bound. docs/10-running-in-production.md states the rule as
// "a template cannot bind both", and the same document makes the
// write-time/run-time distinction load bearing elsewhere, so a reader is
// entitled to read that as a refused write. This makes the sentence true.
//
// A cryptography credential is only a machine identity when it actually
// carries certificate material, which is the same evidence-based test the
// injector applies; a signing key bound beside a machine credential is
// ordinary and stays accepted.
func checkOneMachineIdentity(seen map[string]Bound) error {
	machine, hasMachine := seen[string(KindSSH)]
	if !hasMachine {
		machine, hasMachine = seen[string(KindNet)]
	}
	certificate, hasCertificate := seen[string(KindCryptography)]

	if !hasMachine || !hasCertificate || !certificate.PresentsCertificate {
		return nil
	}
	return fmt.Errorf(
		"%w: %q is a machine credential and %q presents a client certificate, and a run authenticates as exactly "+
			"one identity: bind one or the other",
		ErrBindingConflict, machine.CredentialName, certificate.CredentialName)
}

// describeVaultIdentifier renders an identifier for a message, naming the
// empty case rather than printing nothing.
//
// An operator who bound two vault credentials and left both identifiers
// blank would otherwise read "with the identifier " and have to guess.
func describeVaultIdentifier(id string) string {
	if id == "" {
		return "(the default, unnamed vault identity)"
	}
	return `"` + id + `"`
}

// VaultIdentifierOf extracts the vault identifier from a credential's input
// values.
//
// It returns the empty string for a non-vault credential and for a vault
// credential that names no identity, which is the same value Ansible's own
// default vault identity has. The caller distinguishes the two by the kind,
// not by this result.
func VaultIdentifierOf(kind Kind, inputs map[string]string) string {
	if kind != KindVault {
		return ""
	}
	return strings.TrimSpace(inputs[VaultIdentifierInput])
}

// BindingSummary describes a set of bindings for a message or a UI, sorted
// so the output is stable.
//
// It exists so the API and the UI describe a template's credentials the
// same way rather than each formatting the list themselves, which is how
// two descriptions of one thing drift apart.
func BindingSummary(bound []Bound) []string {
	out := make([]string, 0, len(bound))
	for _, b := range bound {
		if b.Kind == KindVault && b.VaultIdentifier != "" {
			out = append(out, fmt.Sprintf("%s (%s: %s)", b.CredentialName, b.Kind, b.VaultIdentifier))
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s)", b.CredentialName, b.Kind))
	}
	sort.Strings(out)
	return out
}

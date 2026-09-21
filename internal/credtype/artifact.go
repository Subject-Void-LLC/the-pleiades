package credtype

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The injection artifact: everything one launch's credentials produce,
// ready for an execution adapter to consume.
//
// Nothing here writes a file, opens a connection, or decides a path at run
// time. The injector computes a path with FilePath and hands the adapter a
// label, a path and content; the adapter decides how content crosses into
// wherever it runs. That split is what lets the reserved filename namespace
// and the writer agree by construction: both read the same FilePath result
// rather than each formatting a path of their own.

// Errors this file returns.
var (
	// ErrInjectorConflict reports two credentials injecting the same
	// environment variable, extra variable, file or identity.
	//
	// AWX resolves this collision silently, last writer winning by
	// dictionary order. This platform refuses it. Silently picking one is
	// the failure shape FAILURE_PATTERNS.md #116 already names elsewhere in
	// this codebase: the run authenticates as something the operator did
	// not choose, succeeds or fails for a reason unrelated to what they
	// changed, and nothing in the record says which credential won.
	ErrInjectorConflict = errors.New("credtype: two credentials inject the same value")

	// ErrInjection reports an injection that could not be completed: a
	// template that would not render against the credential's real values,
	// or a credential whose values do not satisfy its own type.
	ErrInjection = errors.New("credtype: injection failed")
)

// The keys a machine credential flattens into, aliased from pkg/wire
// rather than restated.
//
// These were literal strings here until Phase 78d, duplicating
// internal/credential's copy, and the doc comment explained that
// importing internal/credential would close a real cycle: internal/ent
// imports this package for its own field.JSON column types, and
// internal/credential's dependency closure reaches internal/ent through
// internal/crypto. That reasoning was correct and it is unchanged.
//
// It was also an answer to the wrong question. pkg/wire is not in that
// loop at all (it imports encoding/json and pkg/capability and nothing
// else), it is the package that declares the secrets map these keys index,
// and pkg/ may never import internal/ by construction. So both sides can
// alias one definition, which is what pkg/wire's own doc comment already
// claimed and what machine_test.go previously had to assert by comparison.
const (
	// MachineUsername is the account the transport authenticates as.
	MachineUsername = wire.SecretUsername

	// MachinePassword is a password for that account.
	MachinePassword = wire.SecretPassword

	// MachinePrivateKey is a PEM private key body. It is the key half of a
	// client certificate as well as an SSH key.
	MachinePrivateKey = wire.SecretPrivateKeyPEM

	// MachinePassphrase unlocks MachinePrivateKey on the SSH path, and
	// MachinePFX on the certificate path. It does NOT unlock a loose
	// MachinePrivateKey presented with MachineCertificate: nothing on that
	// path decrypts a key, and such a credential is refused rather than
	// silently ignoring the value.
	MachinePassphrase = wire.SecretPassphrase

	// MachineCertificate is a PEM X.509 client certificate to present.
	MachineCertificate = wire.SecretCertificatePEM

	// MachinePFX is a base64 PKCS#12 bundle holding both halves at once,
	// sealed until the point of use.
	MachinePFX = wire.SecretPFXBase64
)

// The input ids a machine credential type declares, which are AWX's own
// names for them.
//
// Only these four are special. Every other input a machine type declares,
// including AWX's become_method, become_username and become_password, is an
// ordinary input reachable from that type's own injector document, exactly
// like an input on any other type. That is deliberate and it is the whole
// reason injectors are data: a type wanting privilege escalation writes
// extra_vars: {ansible_become_password: "{{ become_password }}"} and needs
// no code here. Special-casing them would mean this file deciding what a
// credential type means, which is the thing Section 29 says it must not do.
const (
	// MachineInputUsername maps onto MachineUsername.
	MachineInputUsername = "username"

	// MachineInputPassword maps onto MachinePassword.
	MachineInputPassword = "password"

	// MachineInputKeyData maps onto MachinePrivateKey. AWX's own name for
	// the field is ssh_key_data.
	MachineInputKeyData = "ssh_key_data"

	// MachineInputKeyUnlock maps onto MachinePassphrase. AWX's own name for
	// the field is ssh_key_unlock.
	MachineInputKeyUnlock = "ssh_key_unlock"
)

// The input ids a KindCryptography credential type declares to present a
// client certificate.
//
// These are NOT AWX's names, because AWX has no credential type that
// presents a client certificate to a managed host. Where a name had to be
// invented, it describes the material rather than a protocol, so the same
// type serves WinRM over HTTPS and whatever presents a certificate next.
//
// A type declares EITHER the certificate and key pair OR the bundle,
// never both. The one-of rule is enforced where the values are read
// rather than here, because a constant cannot express it.
const (
	// CertificateInputCertificate maps onto MachineCertificate.
	CertificateInputCertificate = "certificate"

	// CertificateInputPrivateKey maps onto MachinePrivateKey. It is the
	// same flattened key an SSH credential's own private key uses, because
	// it is the same kind of material: a PEM private key body.
	CertificateInputPrivateKey = "private_key"

	// CertificateInputKeyUnlock maps onto MachinePassphrase, and unlocks the
	// BUNDLE only.
	//
	// It does not unlock a loose private key, and an earlier version of this
	// comment claimed it did. Nothing decrypts a private key on the
	// certificate path: crypto/tls cannot, so the value would have been
	// read, carried across the broker and then discarded. A passphrase
	// protected loose key is refused by name instead
	// (pkg/winrmexec.Auth.Validate), naming the bundle as the form that
	// does support one.
	//
	// This is the input Section 17.4's "link a standard Password
	// credential to it" binds: it is an ordinary input, so a
	// CredentialInputSource row can fill it from another credential's own
	// field with no network in the path.
	CertificateInputKeyUnlock = "key_unlock"

	// CertificateInputPFX maps onto MachinePFX.
	CertificateInputPFX = "pfx_bundle"
)

// VaultPasswordInput is the input id an Ansible Vault credential carries
// its password in. AWX's own name for the field.
const VaultPasswordInput = "vault_password"

// SourceFieldMetadataKey is the CredentialInputSource metadata key naming
// which field of a LINKED credential fills the bound input.
//
// It applies only when the source credential is an ordinary one rather than
// an external secret source. An external source addresses a secret with its
// own vocabulary (a mount, a path and a key, for HashiCorp Vault), decoded
// by that source's own LookupFactory. A linked credential has no address to
// decode: the value is already a field of a row this platform holds, so the
// only thing the binding has to say is which field, and this is where it
// says it.
//
// It lives here, beside the other input-id constants, because both the
// store that validates a binding at write time and the resolver that reads
// one at dispatch have to agree on it, and those are different packages.
const SourceFieldMetadataKey = "source_field"

// VaultFileLabel is the label the vault strategy generates its password
// file under, so a vault credential's file is distinguishable from one a
// file injector produced.
const VaultFileLabel = "vault"

// FileMode is the permission every generated credential file carries.
//
// It is a constant rather than a parameter because there is no credential
// file this platform generates that anything but the process reading it has
// any business opening, and a mode a caller can choose is a mode a caller
// can choose wrong.
const FileMode int64 = 0o600

// fileDirectory is where every generated credential file lives.
//
// It is under /run deliberately: on the legacy path this is inside an
// ephemeral container that is destroyed with the job, and a path under /run
// says that plainly to whoever reads a spec or a process listing.
const fileDirectory = "/run/pleiades/credentials"

// FilePath is the one place a generated credential file's path is decided.
//
// Both the writer and the reserved filename namespace read this result, so
// {{ tower.filename }} and the file the adapter actually creates cannot
// disagree. Computing it in two places is exactly how a credential type
// ends up pointing a client library at a path nothing wrote.
//
// The label is safe to concatenate because Injectors.Validate already
// refused anything outside ^[a-z0-9_]+$, which contains no separator and no
// dot, so no label can escape fileDirectory. injectors_fuzz_test.go holds
// that property over arbitrary documents rather than leaving it as a
// reading of the pattern.
func FilePath(credentialID int, label string) string {
	base := fileDirectory + "/" + strconv.Itoa(credentialID)
	if label == "" {
		return base
	}
	return base + "." + label
}

// File is one generated credential file.
type File struct {
	// Label is the multi-file label this file was generated under, and
	// empty for the single-file spelling. It is what
	// {{ tower.filename.<label> }} addresses.
	Label string

	// Path is the absolute path this file is written to wherever the job
	// runs, as computed by FilePath.
	Path string

	// Content is the rendered file body.
	Content string

	// Mode is the permission bits, always FileMode.
	Mode int64
}

// VaultPassword is one Ansible Vault identity a run must be given.
//
// It is carried separately from Files even though the password reaches
// ansible-playbook as a file, because the file alone is not enough: the
// command line has to name it with --vault-id <identifier>@<path>, and the
// identifier is what lets a playbook encrypted under two different vault
// identities be decrypted in one run.
type VaultPassword struct {
	// Identifier is the vault label, and empty for Ansible's own default
	// vault identity. Empty is a real value here rather than an absent one;
	// see credtype.Bound's own field comment.
	Identifier string

	// Path is where the password file lives.
	Path string
}

// Artifact is what one credential, or a combined set of them, injects.
//
// Every field is what an execution adapter consumes. Nothing here is a
// promise about how: the legacy adapter turns Env into a container
// environment and Files into container files, and the native adapter
// refuses both and consumes only ExtraVars and Machine. That divergence is
// PLAN.md Section 29.4's, not this type's.
type Artifact struct {
	// CredentialID and CredentialName name the credential this artifact was
	// rendered from, so a collision message can say which two credentials
	// clashed. Both are zero on an artifact returned by Combine, which by
	// construction has more than one source.
	CredentialID   int
	CredentialName string

	// Env maps an environment variable name to its rendered value.
	Env map[string]string

	// ExtraVars maps an extra-variable name to its rendered value, or to a
	// nested map whose leaves are rendered values.
	ExtraVars map[string]any

	// Files are the generated files, sorted by path so a spec built from
	// this artifact is byte-stable across runs.
	Files []File

	// machine and vault are not injector targets. They are Go values two
	// specific kinds resolve to, and they are unexported so that only their
	// own strategies can set them: an env or extra_vars injector document
	// must never be able to declare that its credential is the machine
	// identity a run authenticates as.
	machine map[string]string
	vault   []VaultPassword

	// secrets are the values in this artifact that must be scrubbed from
	// any captured output, filled in by the secret-tracking decorator as it
	// wraps each target.
	//
	// It exists because SECRECY IS NOT RECOVERABLE DOWNSTREAM. An execution
	// adapter holds an environment map and a file body and has no way to
	// tell which of them came from a secret input: a token and a region
	// look identical by the time they arrive. So the side that knows says
	// so, and it says so here.
	//
	// Getting this wrong in the permissive direction is worse than it
	// sounds. An adapter that registered every injected value would scrub
	// an ordinary URL, region or username out of every later line for the
	// rest of the process, which corrupts output without protecting
	// anything, and Credential.SecretValues' own doc comment names that as
	// the reason it returns only what is marked secret.
	secrets []string
}

// Machine returns the flattened machine credential this artifact carries,
// and whether it carries one at all.
//
// The map is the shape internal/credential.Flatten produces and
// wire.DispatchPayload.Secrets carries, so a caller assigns it straight
// across. See this file's own constants for why the keys are restated here
// rather than imported.
func (a Artifact) Machine() (map[string]string, bool) {
	if len(a.machine) == 0 {
		return nil, false
	}
	return a.machine, true
}

// Vault returns the vault identities this artifact carries, ordered by
// identifier so a run's --vault-id arguments are stable.
func (a Artifact) Vault() []VaultPassword {
	return a.vault
}

// SecretValues returns every value in this artifact that must be scrubbed
// from captured output, sorted longest first.
//
// Longest first because that is what the scrub requires: a password that is
// a prefix of a passphrase must not carve the passphrase in half and leave
// its tail exposed. Returning them already in that order means no caller
// can get it wrong.
//
// This is what crosses the wire so an execution adapter, in a different
// process from the one that rendered these, can mask them. See the secrets
// field's own comment for why it cannot work out the answer itself.
func (a Artifact) SecretValues() []string {
	out := append([]string(nil), a.secrets...)
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// trackSecret records a value as one that must be masked, ignoring
// duplicates and anything too short to mask safely.
//
// The length bound is redact.MinLiteralLength's, restated as a call rather
// than a constant here: registering a short or common value would scrub
// that substring out of every unrelated later line, which is the same
// reason redact.Literals.Add refuses one.
func (a *Artifact) trackSecret(values ...string) {
	for _, v := range values {
		if len(v) < redact.MinLiteralLength {
			continue
		}
		if slices.Contains(a.secrets, v) {
			continue
		}
		a.secrets = append(a.secrets, v)
	}
}

// Empty reports whether this artifact injects nothing at all.
func (a Artifact) Empty() bool {
	return len(a.Env) == 0 && len(a.ExtraVars) == 0 && len(a.Files) == 0 &&
		len(a.machine) == 0 && len(a.vault) == 0
}

// Combine merges per-credential artifacts into the one artifact a dispatch
// injects, refusing any collision rather than picking a winner.
//
// Five things can collide, and each names both credentials:
//
//   - the same environment variable
//   - the same extra-variable leaf (nested maps deep-merge; only leaves
//     collide)
//   - the same generated file path
//   - two machine identities
//   - two vault credentials sharing an identifier
//
// The last two duplicate what CheckBinding already refuses at the binding.
// That is deliberate rather than redundant: CheckBinding runs against
// stored bindings, and a launch can also carry credentials that were not
// stored, so this is the check that runs on what is actually about to be
// injected.
func Combine(arts ...Artifact) (Artifact, error) {
	out := Artifact{
		Env:       make(map[string]string),
		ExtraVars: make(map[string]any),
	}

	envOwner := make(map[string]string)
	varOwner := make(map[string]string)
	fileOwner := make(map[string]string)
	vaultOwner := make(map[string]string)
	machineOwner := ""

	for _, a := range arts {
		for name, value := range a.Env {
			if previous, clash := envOwner[name]; clash {
				return Artifact{}, fmt.Errorf(
					"%w: %q and %q both inject the environment variable %s",
					ErrInjectorConflict, previous, a.CredentialName, name)
			}
			envOwner[name] = a.CredentialName
			out.Env[name] = value
		}

		if err := mergeVars(out.ExtraVars, a.ExtraVars, nil, varOwner, a.CredentialName); err != nil {
			return Artifact{}, err
		}

		for _, f := range a.Files {
			if previous, clash := fileOwner[f.Path]; clash {
				return Artifact{}, fmt.Errorf(
					"%w: %q and %q both generate the file %s",
					ErrInjectorConflict, previous, a.CredentialName, f.Path)
			}
			fileOwner[f.Path] = a.CredentialName
			out.Files = append(out.Files, f)
		}

		if machine, ok := a.Machine(); ok {
			if machineOwner != "" {
				return Artifact{}, fmt.Errorf(
					"%w: %q and %q are both machine credentials, and a run authenticates as exactly one identity",
					ErrInjectorConflict, machineOwner, a.CredentialName)
			}
			machineOwner = a.CredentialName
			out.machine = machine
		}

		for _, v := range a.vault {
			if previous, clash := vaultOwner[v.Identifier]; clash {
				return Artifact{}, fmt.Errorf(
					"%w: %q and %q are both vault credentials with the identifier %s",
					ErrInjectorConflict, previous, a.CredentialName, describeVaultIdentifier(v.Identifier))
			}
			vaultOwner[v.Identifier] = a.CredentialName
			out.vault = append(out.vault, v)
		}

		out.trackSecret(a.secrets...)
	}

	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	sort.Slice(out.vault, func(i, j int) bool { return out.vault[i].Identifier < out.vault[j].Identifier })

	if len(out.Env) == 0 {
		out.Env = nil
	}
	if len(out.ExtraVars) == 0 {
		out.ExtraVars = nil
	}
	return out, nil
}

// mergeVars deep-merges src into dst, recording per-leaf ownership so a
// collision message can name both credentials.
//
// Nested maps merge rather than collide: two credentials each contributing
// a different leaf under the same parent is not a conflict, and refusing it
// would make a nested injector document unusable alongside any other
// credential. Only a leaf written twice is a real disagreement about what
// value a run should see.
func mergeVars(dst, src map[string]any, path []string, owner map[string]string, credential string) error {
	for name, value := range src {
		here := append(append([]string{}, path...), name)
		where := strings.Join(here, ".")

		nested, isMap := value.(map[string]any)
		if isMap {
			existing, present := dst[name]
			if !present {
				child := make(map[string]any, len(nested))
				dst[name] = child
				if err := mergeVars(child, nested, here, owner, credential); err != nil {
					return err
				}
				continue
			}
			existingMap, existingIsMap := existing.(map[string]any)
			if !existingIsMap {
				return fmt.Errorf(
					"%w: %q and %q both inject the extra variable %s, one as a value and one as a nested map",
					ErrInjectorConflict, owner[where], credential, where)
			}
			if err := mergeVars(existingMap, nested, here, owner, credential); err != nil {
				return err
			}
			continue
		}

		if previous, clash := owner[where]; clash {
			return fmt.Errorf(
				"%w: %q and %q both inject the extra variable %s",
				ErrInjectorConflict, previous, credential, where)
		}
		if _, occupied := dst[name]; occupied {
			// A leaf landing where a nested map already is. The owner map
			// has no entry for this exact path (the map's own leaves are
			// what it recorded), so the message names the parent.
			return fmt.Errorf(
				"%w: %q injects the extra variable %s as a value, where another credential already injected a nested map",
				ErrInjectorConflict, credential, where)
		}
		owner[where] = credential
		dst[name] = value
	}
	return nil
}

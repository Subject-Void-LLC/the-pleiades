package credtype

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// The injection strategies: one Target per thing a credential can produce.
//
// PLAN.md Section 29.2 names three injector targets and two kinds that
// resolve to a Go value instead. This file has five Targets rather than one
// function with a five-armed switch, and the reason is the Pattern Entry
// Gate's own: a switch is edited by whoever adds a target, and every
// existing arm is in that edit's blast radius. A Target registers.
//
// The decorator is what makes that safe. secretTracking wraps every Target,
// including one added later by somebody who never read this file, and
// registers whatever it produced from a secret input with the masking
// ruleset. A new Target cannot forget to do that, because it is not the
// thing doing it.

// Phase says when a Target runs relative to the reserved filename
// namespace.
//
// The ordering is forced rather than chosen: an env or extra_vars template
// may write {{ tower.filename.cert }}, and that value does not exist until
// the file target has decided where the cert file goes. Two phases is the
// smallest thing that expresses it.
type Phase int

const (
	// PhaseFiles runs before the reserved namespace exists. A Target in
	// this phase renders against the credential's inputs alone.
	PhaseFiles Phase = iota

	// PhaseValues runs after, against the inputs plus the reserved
	// namespace.
	PhaseValues
)

// Request is everything a Target needs to render one credential.
type Request struct {
	// Credential is the credential being injected, with real values and
	// defaults already filled in.
	Credential Credential

	// Engine compiles the templates. Never nil: NewInjector refuses one.
	Engine render.Engine

	// Vars is what a template evaluates against for this phase. See Phase.
	Vars map[string]any
}

// Target renders one credential into one part of an Artifact.
//
// An implementation must be safe for concurrent use and must not retain
// anything from a Request past the call: a Request carries plaintext.
type Target interface {
	// Key names this target, for registration and for a log line. The
	// three data targets use their own injector-document key.
	Key() string

	// Phase says when this target runs.
	Phase() Phase

	// Apply renders this target's share of req into art. A Target that has
	// nothing to do for this credential returns nil without touching art.
	Apply(req Request, art *Artifact) error
}

// targets is the process-wide strategy table.
//
// Registration happens in this package's own init below rather than through
// a builtins.go blank import, unlike internal/catalog: a Target is not an
// extension point an operator adds, it is one of the five things PLAN.md
// Section 29.2 defines, and there is no out-of-tree caller that could
// register a sixth. What the Registry buys here is that adding one is an
// addition rather than an edit to a switch every other target shares.
var targets = registry.New[Target]()

func init() {
	targets.MustRegister("file", fileTarget{})
	targets.MustRegister("vault", vaultTarget{})
	targets.MustRegister("env", envTarget{})
	targets.MustRegister("extra_vars", extraVarsTarget{})
	targets.MustRegister("machine", machineTarget{})
}

// orderedTargets returns every registered Target in a deterministic
// execution order: files first, then values, and within a phase by key.
//
// The within-phase ordering does not affect the result (no two targets in
// one phase write the same part of an artifact) but it does affect the
// order errors are reported in, and a validation failure that moves between
// runs is a validation failure nobody can write a test for.
func orderedTargets() []Target {
	all := targets.All()
	out := make([]Target, 0, len(all))
	for _, t := range all {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Phase() != out[j].Phase() {
			return out[i].Phase() < out[j].Phase()
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// fileTarget generates the files a type's file injector declares.
type fileTarget struct{}

func (fileTarget) Key() string  { return "file" }
func (fileTarget) Phase() Phase { return PhaseFiles }

// Apply renders each file template and records where it will be written.
//
// It writes nothing. The path comes from FilePath, which is the same call
// the reserved namespace makes, so a template addressing a file and the
// adapter creating it cannot disagree about where it is.
func (fileTarget) Apply(req Request, art *Artifact) error {
	inj := req.Credential.Type.Injectors
	if len(inj.File) == 0 {
		return nil
	}

	for _, label := range inj.FileLabels() {
		key := FileTemplateKey
		if label != "" {
			key = fileTemplatePrefix + label
		}
		tmpl, ok := inj.File[key]
		if !ok {
			// Unreachable for a document FileLabels derived its labels
			// from, and reported rather than assumed: a silent skip here
			// would be a credential file the run expects and never gets.
			return fmt.Errorf("%w: credential %q declares the file label %q with no template",
				ErrInjection, req.Credential.Name, label)
		}
		content, err := renderOne(req, "file "+key, tmpl)
		if err != nil {
			return err
		}
		art.Files = append(art.Files, File{
			Label:   label,
			Path:    FilePath(req.Credential.ID, label),
			Content: content,
			Mode:    FileMode,
		})
	}
	return nil
}

// vaultTarget turns an Ansible Vault credential into a password file plus
// the identity that names it on the command line.
//
// It is real code rather than an injector document because its output is
// not one of the three data targets: a vault password reaches
// ansible-playbook as --vault-id <identifier>@<path>, which is an argument,
// and no injector document can produce an argument.
//
// The generated file is deliberately NOT part of the reserved filename
// namespace. That namespace addresses files a type's own file injector
// declared, so a template can name them; this file is named by an argument
// the adapter builds, and putting it in the namespace would offer authors a
// path whose meaning is decided elsewhere.
type vaultTarget struct{}

func (vaultTarget) Key() string  { return "vault" }
func (vaultTarget) Phase() Phase { return PhaseFiles }

// Apply records the vault password file and its identity.
func (vaultTarget) Apply(req Request, art *Artifact) error {
	cred := req.Credential
	if cred.Type.Kind != KindVault {
		return nil
	}

	password := cred.Inputs[VaultPasswordInput]
	if password == "" {
		return fmt.Errorf("%w: vault credential %q carries no %s",
			ErrInjection, cred.Name, VaultPasswordInput)
	}

	path := FilePath(cred.ID, VaultFileLabel)
	art.Files = append(art.Files, File{
		Label: VaultFileLabel,
		Path:  path,
		// A trailing newline, because ansible-playbook reads a vault
		// password file as a single line and strips exactly one: a file
		// written without it works, and a file written with two would
		// authenticate with a password nobody typed.
		Content: password + "\n",
		Mode:    FileMode,
	})
	art.vault = append(art.vault, VaultPassword{
		Identifier: VaultIdentifierOf(cred.Type.Kind, cred.Inputs),
		Path:       path,
	})
	return nil
}

// envTarget renders a type's environment-variable injector.
type envTarget struct{}

func (envTarget) Key() string  { return "env" }
func (envTarget) Phase() Phase { return PhaseValues }

// Apply renders each environment variable's template.
//
// It re-checks the variable name, which Injectors.Validate already checked
// at save time. That is a run-time backstop rather than a redundancy: the
// name check is what refuses LD_PRELOAD and the BASH_FUNC_ prefix, this
// injector document reaches here out of a database row, and PLAN.md Section
// 29.3's own residual note says a direct SQL writer can bypass the
// application's checks. A control worth having at the write is worth
// repeating at the read when the read is what actually executes.
func (envTarget) Apply(req Request, art *Artifact) error {
	inj := req.Credential.Type.Injectors
	if len(inj.Env) == 0 {
		return nil
	}
	if art.Env == nil {
		art.Env = make(map[string]string, len(inj.Env))
	}

	for _, name := range sortedKeys(inj.Env) {
		if err := validateEnvName(name); err != nil {
			return fmt.Errorf("%w: credential %q: %s", ErrInjection, req.Credential.Name, err)
		}
		value, err := renderOne(req, "env "+name, inj.Env[name])
		if err != nil {
			return err
		}
		if value == "" && inj.OmitsEmpty(name) {
			// Unset rather than set-to-empty. See Injectors.OmitEmpty for
			// why the two are different to the thing reading the variable.
			continue
		}
		art.Env[name] = value
	}
	return nil
}

// extraVarsTarget renders a type's extra-variable injector, including
// nested structures.
type extraVarsTarget struct{}

func (extraVarsTarget) Key() string  { return "extra_vars" }
func (extraVarsTarget) Phase() Phase { return PhaseValues }

// Apply renders the extra-variable tree.
func (extraVarsTarget) Apply(req Request, art *Artifact) error {
	inj := req.Credential.Type.Injectors
	if len(inj.ExtraVars) == 0 {
		return nil
	}
	rendered, err := renderVarTree(req, inj.ExtraVars, nil)
	if err != nil {
		return err
	}
	if art.ExtraVars == nil {
		art.ExtraVars = make(map[string]any, len(rendered))
	}
	for name, value := range rendered {
		art.ExtraVars[name] = value
	}
	return nil
}

// machineTarget turns a machine credential into the flattened identity the
// transport authenticates with.
//
// Like vaultTarget this is real code rather than data, and for the same
// kind of reason: its output is a Go value the SSH transport consumes
// directly (internal/engine's TransportActionExecutor takes a credential
// store, not an environment), and no injector document can produce one.
type machineTarget struct{}

func (machineTarget) Key() string  { return "machine" }
func (machineTarget) Phase() Phase { return PhaseValues }

// Apply maps a kind's transport inputs onto their flattened keys.
//
// Only the inputs listed below are read. Every other input a machine type
// declares, become_password among them, is an ordinary input its own
// injector document can reference; see this package's MachineInput* and
// CertificateInput* constants for why that is the design rather than an
// omission.
//
// # Why a network credential is a machine credential here
//
// Two kinds share the password mapping, and the second one is a judgement
// worth writing down. AWX's net credential type declares exactly these four
// inputs under exactly these ids, and AWX consumes them by handing them to
// the network connection plugins, which reach the device over SSH. This
// platform's only transport is that same SSH, so the four values mean the
// same thing here that they mean there.
//
// The alternative was to ship net with no target at all, and that is the
// worse answer rather than the more cautious one: the type would store a
// username and a private key that nothing ever read, which is
// FAILURE_PATTERNS.md #116's shape, and the operator's run would fail to
// authenticate against a device whose credential they had correctly filled
// in. The kinds stay distinct everywhere else, including in the
// one-credential-per-kind binding rule, so a template may still bind one
// machine credential and one network credential.
//
// # Why a certificate is a machine identity rather than a third thing
//
// KindCryptography joined this target in Phase 78d rather than getting one
// of its own, and that is the load-bearing decision rather than a tidying
// one. A client certificate answers "who does this run authenticate as",
// which is the same question a username and password answer, so it belongs
// in the same slot. Putting it there is what makes Combine's existing
// refusal apply to it for free: an artifact may carry exactly one machine
// identity, so binding both a machine credential and a certificate
// credential to one template is refused with a message that says a run
// authenticates as exactly one identity, and no new rule had to be written
// to get that.
//
// The consequence is real and is stated here rather than discovered later:
// one template cannot reach Linux over SSH and Windows by certificate in
// the same run. That is the pre-existing one-identity rule doing what it
// says, not a limitation this stage introduced.
func (machineTarget) Apply(req Request, art *Artifact) error {
	cred := req.Credential

	var mapping []struct{ input, key string }
	switch cred.Type.Kind {
	case KindSSH, KindNet:
		mapping = []struct{ input, key string }{
			{MachineInputUsername, MachineUsername},
			{MachineInputPassword, MachinePassword},
			{MachineInputKeyData, MachinePrivateKey},
			{MachineInputKeyUnlock, MachinePassphrase},
		}
	case KindCryptography:
		mapping = []struct{ input, key string }{
			{CertificateInputCertificate, MachineCertificate},
			{CertificateInputPrivateKey, MachinePrivateKey},
			{CertificateInputKeyUnlock, MachinePassphrase},
			{CertificateInputPFX, MachinePFX},
		}
	default:
		return nil
	}

	machine := make(map[string]string, len(mapping))
	for _, m := range mapping {
		// An absent key rather than an empty value, matching
		// internal/credential.Flatten exactly: a Collection method reading
		// this map distinguishes "this device has no key" from "this device
		// has a key that happens to be empty" by presence.
		if v := cred.Inputs[m.input]; v != "" {
			machine[m.key] = v
		}
	}
	if len(machine) == 0 {
		return nil
	}
	claims, err := checkCertificateMaterial(cred, machine)
	if err != nil {
		return err
	}
	if !claims {
		return nil
	}
	art.machine = machine
	return nil
}

// checkCertificateMaterial decides whether a cryptography credential is a
// machine identity at all, and refuses one that is a contradictory half of
// one.
//
// # Why it asks that question rather than validating every cryptography credential
//
// KindCryptography is "a signing or verification key" and AWX registers its
// GPG Public Key type under it, so the kind covers far more than client
// certificates and any operator may choose it for a custom type. This
// target therefore cannot treat every cryptography credential as a
// certificate: the deciding evidence is a CERTIFICATE or a BUNDLE, not the
// kind and not an input that happens to be called private_key.
//
// Getting this wrong is not theoretical. A code-signing credential with an
// input named private_key and an env injector is an ordinary thing to have,
// it works today, and an earlier draft of this function turned every launch
// binding one into an injection-time refusal complaining about a missing
// certificate it was never meant to carry. Worse, a type declaring only
// key_unlock would have claimed the single machine-identity slot while
// carrying no identity, so Combine would refuse a template for a conflict
// with something that is not a credential for authenticating as anyone.
//
// So the answer is a bool: claims tells Apply whether to take the slot.
// False means this credential is not a machine identity and its inputs
// belong to its own injector document, which is exactly what they did
// before this target learned the kind.
//
// It runs at injection rather than only at the transport because this is
// where the credential is still identifiable. By the time these values
// reach a Collection method they are an anonymous map, and the error a
// transport can raise there names a device rather than the credential an
// operator has to go and edit.
//
// A bundle and a loose pair are alternatives, never a pair of fallbacks.
// Silently preferring one would mean a run authenticating with material the
// operator did not choose, which is FAILURE_PATTERNS.md #116's shape again
// and the same reason ErrInjectorConflict exists.
func checkCertificateMaterial(cred Credential, machine map[string]string) (claims bool, err error) {
	if cred.Type.Kind != KindCryptography {
		// SSH and net credentials reached here, and they are machine
		// identities by definition of their own kinds.
		return true, nil
	}

	_, hasBundle := machine[MachinePFX]
	_, hasCert := machine[MachineCertificate]
	_, hasKey := machine[MachinePrivateKey]

	switch {
	case hasBundle && (hasCert || hasKey):
		return false, fmt.Errorf("%w: credential %q carries a %s bundle and a separate %s or %s, and those are two "+
			"ways to supply one identity rather than a pair of fallbacks: keep whichever the target expects and clear the other",
			ErrInvalidCredential, cred.Name, CertificateInputPFX,
			CertificateInputCertificate, CertificateInputPrivateKey)

	case hasCert && !hasKey:
		// A certificate is unambiguous evidence that this credential means
		// to authenticate, so a missing key is an error rather than a
		// reason to walk away.
		return false, fmt.Errorf("%w: credential %q has a %s but no %s, and a certificate alone cannot prove possession",
			ErrInvalidCredential, cred.Name, CertificateInputCertificate, CertificateInputPrivateKey)

	case hasCert || hasBundle:
		return true, nil

	default:
		// A key with no certificate, or a passphrase on its own. Not a
		// machine identity, and deliberately not an error: this is what a
		// signing credential looks like, and refusing it here would break
		// something that never had anything to do with this feature.
		return false, nil
	}
}

// secretTracking is the Decorator every Target is wrapped in.
//
// It registers two things with the masking ruleset before they can reach a
// log line: the credential's own secret input values, and any value a
// Target just produced that contains one. The second is what covers a
// derived value, "Bearer <token>" being the common shape, whose sensitive
// half would otherwise only be masked as a substring of a longer line.
//
// It is a wrapper rather than a call inside each Target for the reason the
// pattern exists: a Target added later is wrapped by NewInjector without
// its author doing anything, so the one thing that must never be forgotten
// is not something anybody has to remember.
type secretTracking struct {
	inner    Target
	literals *redact.Literals
}

func (t secretTracking) Key() string  { return t.inner.Key() }
func (t secretTracking) Phase() Phase { return t.inner.Phase() }

// Apply registers whatever inner produced from a secret input, in two
// places: this process's own masking set, and the artifact itself.
//
// Both are needed and they serve different readers. The Literals set covers
// THIS process, which is the Controller, where an injection failure is
// about to be logged. The artifact's own list crosses the wire, because the
// adapter that will actually run this material lives in a different process
// with a different masking set and no way to work out which of the values
// it was handed are secret.
//
// It diffs the artifact rather than asking the Target what it did, which is
// what keeps it decoupled: it needs no knowledge of any Target's internals,
// and it works identically for one that does not exist yet.
func (t secretTracking) Apply(req Request, art *Artifact) error {
	secrets := req.Credential.SecretValues()
	if len(secrets) == 0 {
		return t.inner.Apply(req, art)
	}

	before := artifactValues(art)
	err := t.inner.Apply(req, art)

	// Recorded even on failure, deliberately: a partially applied artifact
	// can still have put a rendered secret somewhere, and the error being
	// returned is about to be logged.
	art.trackSecret(secrets...)
	if t.literals != nil {
		t.literals.Add(secrets...)
	}
	if err != nil {
		return err
	}

	for value := range artifactValues(art) {
		if _, seen := before[value]; seen {
			continue
		}
		if containsAny(value, secrets) {
			art.trackSecret(value)
			if t.literals != nil {
				t.literals.Add(value)
			}
		}
	}
	return nil
}

// artifactValues collects every string an artifact currently holds, as a
// set, so Apply can tell what its inner Target just added.
func artifactValues(art *Artifact) map[string]struct{} {
	out := make(map[string]struct{})
	for _, v := range art.Env {
		out[v] = struct{}{}
	}
	collectVarValues(art.ExtraVars, out)
	for _, f := range art.Files {
		out[f.Content] = struct{}{}
	}
	for _, v := range art.machine {
		out[v] = struct{}{}
	}
	return out
}

// collectVarValues walks a rendered extra-variable tree, adding every
// string leaf.
func collectVarValues(vars map[string]any, out map[string]struct{}) {
	for _, value := range vars {
		switch v := value.(type) {
		case string:
			out[v] = struct{}{}
		case map[string]any:
			collectVarValues(v, out)
		}
	}
}

// containsAny reports whether text contains any of the given secrets.
func containsAny(text string, secrets []string) bool {
	for _, s := range secrets {
		if s != "" && len(s) <= len(text) && strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// sortedKeys returns a map's keys in sorted order, so rendering reports its
// first failure deterministically.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Package managed is the catalog of credential types this platform ships.
//
// # What this package is, and the correction it records
//
// The plan for this phase said roughly twenty of AWX's managed credential
// types have injectors that are pure data, and that shipping them was
// therefore a matter of copying documents. That is not true of AWX, and
// finding out how untrue changed what this package could honestly contain.
// AGENTS.md's Architecture Mismatch protocol asks for the map to be
// corrected before the code, so the correction is here, next to the data it
// governs, rather than only in a document beside it.
//
// AWX's managed types live in awx_plugins.credentials.plugins, which is the
// authority rather than any prose about it. Of the twenty-two types it
// registers:
//
//   - Seven build their environment in PYTHON, through a custom_injectors
//     function: aws, gce, azure_rm, openstack, vmware,
//     kubernetes_bearer_token and terraform. Their injector document is
//     empty. AWX's API serializes it as {}.
//   - Two have a data document that uses Jinja control flow ({% if %}):
//     insights and rhv. internal/render refuses control-flow blocks by
//     design, loudly rather than by passing them through as text.
//   - One has a data document this platform expresses exactly: controller.
//   - Twelve declare no injectors at all. Their values are consumed
//     structurally by a subsystem rather than injected: project sync,
//     webhooks, execution-environment pulls, content signing, or an
//     inventory plugin.
//
// So the honest count of AWX types that are a document this platform can
// copy is one, not twenty. What this package ships instead is six types
// whose BEHAVIOUR this platform reproduces, which is the property that
// actually decides whether a migrated playbook runs, and sixteen declared
// and not implemented with a per-type reason.
//
// # Why behaviour rather than the document is the right fidelity test
//
// For the seven Python types there is no document to be faithful to. What
// a customer's playbook observes is an environment, so an environment is
// what has to match. That is what licenses credtype.Injectors.OmitEmpty,
// the one field on that struct AWX does not have: it expresses in data the
// has_input condition AWX expresses in code, and it is what makes the aws
// type here produce byte-identical output rather than an approximation.
//
// # Declared and not implemented, rather than absent
//
// A namespace named here with a reason is worth more to somebody migrating
// than a missing one. It is the same choice internal/catalog makes for
// module FQCNs and internal/credtype makes for external secret sources: an
// import that meets gce reports what is missing and why, instead of
// reporting a namespace it has never heard of, which reads to an operator
// as a typo in their own export.
package managed

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// typeData holds one JSON document per credential type this platform ships.
//
// One file per type rather than one document holding all of them, so that
// adding a type is a new file and a diff nobody has to read around, and so
// that each file is a thing an operator can compare against their own AWX
// export directly.
//
//go:embed types/*.json
var typeData embed.FS

// shipped is the parsed catalog, keyed by namespace.
var shipped = mustParse()

// mustParse decodes every embedded document at startup.
//
// It panics rather than returning an error, and that is the same judgement
// regexp.MustCompile makes at package scope throughout this repository. The
// data is compiled into the binary: a failure here cannot depend on input,
// environment, configuration or timing, so there is no run-time condition
// for a caller to handle and an error return would only move an impossible
// branch into every caller. TestEveryShippedTypeParsesAndValidates proves
// the branch is not taken.
func mustParse() map[string]credtype.CredentialType {
	out, err := parseTypes(typeData, "types")
	if err != nil {
		panic("credtype/managed: " + err.Error())
	}
	return out
}

// parseTypes decodes a directory of credential type documents.
//
// Split out from mustParse so its guards can be tested. The panic above is
// unreachable for the embedded data, which is exactly why the checks in
// here would otherwise never be exercised: they are the ones that catch a
// document somebody adds later, and a guard nothing tests is a guard that
// can be wrong in the direction of accepting what it was meant to refuse.
func parseTypes(files fs.FS, dir string) (map[string]credtype.CredentialType, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, fmt.Errorf("reading the embedded types: %w", err)
	}

	out := make(map[string]credtype.CredentialType, len(entries))
	for _, entry := range entries {
		name := path.Join(dir, entry.Name())
		raw, readErr := fs.ReadFile(files, name)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", name, readErr)
		}

		var ct credtype.CredentialType
		decoder := json.NewDecoder(bytes.NewReader(raw))
		// An unknown key is a mistake in data this repository owns, most
		// likely a field copied from an AWX export that this platform does
		// not model. Ignoring it would mean a document that looks like it
		// declares something it does not.
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&ct); decodeErr != nil {
			return nil, fmt.Errorf("decoding %s: %w", name, decodeErr)
		}

		// The file name and the namespace are two statements of the same
		// fact, so they are held together here rather than allowed to drift
		// into a file whose contents describe a different type.
		//
		// This also makes a duplicate namespace unrepresentable, which is
		// why there is no separate check for one: two documents claiming a
		// namespace would both have to be named <namespace>.json, and a
		// directory cannot hold two files with one name. An explicit
		// duplicate check was written here first and removed when its test
		// could not construct an input that reached it.
		if want := ct.Namespace + ".json"; entry.Name() != want {
			return nil, fmt.Errorf("%s declares namespace %s, so it should be named %s", name, ct.Namespace, want)
		}
		out[ct.Namespace] = ct
	}
	return out, nil
}

// Types returns every credential type this platform ships, sorted by
// namespace.
//
// Sorted rather than in map order because the reconcile writes them in this
// order and a log of a startup should read the same way twice.
func Types() []credtype.CredentialType {
	out := make([]credtype.CredentialType, 0, len(shipped))
	for _, ct := range shipped {
		out = append(out, ct)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}

// Type returns the shipped type with this namespace.
func Type(namespace string) (credtype.CredentialType, bool) {
	ct, ok := shipped[namespace]
	return ct, ok
}

// Has reports whether this platform ships a type under this namespace.
//
// It exists so a caller asking the yes-or-no question does not have to
// discard a whole credential type to get an answer, and so the import
// command's classification reads as the three questions it is actually
// asking rather than as one lookup and two error comparisons.
func Has(namespace string) bool {
	_, ok := shipped[namespace]
	return ok
}

// Reason says why a declared AWX credential type is not implemented here.
//
// It is a closed vocabulary rather than free text on each entry, so the
// sixteen reasons collapse into four groups a reader can hold in their head,
// and so a document can render them as a table without restating each one.
type Reason string

// The four reasons, each naming what would have to exist for the type to
// become implementable.
const (
	// ReasonPythonInjectors is AWX building the environment in code, with
	// branching this platform's data-driven injectors cannot express.
	// Implementing one means deciding, per type, whether its conditionality
	// is expressible as data the way aws turned out to be.
	ReasonPythonInjectors Reason = "awx builds this type's environment in python rather than in its injector document"

	// ReasonControlFlow is a data document that uses Jinja control flow.
	// internal/render refuses {% %} blocks deliberately, because passing an
	// unsupported block through as literal text is how an author comes to
	// believe a conditional ran.
	ReasonControlFlow Reason = "awx's injector document for this type uses jinja control flow, which this platform's renderer refuses rather than passing through as text"

	// ReasonNoSubsystem is a type AWX injects nothing for because something
	// else consumes it: project sync, a webhook, an execution-environment
	// pull, content signing, or an inventory plugin. The type is not the
	// missing piece; the subsystem is.
	ReasonNoSubsystem Reason = "this type is consumed by a subsystem this platform has not built, so storing one would be storing a secret nothing reads"

	// ReasonExternalSource is a credential whose behaviour is a lookup
	// rather than an injection. These are represented by
	// credtype.DeclaredLookups instead, and shipping them here as well
	// would be two answers to one question.
	ReasonExternalSource Reason = "this is an external secret source rather than an injected credential, and it is declared as a lookup instead"
)

// NotImplemented is one AWX credential type this platform recognises and
// does not implement.
type NotImplemented struct {
	// Namespace is AWX's own stable identifier, which is what an import
	// keys on. It is the reason this list exists in code rather than prose.
	Namespace string

	// Name is AWX's display name, so a report reads the way the operator's
	// own AWX does.
	Name string

	// Kind is AWX's coarse grouping.
	Kind credtype.Kind

	// Reason is why it is not implemented.
	Reason Reason

	// Detail names the specific thing that is missing, where the reason
	// alone would leave an operator guessing which subsystem or which
	// conditional is meant.
	Detail string
}

// notImplemented is every AWX managed credential type this platform does
// not implement, in AWX's own namespaces.
//
// Sorted by namespace, and complete against AWX's registry:
// TestTheCatalogCoversEveryAWXManagedType asserts that the shipped types and
// this list together account for every namespace AWX registers, so a type
// AWX adds shows up as a failing test rather than as an import that reports
// an unknown namespace.
var notImplemented = []NotImplemented{
	{
		Namespace: "aws_secretsmanager", Name: "AWS Secrets Manager lookup", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built, and its own credentials (an access key, or an instance role) are a second authentication story this platform has not designed",
	},
	{
		Namespace: "azure_kv", Name: "Microsoft Azure Key Vault", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built, and authenticating to it means a service principal or a managed identity, neither of which this platform can obtain yet",
	},
	{
		Namespace: "azure_rm", Name: "Microsoft Azure Resource Manager", Kind: credtype.KindCloud,
		Reason: ReasonPythonInjectors,
		Detail: "it chooses between service-principal and username authentication at run time, setting a different set of variables for each, and that branch is not a value a template can render",
	},
	{
		Namespace: "bitbucket_dc_token", Name: "Bitbucket Data Center HTTP Access Token", Kind: credtype.KindToken,
		Reason: ReasonNoSubsystem,
		Detail: "it authenticates project source-control sync and webhooks, neither of which exists here",
	},
	{
		Namespace: "centrify_vault", Name: "Centrify Vault Credential Provider", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built, and it is the least commonly deployed of the eight, so it is last rather than next",
	},
	{
		Namespace: "conjur", Name: "CyberArk Conjur Secrets Manager Lookup", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built; CyberArk is the external secret store the credential design names explicitly, so this one is a real commitment rather than a courtesy declaration",
	},
	{
		Namespace: "gce", Name: "Google Compute Engine", Kind: credtype.KindCloud,
		Reason: ReasonPythonInjectors,
		Detail: "it assembles a service-account JSON document around the private key and varies its environment depending on whether an inventory update is running",
	},
	{
		Namespace: "galaxy_api_token", Name: "Ansible Galaxy/Automation Hub API Token", Kind: credtype.KindGalaxy,
		Reason: ReasonNoSubsystem,
		Detail: "it authenticates collection installation during project sync, which is Run tier",
	},
	{
		Namespace: "github_token", Name: "GitHub Personal Access Token", Kind: credtype.KindToken,
		Reason: ReasonNoSubsystem,
		Detail: "it authenticates webhook callbacks, which do not exist here",
	},
	{
		Namespace: "gitlab_token", Name: "GitLab Personal Access Token", Kind: credtype.KindToken,
		Reason: ReasonNoSubsystem,
		Detail: "it authenticates webhook callbacks, which do not exist here",
	},
	{
		Namespace: "gpg_public_key", Name: "GPG Public Key", Kind: credtype.KindCryptography,
		Reason: ReasonNoSubsystem,
		Detail: "it validates content signatures during project sync, and there is no project sync to validate",
	},
	{
		Namespace: "hashivault_ssh", Name: "HashiCorp Vault Signed SSH", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "it signs an SSH certificate rather than reading a stored value, so it needs a credential type that can hold a signed certificate and a transport that will present one, and neither exists",
	},
	{
		Namespace: "insights", Name: "Insights", Kind: credtype.KindInsights,
		Reason: ReasonControlFlow,
		Detail: "its authentication extra variable is a {% if client_id %} block choosing between service-account and basic authentication",
	},
	{
		Namespace: "kubernetes_bearer_token", Name: "OpenShift or Kubernetes API Bearer Token", Kind: credtype.KindKubernetes,
		Reason: ReasonPythonInjectors,
		Detail: "it writes a certificate authority file and sets the verify flag only when both a CA and verification are configured, and it needs the file injector the native path refuses",
	},
	{
		Namespace: "openstack", Name: "OpenStack", Kind: credtype.KindCloud,
		Reason: ReasonPythonInjectors,
		Detail: "it builds a clouds.yaml document whose keys are present or absent depending on which optional inputs were filled in",
	},
	{
		Namespace: "registry", Name: "Container Registry", Kind: credtype.KindRegistry,
		Reason: ReasonNoSubsystem,
		Detail: "it authenticates execution-environment image pulls, and this platform's legacy adapter runs one pinned image it does not pull per job",
	},
	{
		Namespace: "rhv", Name: "Red Hat Virtualization", Kind: credtype.KindCloud,
		Reason: ReasonControlFlow,
		Detail: "its generated ini file ends with a {% if ca_file %} block, so the document cannot render without control flow even though its environment could",
	},
	{
		Namespace: "satellite6", Name: "Red Hat Satellite 6", Kind: credtype.KindCloud,
		Reason: ReasonNoSubsystem,
		Detail: "awx declares no injectors for it at all and consumes it through the satellite inventory plugin, which is an inventory source this platform does not have",
	},
	{
		Namespace: "terraform", Name: "Terraform backend configuration", Kind: credtype.KindCloud,
		Reason: ReasonPythonInjectors,
		Detail: "it writes one or two files depending on whether Google Cloud credentials were supplied, and it needs the file injector the native path refuses",
	},
	{
		Namespace: "thycotic_dsv", Name: "Thycotic DevOps Secrets Vault", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built, and it authenticates with a client id and secret exchanged for a short-lived token, so a source would have to hold and refresh that token rather than present a static one",
	},
	{
		Namespace: "thycotic_tss", Name: "Thycotic Secret Server", Kind: credtype.KindExternal,
		Reason: ReasonExternalSource,
		Detail: "no client for it is built, and it is a different product and API from the DevOps Secrets Vault above despite the shared vendor name, so implementing one does not give the other",
	},
	{
		Namespace: "vmware", Name: "VMware vCenter", Kind: credtype.KindCloud,
		Reason: ReasonNoSubsystem,
		Detail: "three of its four variables are a straight copy of its inputs, but the fourth is VMWARE_VALIDATE_CERTS, which awx reads from a deployment-wide setting rather than from the credential, and this platform has no settings surface to read it from",
	},
}

// DeclaredNotImplemented returns every AWX credential type recognised and
// not implemented, sorted by namespace.
func DeclaredNotImplemented() []NotImplemented {
	out := make([]NotImplemented, len(notImplemented))
	copy(out, notImplemented)
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}

// ErrNotImplemented reports a credential type this platform recognises and
// has not implemented. It mirrors credtype.ErrLookupNotImplemented, and for
// the same reason: recognised and unbuilt is a different fact from unknown,
// and an operator acts on the two differently.
var ErrNotImplemented = fmt.Errorf("credtype/managed: this credential type is declared but not implemented")

// CheckNamespace reports whether a namespace names a type this platform
// ships, a type it declares and has not implemented, or neither.
//
// It returns nil for a namespace it has never heard of, which looks
// backwards and is deliberate: an unknown namespace is a CUSTOM credential
// type, which is the case this platform supports best. Only a namespace AWX
// manages and this platform does not implement is an error.
func CheckNamespace(namespace string) error {
	if _, ok := shipped[namespace]; ok {
		return nil
	}
	for _, ni := range notImplemented {
		if ni.Namespace == namespace {
			return fmt.Errorf("%w: %s (%s), because %s: %s",
				ErrNotImplemented, ni.Namespace, ni.Name, ni.Reason, ni.Detail)
		}
	}
	return nil
}

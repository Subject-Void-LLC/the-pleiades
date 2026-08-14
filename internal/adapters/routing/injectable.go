package routing

import (
	"errors"
	"fmt"
	"sort"
)

// Which injector targets an execution path can honour.
//
// # Why this rule lives here rather than in the adapter that enforces it
//
// It is a routing rule. It answers "can this adapter run this", which is
// the question this package exists for, and it has two callers on opposite
// sides of the system: internal/api refuses a binding at the moment an
// operator makes it, and internal/adapters/native refuses a payload at the
// moment one arrives.
//
// Putting it in the native adapter would mean internal/api importing that
// adapter, which pulls internal/transport/ssh, internal/engine and
// internal/lock into the Controller binary so that an HTTP handler can
// compare a string. Putting it here costs the Controller nothing:
// internal/api already depends on internal/launch and pkg/wire, which is
// this package's entire dependency set.
//
// # What the rule is
//
// PLAN.md Section 29.4 accepts the ephemeral container as the trust
// boundary that permits a secret in an environment variable, and scopes
// that acceptance to the legacy Ansible adapter. The native Go mesh keeps
// Section 17.5's stricter rule, so env and file injectors have no meaning
// there.
//
// No meaning is not the same as no harm. Quietly dropping them is
// FAILURE_PATTERNS.md #116's shape, "correctly computed and never read by
// anything downstream", which internal/adapters/native already refuses to
// repeat for forks and limit. An operator who bound an aws credential to a
// native template and watched the run fail to authenticate would have
// nothing in the record pointing at the reason. So the platform refuses,
// twice.

// AdapterNative is the adapter a launch kind names when it runs on the
// native Go execution path.
//
// Restated here rather than imported from internal/launch/kinds/runbook,
// which is where the kind declares it, because importing a kind package
// would make this routing table depend on the specific kinds a deployment
// happens to register, and this package's whole design is that adding a
// kind changes its input rather than its code.
// TestAdapterNamesMatchTheirKinds holds the two together.
const AdapterNative = "native"

// AdapterLegacy is the adapter a launch kind names when it runs an
// unconverted Ansible playbook in an ephemeral container.
const AdapterLegacy = "legacy"

// ErrUnsupportedInjection reports injected credential material an execution
// path cannot honour.
var ErrUnsupportedInjection = errors.New("routing: this execution path cannot inject that")

// UnsupportedInjectionMessage explains the refusal, in one sentence shared
// by the bind-time check and the run-time backstop.
//
// One constant rather than two similar sentences, so an operator who hits
// the second after somehow getting past the first does not have to work out
// whether they are looking at the same problem.
const UnsupportedInjectionMessage = "the native execution path keeps the stricter rule that a secret never enters a process environment or a file on the runner's own disk, so a credential type using env or file injectors can only run on the legacy Ansible path"

// CheckInjectable reports whether a credential type's injector targets can
// be honoured by the named adapter.
//
// It takes the two target key sets as plain strings rather than a
// credtype.Injectors, so this package does not import internal/credtype for
// one field read. A routing table has no other business knowing what a
// credential type is, and the callers already hold the document.
//
// The message names the offending keys, sorted, because "this credential
// cannot be used here" without saying which part of it is the difference
// between an operator editing one injector line and an operator giving up.
func CheckInjectable(adapter, typeName string, envNames, fileLabels []string) error {
	if adapter != AdapterNative {
		return nil
	}
	if len(envNames) == 0 && len(fileLabels) == 0 {
		return nil
	}

	offending := describeTargets(envNames, fileLabels)
	return fmt.Errorf("%w: the credential type %q injects %v, and %s",
		ErrUnsupportedInjection, typeName, offending, UnsupportedInjectionMessage)
}

// describeTargets names injector targets for a refusal message.
func describeTargets(envNames, fileLabels []string) []string {
	out := make([]string, 0, len(envNames)+len(fileLabels))
	for _, name := range envNames {
		out = append(out, "env "+name)
	}
	for _, label := range fileLabels {
		if label == "" {
			// The single-file spelling, whose label is legitimately empty.
			// Printing "file " with nothing after it would read as a bug.
			out = append(out, "file template")
			continue
		}
		out = append(out, "file template."+label)
	}
	sort.Strings(out)
	return out
}

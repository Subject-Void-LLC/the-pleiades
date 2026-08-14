package native

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The native path's answer to injected credential material: extra variables
// are consumed, environment variables and generated files are REFUSED.
//
// # Why a refusal rather than a silent skip
//
// PLAN.md Section 29.4 accepts the ephemeral container as the trust
// boundary that permits a secret in an environment, and scopes that
// acceptance to the legacy Ansible adapter. The native Go mesh keeps
// Section 17.5's stricter rule, so an env injector is meaningless here.
//
// Meaningless is not the same as harmless. Quietly dropping it is exactly
// FAILURE_PATTERNS.md #116's shape, "correctly computed and never read by
// anything downstream", which adapter.go already refuses to repeat for
// forks and limit two functions away. An operator who bound an aws
// credential to a native template and watched the run fail to authenticate
// would have nothing in the record pointing at the reason. So the platform
// says so, twice: once at bind time, where the fix is cheap, and once here,
// as a backstop for a payload that arrived by some other route.
//
// # Why file is refused for a stronger reason than env
//
// There is no ephemeral filesystem on this path. The Runner is a long-lived
// process on a real host, and both Section 17 and this platform's own
// container-orchestrator contract say the Runner never writes a secret to
// its own disk. Materialising a credential file here would mean inventing a
// new secret-at-rest surface in order to satisfy a checklist, which is
// worse than not having the feature.
//
// The follow-up is named rather than built: sdk.RunbookContext.InjectFiles,
// materialising content inside the per-task CHILD subprocess over the
// existing stdin plus fd-3 channel, where it would live in that
// subprocess's memory for the length of one task and never touch a
// filesystem at all.

// The rule itself, its error and its message live in
// internal/adapters/routing, alongside the adapter names, because the same
// rule has a second caller on the far side of the system: internal/api
// refuses a binding when an operator makes it. See that package's
// injectable.go for why it lives there rather than here.

// refuseUnsupportedInjection is the run-time backstop.
//
// It fires on a payload carrying env or file material regardless of how it
// got here: a bind that predates the bind-time check, a Controller running
// an older build, or a direct publish onto the bus. It fails the dispatch
// loudly rather than running it with part of its credentials missing,
// because a run that authenticated against nothing and reported success is
// the outcome this whole file exists to prevent.
func refuseUnsupportedInjection(injected *wire.Injected) error {
	if injected == nil {
		return nil
	}

	var offending []string
	for name := range injected.Env {
		offending = append(offending, "env "+name)
	}
	for _, f := range injected.Files {
		offending = append(offending, "file "+f.Path)
	}
	for _, v := range injected.Vault {
		// A vault password is a file by construction, and Ansible Vault is
		// an Ansible feature with no native equivalent at all, so it is
		// refused for both reasons at once.
		offending = append(offending, "vault "+v.Path)
	}
	if len(offending) == 0 {
		return nil
	}
	sort.Strings(offending)

	return fmt.Errorf("%w: this dispatch carries %v, and %s",
		routing.ErrUnsupportedInjection, offending, routing.UnsupportedInjectionMessage)
}

// injectedVariables merges a dispatch's injected extra variables over its
// launch-time ones, refusing a collision rather than picking a winner.
//
// It mirrors the legacy adapter's own mergeExtraVars exactly, including the
// refusal, so the two paths cannot disagree about what a run's variables
// are. They are separate functions rather than one shared helper because
// the two packages share no dependency by design (an execution adapter
// imports the other's package for nothing), and the alternative would be a
// third package holding four lines.
func injectedVariables(launched map[string]any, injected *wire.Injected) (map[string]any, error) {
	if injected == nil || len(injected.ExtraVars) == 0 {
		return launched, nil
	}

	out := make(map[string]any, len(launched)+len(injected.ExtraVars))
	for name, value := range launched {
		out[name] = value
	}

	names := make([]string, 0, len(injected.ExtraVars))
	for name := range injected.ExtraVars {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, clash := out[name]; clash {
			return nil, fmt.Errorf(
				"a bound credential injects the extra variable %q, which this launch also set", name)
		}
		out[name] = injected.ExtraVars[name]
	}
	return out, nil
}

// injectedSecretValues returns the values this injection says are secret,
// so the run's own output masking scrubs them.
//
// It returns exactly what Injected.Mask names rather than every string leaf
// of the injected variables. Masking everything would look safer and be
// worse: a non-secret injected value (a region, an endpoint URL) would be
// scrubbed out of this run's own error text, so an operator debugging a
// failure would read "connection to ******** refused".
func injectedSecretValues(injected *wire.Injected) []string {
	if injected == nil {
		return nil
	}
	return injected.Mask
}

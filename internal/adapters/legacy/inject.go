package legacy

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// This file owns turning a dispatch's injected credential material into the
// parts of a ContainerSpec that carry it: the secret environment, the
// generated files, and the extra-variables file.
//
// It is split out of adapter.go for the same file-per-concern reason argv.go
// and inventory.go already are, and because keeping every decision about
// where a secret may go in one readable file is worth more here than
// anywhere else in this package.

// extraVarsContainerPath is where the extra-variables file is written
// inside the container, and the path buildArgv's `-e @<path>` names.
//
// A named constant rather than two hand-typed literals, for the reason
// inventoryContainerPath and playbookContainerPath are: the writer and the
// reader of this path cannot be allowed to drift.
const extraVarsContainerPath = "/run/pleiades/extravars.json"

// protectedEnvNames are variables this adapter sets for its own correctness
// and an injector may not replace.
//
// The two colour variables are here because this package's own stdout
// parser depends on their values: an injector that turned colour back on
// would not break Ansible, it would break this platform's ability to read
// what Ansible said, and every task outcome would be reported wrong.
//
// ANSIBLE_HOST_KEY_CHECKING is deliberately NOT protected. An operator who
// wants host key checking back on is exactly the deployment this adapter
// cannot serve today (see adapter.go's own comment on why it is disabled),
// so an injector that can set it is a way forward rather than a hazard.
var protectedEnvNames = map[string]string{
	"ANSIBLE_FORCE_COLOR": "this adapter's own stdout parser depends on its value",
	"ANSIBLE_NOCOLOR":     "as ANSIBLE_FORCE_COLOR",
}

// mergeEnv combines a container's non-secret and secret environments.
//
// Called by the orchestrator immediately before the container starts, which
// is the whole design: the two halves stay separate everywhere else so that
// anything printing a ContainerSpec prints only the safe one.
//
// A collision cannot happen here, because injectedEnv below already refused
// every protected name and the adapter's own three variables are the only
// entries in base. Should one ever arise, base wins: this adapter's
// correctness beats an injector document's preference.
func mergeEnv(base, secret map[string]string) map[string]string {
	if len(secret) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(secret))
	for k, v := range secret {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// registerInjectedSecrets records the values this dispatch says are secret
// with the process-wide masking set, so a later log line or a captured
// module result echoing one back is scrubbed.
//
// It registers exactly what Injected.Mask names and nothing else. Masking
// every injected value instead would look safer and be worse: an ordinary
// URL, region or username would be scrubbed out of every later line in this
// Runner for the rest of its life, corrupting output without protecting
// anything. The Controller knows which values came from inputs marked
// secret, and the wire is where it says so.
func registerInjectedSecrets(injected *wire.Injected) {
	if injected == nil || len(injected.Mask) == 0 {
		return
	}
	redact.Shared().Literals().Add(injected.Mask...)
}

// injectedEnv returns the environment an injector contributed, refusing any
// variable this adapter needs to control.
func injectedEnv(injected *wire.Injected) (map[string]string, error) {
	if injected == nil || len(injected.Env) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(injected.Env))
	for _, name := range sortedKeys(injected.Env) {
		if why, protected := protectedEnvNames[name]; protected {
			return nil, fmt.Errorf(
				"a bound credential injects %s, which this adapter sets itself because %s", name, why)
		}
		out[name] = injected.Env[name]
	}
	return out, nil
}

// injectedFiles turns generated credential files into container files.
//
// Mode comes from the payload rather than being re-decided here, and the
// injector sets it to 0o600 for every file it generates. A defensive
// override would hide a Controller that had started sending something else,
// which is worth knowing about rather than silently correcting.
func injectedFiles(injected *wire.Injected) ([]ContainerFile, error) {
	if injected == nil || len(injected.Files) == 0 {
		return nil, nil
	}

	out := make([]ContainerFile, 0, len(injected.Files))
	for _, f := range injected.Files {
		if f.Path == "" {
			return nil, fmt.Errorf("a bound credential generated a file with no path")
		}
		if f.Mode == 0 {
			// A zero mode would create an unreadable file, and the run
			// would fail reading its own credential rather than here.
			return nil, fmt.Errorf("a bound credential generated %s with no file mode", f.Path)
		}
		out = append(out, ContainerFile{
			Content:       []byte(f.Content),
			ContainerPath: f.Path,
			Mode:          f.Mode,
		})
	}
	return out, nil
}

// mergeExtraVars folds a credential's injected extra variables over the
// launch's own, refusing a collision rather than picking a winner.
//
// The refusal direction is worth stating: it refuses rather than letting
// the credential win or the launch win, because either silent resolution
// means a run using a value the operator did not choose and cannot see.
// credtype.Combine already refuses a collision BETWEEN credentials; this is
// the collision between a credential and the launch, which that check
// cannot see because it never holds the launch's variables.
func mergeExtraVars(launched map[string]any, injected *wire.Injected) (map[string]any, error) {
	if injected == nil || len(injected.ExtraVars) == 0 {
		return launched, nil
	}

	out := make(map[string]any, len(launched)+len(injected.ExtraVars))
	for name, value := range launched {
		out[name] = value
	}
	for _, name := range sortedKeys(injected.ExtraVars) {
		if _, clash := out[name]; clash {
			return nil, fmt.Errorf(
				"a bound credential injects the extra variable %q, which this launch also set", name)
		}
		out[name] = injected.ExtraVars[name]
	}
	return out, nil
}

// vaultsOf returns the vault identities a dispatch carries, or nothing.
//
// A tiny helper rather than an inline nil check at the one call site,
// because buildArgv takes the slice and a nil-pointer dereference on a
// credential-less dispatch would be a crash on the most common path in the
// system.
func vaultsOf(injected *wire.Injected) []wire.InjectedVault {
	if injected == nil {
		return nil
	}
	return injected.Vault
}

// sortedKeys returns a map's keys in sorted order, so a refusal names the
// same variable on every run rather than whichever one Go's map iteration
// reached first.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

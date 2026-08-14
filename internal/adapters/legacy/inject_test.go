package legacy_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The injection tests, all driven through Adapter.Execute rather than
// through the unexported helpers, because what matters is the
// ContainerSpec a real orchestrator would be handed. A helper tested in
// isolation can be correct and still never be called.

// injectingPayload is a dispatch carrying every kind of injected material
// at once, which is the shape a template bound to a cloud credential and a
// vault credential produces.
func injectingPayload() wire.DispatchPayload {
	return wire.DispatchPayload{
		JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
		Injected: &wire.Injected{
			Env:       map[string]string{"REST_API_TOKEN": "a-real-bearer-token"},
			ExtraVars: map[string]any{"ansible_api_url": "https://api.example.test"},
			Files: []wire.InjectedFile{
				{Label: "cert", Path: "/run/pleiades/credentials/18.cert", Content: "cert-body", Mode: 0o600},
			},
			Vault: []wire.InjectedVault{
				{Identifier: "prod", Path: "/run/pleiades/credentials/9.vault"},
			},
		},
	}
}

// runInjecting executes one dispatch against a fake orchestrator and
// returns the spec it was handed.
func runInjecting(t *testing.T, payload wire.DispatchPayload) (legacy.ContainerSpec, error) {
	t.Helper()

	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{result: legacy.ContainerResult{Output: []byte(realPlaybookOutput), ExitCode: 0}}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	err := adapter.Execute(context.Background(), payload)
	return orch.lastSpec, err
}

// fileAt returns the container file at path, or fails.
func fileAt(t *testing.T, spec legacy.ContainerSpec, path string) legacy.ContainerFile {
	t.Helper()
	for _, f := range spec.Files {
		if f.ContainerPath == path {
			return f
		}
	}
	t.Fatalf("no container file at %s; the spec has %d files", path, len(spec.Files))
	return legacy.ContainerFile{}
}

// TestInjectedMaterialReachesTheContainer covers every target at once, and
// the assertion that carries the design is the FIRST one: the injected
// secret is in SecretEnv, not Env.
//
// The two are separate fields precisely so that anything in this package
// which prints a spec prints the safe half. If they were one map, that
// property would be a rule somebody has to remember rather than a type.
func TestInjectedMaterialReachesTheContainer(t *testing.T) {
	spec, err := runInjecting(t, injectingPayload())
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	if spec.SecretEnv["REST_API_TOKEN"] != "a-real-bearer-token" {
		t.Errorf("SecretEnv = %v, want the injected token", spec.SecretEnv)
	}
	if _, leaked := spec.Env["REST_API_TOKEN"]; leaked {
		t.Errorf("the injected token is in Env, which this package treats as safe to print: %v", spec.Env)
	}

	// The adapter's own three variables are untouched and still in Env.
	for _, name := range []string{"ANSIBLE_FORCE_COLOR", "ANSIBLE_NOCOLOR", "ANSIBLE_HOST_KEY_CHECKING"} {
		if _, present := spec.Env[name]; !present {
			t.Errorf("Env lost %s", name)
		}
	}

	cert := fileAt(t, spec, "/run/pleiades/credentials/18.cert")
	if string(cert.Content) != "cert-body" || cert.Mode != 0o600 {
		t.Errorf("the injected file = %+v, want the rendered body at mode 0600", cert)
	}

	extraVars := fileAt(t, spec, "/run/pleiades/extravars.json")
	if !strings.Contains(string(extraVars.Content), `"ansible_api_url":"https://api.example.test"`) {
		t.Errorf("the extra-vars file = %s, want the injected variable", extraVars.Content)
	}

	if !containsPair(spec.Argv, "--vault-id", "prod@/run/pleiades/credentials/9.vault") {
		t.Errorf("Argv = %#v, want the vault identity", spec.Argv)
	}
}

// TestNoInjectedValueEverReachesArgv is the argv leak, stated as the
// property rather than as the shape of the fix.
//
// Before Phase 22 the extra variables were marshalled onto argv, so `ps
// auxww` inside the container and /proc/<ppid>/cmdline showed every one of
// them to every other module in the run. This asserts against the spec the
// adapter built, before it runs, so the check does not depend on a
// container existing.
func TestNoInjectedValueEverReachesArgv(t *testing.T) {
	const secret = "a-real-bearer-token"

	payload := injectingPayload()
	// A secret in a plain launch-time extra variable too, because the
	// adapter cannot know which extra variables are secret and the fix is
	// unconditional for exactly that reason.
	payload.ExtraVars = map[string]any{"launch_token": secret}

	spec, err := runInjecting(t, payload)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	for i, arg := range spec.Argv {
		if strings.Contains(arg, secret) {
			t.Fatalf("Argv[%d] = %q carries a secret value", i, arg)
		}
		if strings.Contains(arg, "cert-body") {
			t.Fatalf("Argv[%d] = %q carries injected file content", i, arg)
		}
	}

	// And the values really did reach the run, which is what stops this
	// test from passing for a build that simply dropped them.
	extraVars := string(fileAt(t, spec, "/run/pleiades/extravars.json").Content)
	if !strings.Contains(extraVars, secret) {
		t.Errorf("the extra-vars file lost the launch's own variable: %s", extraVars)
	}
}

// TestAnInjectorCannotReplaceTheAdaptersOwnVariables covers the refusal
// that protects this package's stdout parser.
//
// An injector that turned colour back on would not break Ansible. It would
// break this platform's ability to READ what Ansible said, so every task
// outcome in the run would be reported wrong, which is worse than a failure.
func TestAnInjectorCannotReplaceTheAdaptersOwnVariables(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "ANSIBLE_FORCE_COLOR is refused",
			env:     map[string]string{"ANSIBLE_FORCE_COLOR": "true"},
			wantErr: true,
		},
		{
			name:    "ANSIBLE_NOCOLOR is refused",
			env:     map[string]string{"ANSIBLE_NOCOLOR": "0"},
			wantErr: true,
		},
		{
			name: "ANSIBLE_HOST_KEY_CHECKING is deliberately allowed",
			// An operator wanting host key checking back on is exactly the
			// deployment this adapter cannot serve today, so an injector
			// that can set it is a way forward rather than a hazard.
			env:     map[string]string{"ANSIBLE_HOST_KEY_CHECKING": "true"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := wire.DispatchPayload{
				JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
				Injected: &wire.Injected{Env: tt.env},
			}
			_, err := runInjecting(t, payload)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Execute honoured an injector overriding a variable this adapter controls")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute refused a variable an injector is allowed to set: %v", err)
			}
		})
	}
}

// TestAnInjectedExtraVariableCollidingWithTheLaunchIsRefused covers the
// collision credtype.Combine structurally cannot see, because it never
// holds the launch's own variables.
func TestAnInjectedExtraVariableCollidingWithTheLaunchIsRefused(t *testing.T) {
	payload := wire.DispatchPayload{
		JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
		ExtraVars: map[string]any{"deploy_env": "prod"},
		Injected:  &wire.Injected{ExtraVars: map[string]any{"deploy_env": "from-the-credential"}},
	}

	_, err := runInjecting(t, payload)
	if err == nil {
		t.Fatal("Execute silently picked a winner between a launch variable and an injected one")
	}
	if !strings.Contains(err.Error(), "deploy_env") {
		t.Errorf("the refusal does not name the colliding variable: %v", err)
	}
}

// TestAMalformedInjectionIsRefusedBeforeAContainerStarts covers the two
// shapes that would produce a run failing on its own credentials rather
// than here.
func TestAMalformedInjectionIsRefusedBeforeAContainerStarts(t *testing.T) {
	tests := []struct {
		name string
		file wire.InjectedFile
	}{
		{
			name: "a file with no path",
			file: wire.InjectedFile{Content: "body", Mode: 0o600},
		},
		{
			name: "a file with no mode, which would be created unreadable",
			file: wire.InjectedFile{Path: "/run/pleiades/credentials/18", Content: "body"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := wire.DispatchPayload{
				JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
				Injected: &wire.Injected{Files: []wire.InjectedFile{tt.file}},
			}
			if _, err := runInjecting(t, payload); err == nil {
				t.Fatal("Execute accepted a malformed injected file")
			}
		})
	}
}

// TestADispatchWithNoInjectionIsUnchanged is the regression guard for every
// dispatch that existed before this phase, asserted on the spec rather than
// on the absence of an error.
func TestADispatchWithNoInjectionIsUnchanged(t *testing.T) {
	spec, err := runInjecting(t, wire.DispatchPayload{
		JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	if len(spec.SecretEnv) != 0 {
		t.Errorf("SecretEnv = %v, want none", spec.SecretEnv)
	}
	// Inventory and playbook only: no extra-vars file, no credential files.
	if len(spec.Files) != 2 {
		t.Errorf("the spec has %d files, want only the inventory and the playbook", len(spec.Files))
	}
	for _, arg := range spec.Argv {
		if arg == "-e" || strings.HasPrefix(arg, "--vault-id") {
			t.Errorf("Argv = %#v, want no extra-vars or vault flags", spec.Argv)
		}
	}
}

// containsPair reports whether argv contains flag immediately followed by
// value.
func containsPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

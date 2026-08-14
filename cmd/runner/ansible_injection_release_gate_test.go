package main_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// Phase 22's own Release Gate for credential injection: a real credential
// type, really rendered, really injected into a real ephemeral container
// running a real ansible-playbook, and really read back by the playbook.
//
// # Why this is TWO runs rather than one
//
// One run cannot both print a value and prove it was not printed. So the
// gate splits, deliberately, and the split is the design rather than a
// convenience:
//
//   - Run 1 proves ARRIVAL, byte for byte, using non-secret control values
//     the playbook is free to echo. What it establishes is that the
//     environment a customer's playbook sees here is the environment it saw
//     in AWX.
//   - Run 2 proves ABSENCE, using real secret values, in four places at
//     once: the container's argv as the adapter built it, /proc/1/cmdline
//     read from INSIDE the container, every job event published, and every
//     byte the masked logger wrote.
//
// The second run's /proc check is the one that could not be made any other
// way. Accepting the ephemeral container as the trust boundary (PLAN.md
// Section 29.4) permits a secret in that container's environment; it does
// not permit every task in the run to read every other task's credentials
// out of the process table, which is what the old `-e <json>` argv form
// allowed.
//
// It lives in cmd/runner deliberately: this package's TestMain re-exec
// precedent and flaky-packages.json's existing cmd/runner entry both apply
// with no new edits anywhere.

// The control values run 1 uses. Non-secret by construction, so the
// playbook may print them and the reference comparison can be exact.
const (
	controlToken = "control-token-not-a-secret"
	controlURL   = "https://api.example.test"
)

// The real values run 2 uses. Distinctive enough that finding one anywhere
// is a genuine result rather than a coincidence.
const (
	injectionGateSecret = "sk-live-CANARY-injection-9f8e7d6c5b4a"
	injectionGateVault  = "vault-CANARY-password-3c2b1a09"
)

// injectionGateCredentialID is the credential id the generated file path
// and therefore {{ tower.filename }} are derived from. Fixed, so the
// expected path in testdata is stable.
const injectionGateCredentialID = 18

// injectionGateType is the credential type this gate injects. It exercises
// all three data targets at once, including the reserved filename
// namespace, which is the target most likely to be quietly wrong.
//
// secret decides whether api_token is declared secret. Both runs use the
// identical INJECTORS, because that is what byte identity is about; they
// differ only in that flag, and the difference is what makes two runs
// necessary.
//
// The arrival run declares it NOT secret, and this is a real finding rather
// than a convenience. The first version of this gate used a harmless
// control value and still declared the input secret, and the run reported
// "REST_API_TOKEN": "********": the platform had masked it on the way out,
// correctly, because the masking rule keys on whether the INPUT is declared
// secret and not on whether the value happens to be sensitive. A gate
// asserting exact bytes cannot also ask the platform to hide them. So
// arrival uses a non-secret input and the leak run below uses a secret one,
// which is the same split for the same reason the two runs exist at all.
func injectionGateType(secret bool) credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Custom REST API Token",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api_token",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Type: credtype.InputString, Label: "API Bearer Token", Secret: secret},
				{ID: "api_url", Type: credtype.InputString, Label: "API Base URL"},
			},
			Required: []string{"api_token", "api_url"},
		},
		Injectors: credtype.Injectors{
			Env: map[string]string{
				"REST_API_TOKEN":  "{{ api_token }}",
				"REST_API_URL":    "{{ api_url }}",
				"REST_API_CONFIG": "{{ tower.filename }}",
			},
			// A SECRET extra variable, deliberately. Extra variables are
			// what the old `-e <json>` argv form leaked, so putting a
			// secret here is what makes the leak run's /proc/1/cmdline
			// check load-bearing rather than incidental: without it, a
			// regression restoring the old form would put only the
			// non-secret url on argv and the check would pass.
			ExtraVars: map[string]any{
				"ansible_api_url":   "{{ api_url }}",
				"ansible_api_token": "{{ api_token }}",
			},
			File: map[string]string{"template": "token={{ api_token }}\nurl={{ api_url }}\n"},
		},
	}
}

// injectFor renders the gate's credential type against the given values,
// through the real injector and the real Artifact-to-wire conversion the
// Controller's fan-out uses.
//
// Through dispatch.InjectedFrom rather than a conversion of this test's
// own, deliberately: a gate that hand-built a wire.Injected would be
// proving the adapter against a shape nothing in production produces.
func injectFor(t *testing.T, token, url string, secret bool) *wire.Injected {
	t.Helper()

	injector, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatalf("NewInjector: %v", err)
	}

	art, err := injector.Inject([]credtype.Credential{{
		ID:     injectionGateCredentialID,
		Name:   "release gate api",
		Type:   injectionGateType(secret),
		Inputs: map[string]string{"api_token": token, "api_url": url},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	return dispatch.InjectedFrom(art)
}

// injectionArrivalPlaybook reports every injected value back, so run 1 can
// compare what the playbook saw against the reference.
//
// The environment is read with lookup('env', ...), which evaluates on the
// controller, meaning inside the ephemeral runner container: exactly where
// an Ansible module reads it from, and exactly what Section 29.4's trust
// boundary is about.
const injectionArrivalPlaybook = `---
- hosts: all
  gather_facts: false
  tasks:
    - name: report the injected environment
      debug:
        msg: "{{ {'REST_API_TOKEN': lookup('env', 'REST_API_TOKEN'), 'REST_API_URL': lookup('env', 'REST_API_URL'), 'REST_API_CONFIG': lookup('env', 'REST_API_CONFIG')} | to_json }}"

    - name: report the injected extra variable
      debug:
        msg: "extra_var={{ ansible_api_url }} extra_token={{ ansible_api_token }}"

    - name: report the generated credential file
      debug:
        msg: "file={{ lookup('file', lookup('env', 'REST_API_CONFIG')) | replace('\n', '|') }}"
`

// injectionLeakPlaybook reads the runner's own command line from inside
// the container, which is the only way to prove argv is clean within the
// trust boundary.
//
// PID 1 is ansible-playbook itself: the container's Cmd is the argv this
// adapter built, so /proc/1/cmdline is that argv exactly as any module
// shelling out during the run would see it.
const injectionLeakPlaybook = `---
- hosts: all
  gather_facts: false
  tasks:
    - name: read the runner's own command line
      shell: "tr '\\0' ' ' < /proc/1/cmdline"
      delegate_to: localhost
      changed_when: false
      register: own_cmdline

    - name: report the runner's own command line
      debug:
        msg: "cmdline={{ own_cmdline.stdout }}"

    - name: prove the credential really did arrive
      debug:
        msg: "token_length={{ lookup('env', 'REST_API_TOKEN') | length }}"
`

// TestInjectionReleaseGate_ArrivalIsByteIdentical is run 1.
//
// It uses control values, not secrets, so the playbook may print
// everything and the comparison can be exact. See testdata/README.md for
// what byte-identical means here and, just as importantly, what it does
// not.
func TestInjectionReleaseGate_ArrivalIsByteIdentical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}

	h := newAnsibleReleaseGateHarness(t, injectionArrivalPlaybook)

	payload := wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "upgrade.yml",
		DeviceID:     "release-gate-device",
		DeviceName:   "sw1",
		DeviceHost:   ansibleGateNetworkAlias,
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameCiscoIOS},
		Secrets:      gateMachineSecrets(),
		Injected:     injectFor(t, controlToken, controlURL, false),
	}

	events := h.dispatch(t, payload)
	if final := events[len(events)-1]; final.Status != "ok" {
		t.Fatalf("final status = %q, want ok: events=%+v", final.Status, events)
	}

	// The environment the playbook actually observed, decoded from its own
	// reported JSON rather than from anything this process computed.
	observed := decodeReportedEnv(t, events)

	reference := map[string]string{}
	raw, err := os.ReadFile(filepath.Join("testdata", "awx_reference_env.json"))
	if err != nil {
		t.Fatalf("failed to read the reference environment: %v", err)
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatalf("failed to decode the reference environment: %v", err)
	}

	if len(observed) != len(reference) {
		t.Fatalf("the run observed %d injected variables and the reference declares %d:\n got %v\nwant %v",
			len(observed), len(reference), sortedPairs(observed), sortedPairs(reference))
	}
	for name, want := range reference {
		if observed[name] != want {
			t.Errorf("%s = %q, want %q", name, observed[name], want)
		}
	}

	// The other two targets, which the reference file does not cover
	// because AWX writes its generated files somewhere else entirely (see
	// testdata/README.md).
	assertReported(t, events, "report the injected extra variable",
		"extra_var="+controlURL+" extra_token="+controlToken)
	// No trailing separator: Ansible's own lookup('file') strips trailing
	// newlines, so the two-line file comes back as one joined pair. That is
	// the lookup's behaviour rather than this platform's, and the file
	// itself really does end with a newline, which the leak run's own
	// container check does not depend on either way.
	assertReported(t, events, "report the generated credential file",
		"file=token="+controlToken+"|url="+controlURL)
}

// TestInjectionReleaseGate_NoSecretLeaves is run 2: the same type, with
// real secret values, asserting absence in four places.
func TestInjectionReleaseGate_NoSecretLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}

	// A masked logger writing into a buffer this test owns, built exactly
	// the way every composition root builds one. What it proves is not that
	// the adapter logs carefully but that the ruleset is installed: an
	// unmasked handler would fail this.
	var logged bytes.Buffer
	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker: %v", err)
	}
	masker.Literals().Add(injectionGateSecret, injectionGateVault)
	logger := slog.New(slog.NewJSONHandler(&logged, masker.HandlerOptions(slog.LevelDebug)))

	h := newAnsibleReleaseGateHarnessWithLogger(t, injectionLeakPlaybook, logger)

	payload := wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "upgrade.yml",
		DeviceID:     "release-gate-device",
		DeviceName:   "sw1",
		DeviceHost:   ansibleGateNetworkAlias,
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameCiscoIOS},
		Secrets:      gateMachineSecrets(),
		Injected:     injectFor(t, injectionGateSecret, controlURL, true),
	}

	events := h.dispatch(t, payload)
	if final := events[len(events)-1]; final.Status != "ok" {
		t.Fatalf("final status = %q, want ok: events=%+v", final.Status, events)
	}

	// 1. The argv the adapter built, checked on the spec a real Docker
	//    daemon was really handed, before the container ran.
	//
	//    Checking the SPEC rather than only the in-container view is
	//    load-bearing, and the negative control proved why: with the leak
	//    deliberately reintroduced, the /proc line reaching this test came
	//    back with the secret already replaced by the mask placeholder,
	//    because it travels back through the adapter's own output scrub. A
	//    gate that only read the command line from inside the container
	//    would therefore have reported a clean argv for a build that was
	//    leaking. The spec is the one view of argv that nothing has
	//    scrubbed.
	spec := h.orch.lastSpec()
	for i, arg := range spec.Argv {
		if strings.Contains(arg, injectionGateSecret) {
			t.Errorf("Argv[%d] carries the secret: %q", i, arg)
		}
	}
	// And the material really did reach the run, which is what stops this
	// from passing for a build that simply dropped it.
	if len(spec.SecretEnv) == 0 {
		t.Error("nothing was injected at all, so proving absence proves nothing")
	}

	// 2. /proc/1/cmdline as read from INSIDE the container. This is the
	//    assertion the trust boundary does not cover: a credential
	//    injected for one module must not be readable by an unrelated
	//    module in the same run, and the old `-e <json>` form made it so.
	cmdline := reportedValue(t, events, "report the runner's own command line")
	if cmdline == "" {
		t.Fatal("the playbook did not report the runner's own command line")
	}
	if strings.Contains(cmdline, injectionGateSecret) {
		t.Errorf("the container's own /proc/1/cmdline carries the secret: %q", cmdline)
	}
	// The extra-vars flag must really be there, as a file reference, or
	// the check above passes because nothing was passed at all.
	if !strings.Contains(cmdline, "@/run/pleiades/extravars.json") {
		t.Errorf("the command line does not reference the extra-vars file: %q", cmdline)
	}

	// 3. Every job event published over the real bus.
	for i, evt := range events {
		body, err := json.Marshal(evt)
		if err != nil {
			t.Fatalf("failed to encode job event %d: %v", i, err)
		}
		if bytes.Contains(body, []byte(injectionGateSecret)) {
			t.Errorf("job event %d carries the secret: %s", i, body)
		}
	}
	// The credential really did arrive, proved without printing it.
	assertReported(t, events, "prove the credential really did arrive",
		"token_length="+itoa(len(injectionGateSecret)))

	// 4. Every byte the masked logger wrote.
	if bytes.Contains(logged.Bytes(), []byte(injectionGateSecret)) {
		t.Errorf("the masked log carries the secret: %s", logged.String())
	}
}

// gateMachineSecrets is the SSH credential the target container accepts,
// which is unrelated to what this gate injects and is here only so the
// playbook can reach a host at all.
func gateMachineSecrets() map[string]string {
	return map[string]string{
		credtype.MachineUsername: ansibleGateSSHUser,
		credtype.MachinePassword: ansibleGateSSHPassword,
	}
}

// decodeReportedEnv pulls the environment JSON the arrival playbook
// reported back out of the parsed job events.
func decodeReportedEnv(t *testing.T, events []wire.JobEvent) map[string]string {
	t.Helper()

	raw := reportedValue(t, events, "report the injected environment")
	if raw == "" {
		t.Fatal("the playbook did not report its injected environment")
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode the reported environment %q: %v", raw, err)
	}
	return out
}

// reportedValue returns the message a named task reported.
func reportedValue(t *testing.T, events []wire.JobEvent, task string) string {
	t.Helper()

	for _, evt := range events {
		if evt.Task == task {
			return evt.EventData.Message
		}
	}
	return ""
}

// assertReported checks that a named task reported exactly want.
func assertReported(t *testing.T, events []wire.JobEvent, task, want string) {
	t.Helper()

	got := reportedValue(t, events, task)
	if got != want {
		t.Errorf("task %q reported %q, want %q", task, got, want)
	}
}

// sortedPairs renders a map for a failure message, in a stable order.
func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// itoa avoids importing strconv for one call in a message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

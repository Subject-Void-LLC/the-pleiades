package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// sshStub wraps inventorytest.Stub (the repo's shared InventoryItem test
// double) with the two accessors capability.SSHTransportCapable requires,
// which the base Stub does not implement since they are not part of the
// base InventoryItem contract.
type sshStub struct {
	*inventorytest.Stub
	host string
	port int
}

func (s *sshStub) SSHHost() string { return s.host }
func (s *sshStub) SSHPort() int    { return s.port }

// newSSHDevice returns an InventoryItem that declares capability.NameSSHTransport
// and implements capability.SSHTransportCapable, the shape SSHTarget and a
// binding's Capability check both expect.
func newSSHDevice(name, host string, port int) *sshStub {
	return &sshStub{
		Stub: &inventorytest.Stub{
			StubName: name,
			Caps:     []capability.Name{capability.NameSSHTransport},
		},
		host: host,
		port: port,
	}
}

// fakeTransport is a transport.Transport test double with no real
// network, letting these tests exercise transportActionExecutor's own
// dispatch, error handling, and masking logic in isolation from any real
// SSH connection (internal/transport/ssh has its own real-container tests
// for that).
type fakeTransport struct {
	exec func(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error)
}

func (f *fakeTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	return f.exec(ctx, target, cred, command)
}

// fakeCredentialStore is a credential.Store test double returning a fixed
// Credential or error regardless of the requested device name, unless
// perDevice is set.
type fakeCredentialStore struct {
	cred      credential.Credential
	err       error
	perDevice map[string]credential.Credential
}

func (f fakeCredentialStore) Lookup(_ context.Context, deviceName string) (credential.Credential, error) {
	if f.perDevice != nil {
		if c, ok := f.perDevice[deviceName]; ok {
			return c, nil
		}
	}
	return f.cred, f.err
}

// sshBinding returns a TransportBinding wired to a fakeTransport whose
// Exec calls exec, bound to capability.NameSSHTransport via SSHTarget,
// exactly the shape cmd/pleiades/run.go wires for "ssh_exec" in
// production, just with a fake Transport in place of a real ssh.New one.
func sshBinding(exec func(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error)) engine.TransportBinding {
	return engine.TransportBinding{
		Capability: capability.NameSSHTransport,
		Transport:  &fakeTransport{exec: exec},
		Target:     engine.SSHTarget,
	}
}

// TestSSHTarget confirms SSHTarget extracts host and port from a device
// implementing capability.SSHTransportCapable, and reports false for one
// that does not.
func TestSSHTarget(t *testing.T) {
	dev := newSSHDevice("router1", "10.0.0.1", 2222)
	target, ok := engine.SSHTarget(dev)
	if !ok {
		t.Fatal("expected ok for a device implementing SSHTransportCapable")
	}
	if target.Host != "10.0.0.1" || target.Port != 2222 {
		t.Errorf("expected Target{10.0.0.1, 2222}, got %+v", target)
	}

	plain := &inventorytest.Stub{StubName: "plain"}
	if _, ok := engine.SSHTarget(plain); ok {
		t.Error("expected ok=false for a device not implementing SSHTransportCapable")
	}
}

// TestTransportActionExecutor_DelegatesUnknownFQCNToFallback confirms
// "noop" (and any other fqcn not bound) keeps working completely
// unchanged through fallback, which is how Phase W5's own Release Gate
// stays green with this executor wired into the composition root instead
// of the bare builtin one.
func TestTransportActionExecutor_DelegatesUnknownFQCNToFallback(t *testing.T) {
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{},
		fakeCredentialStore{},
		nil,
		engine.NewBuiltinActionExecutor(),
	)

	task := &engine.Task{Name: "check", FQCN: "noop", Params: map[string]interface{}{"changed": true}}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected noop to succeed via fallback, got: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed true, per the authored params.changed")
	}
}

// TestTransportActionExecutor_RequiresCapableDevice confirms a bound fqcn
// rejects a nil device and a device that does not declare the required
// capability, as defense in depth behind validate.CapabilityRule's own
// pre-execution check.
func TestTransportActionExecutor_RequiresCapableDevice(t *testing.T) {
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(nil)},
		fakeCredentialStore{},
		nil,
		engine.NewBuiltinActionExecutor(),
	)

	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}

	if _, err := actions.Execute(context.Background(), task, nil); err == nil {
		t.Error("expected an error for a nil device")
	}

	incapable := &inventorytest.Stub{StubName: "no-ssh"}
	if _, err := actions.Execute(context.Background(), task, incapable); err == nil {
		t.Error("expected an error for a device that does not declare SSHTransportCapable")
	} else if !strings.Contains(err.Error(), string(capability.NameSSHTransport)) {
		t.Errorf("expected the error to name the missing capability, got: %v", err)
	}
}

// TestTransportActionExecutor_RejectsCapabilityWithNoTargetAccessor
// covers the defense-in-depth branch behind the capability check: a
// device can be made to declare a capability (inventorytest.Stub's
// HasCapability trusts its configured Caps, unlike a real concrete type's
// double structural check) without actually implementing that
// capability's accessor methods. This should not happen for any real
// device type, but transportActionExecutor must still fail explicitly
// rather than panic or silently proceed with a zero-value Target.
func TestTransportActionExecutor_RejectsCapabilityWithNoTargetAccessor(t *testing.T) {
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(nil)},
		fakeCredentialStore{},
		nil,
		engine.NewBuiltinActionExecutor(),
	)

	claimsCapabilityOnly := &inventorytest.Stub{
		StubName: "impostor",
		Caps:     []capability.Name{capability.NameSSHTransport},
	}
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}

	if _, err := actions.Execute(context.Background(), task, claimsCapabilityOnly); err == nil {
		t.Error("expected an error for a device that declares the capability but does not implement its Target accessor")
	}
}

// TestTransportActionExecutor_RequiresCommandParam confirms a bound fqcn
// with no params.command, or a non-string one, fails explicitly rather
// than dialing a device to run an empty or malformed command.
func TestTransportActionExecutor_RequiresCommandParam(t *testing.T) {
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(nil)},
		fakeCredentialStore{},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)

	for name, params := range map[string]map[string]interface{}{
		"missing":    nil,
		"empty":      {"command": ""},
		"wrong type": {"command": 5},
	} {
		t.Run(name, func(t *testing.T) {
			task := &engine.Task{FQCN: "ssh_exec", Params: params}
			if _, err := actions.Execute(context.Background(), task, dev); err == nil {
				t.Error("expected an error for a missing/invalid params.command")
			}
		})
	}
}

// TestTransportActionExecutor_CredentialLookupFailure confirms a
// credential.Store error is surfaced, not swallowed, and that Transport.Exec
// is never called when no credential could be resolved (proven via a
// fakeTransport that fails the test if invoked).
func TestTransportActionExecutor_CredentialLookupFailure(t *testing.T) {
	called := false
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			called = true
			return transport.Result{}, nil
		})},
		fakeCredentialStore{err: credential.ErrNotFound},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}

	_, err := actions.Execute(context.Background(), task, dev)
	if err == nil {
		t.Fatal("expected a credential lookup failure to be surfaced")
	}
	if !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("expected the error to wrap credential.ErrNotFound, got: %v", err)
	}
	if called {
		t.Error("expected Transport.Exec never to be called without a resolved credential")
	}
}

// TestTransportActionExecutor_MasksSecretsInStats confirms a command's
// stdout/stderr is masked before it reaches ActionResult.Stats, so a
// remote command that happens to echo the password or key it authenticated
// with never leaks it into a later task's when_cel-visible stat or a
// printed plan. This is the phase's masking-ruleset requirement, proven
// end to end through the executor's own seam, not just credential.Mask in
// isolation (internal/credential's own tests already cover that).
func TestTransportActionExecutor_MasksSecretsInStats(t *testing.T) {
	const secretPassword = "hunter2-super-secret"
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			return transport.Result{
				Stdout:   "connected with password " + secretPassword,
				Stderr:   "warning: password " + secretPassword + " expires soon",
				ExitCode: 0,
			}, nil
		})},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: secretPassword}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "whoami"}}

	result, err := actions.Execute(context.Background(), task, dev)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	stdout, _ := result.Stats["stdout"].(string)
	stderr, _ := result.Stats["stderr"].(string)
	if strings.Contains(stdout, secretPassword) {
		t.Errorf("expected stdout to be masked, got: %q", stdout)
	}
	if strings.Contains(stderr, secretPassword) {
		t.Errorf("expected stderr to be masked, got: %q", stderr)
	}
	if !strings.Contains(stdout, "********") {
		t.Errorf("expected a mask placeholder in stdout, got: %q", stdout)
	}
}

// TestTransportActionExecutor_ChangedDefaultsTrue confirms a successful
// ssh_exec defaults Changed to true (the opposite of noop's default,
// since a raw remote command is not provably idempotent), and that an
// explicit params.changed overrides it, mirroring noop's own convention.
func TestTransportActionExecutor_ChangedDefaultsTrue(t *testing.T) {
	makeExecutor := func() engine.ActionExecutor {
		return engine.NewTransportActionExecutor(
			map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
				return transport.Result{Stdout: "ok", ExitCode: 0}, nil
			})},
			fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
			nil,
			engine.NewBuiltinActionExecutor(),
		)
	}
	dev := newSSHDevice("router1", "10.0.0.1", 22)

	result, err := makeExecutor().Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, dev)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed to default to true for ssh_exec")
	}

	result, err = makeExecutor().Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime", "changed": false}}, dev)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if result.Changed {
		t.Error("expected an explicit params.changed=false to override the default")
	}
}

// TestTransportActionExecutor_NonZeroExitIsError confirms a non-zero
// remote exit code becomes a task failure (per transport.Transport's own
// contract: a non-zero ExitCode is not itself a Go error, but this
// executor is the layer that decides what a failed remote command means
// for the workflow, and Phase W6 decided: it fails the task), and that
// the resulting error text is still masked, never leaking a secret into a
// failure event or printed plan.
func TestTransportActionExecutor_NonZeroExitIsError(t *testing.T) {
	const secretPassword = "hunter2"
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			return transport.Result{Stderr: "auth used " + secretPassword + ": command not found", ExitCode: 127}, nil
		})},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: secretPassword}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "not-a-real-command"}}

	_, err := actions.Execute(context.Background(), task, dev)
	if err == nil {
		t.Fatal("expected a non-zero exit code to fail the task")
	}
	if strings.Contains(err.Error(), secretPassword) {
		t.Errorf("expected the failure error to be masked, got: %v", err)
	}
	if !strings.Contains(err.Error(), "127") {
		t.Errorf("expected the error to name the exit code, got: %v", err)
	}
}

// TestTransportActionExecutor_MasksSecretsInTransportError is a
// regression test for FAILURE_PATTERNS.md #22, found by Phase W6's own
// Schema/Injection Hardening audit: an earlier draft masked only the
// success path's stdout/stderr, so a Transport.Exec error that happened
// to embed credential material (no real golang.org/x/crypto/ssh error
// does today, but nothing enforced that as an invariant) would have
// reached cmd/pleiades/run.go's printed output and the event bus
// unmasked. This proves the error path is masked too, using a fake
// Transport whose error deliberately embeds the real password, exactly
// the shape the audit used to prove the gap.
func TestTransportActionExecutor_MasksSecretsInTransportError(t *testing.T) {
	const secretPassword = "hunter2-transport-error-secret"
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			return transport.Result{}, fmt.Errorf("dial failed: auth rejected for password %s", secretPassword)
		})},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: secretPassword}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}

	_, err := actions.Execute(context.Background(), task, dev)
	if err == nil {
		t.Fatal("expected the transport error to be surfaced as a failure")
	}
	if strings.Contains(err.Error(), secretPassword) {
		t.Errorf("expected the transport-level error to be masked, got: %v", err)
	}
	if !strings.Contains(err.Error(), "********") {
		t.Errorf("expected a mask placeholder in the error, got: %v", err)
	}
}

// TestTransportActionExecutor_TransportErrorIsSurfaced confirms a dial or
// protocol-level failure (transport.Transport's error return, distinct
// from a non-zero ExitCode) is wrapped and surfaced, never mistaken for a
// non-zero exit.
func TestTransportActionExecutor_TransportErrorIsSurfaced(t *testing.T) {
	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			return transport.Result{}, errors.New("dial tcp 10.0.0.1:22: connection refused")
		})},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
	dev := newSSHDevice("router1", "10.0.0.1", 22)
	task := &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}

	_, err := actions.Execute(context.Background(), task, dev)
	if err == nil {
		t.Fatal("expected a transport-level error to be surfaced")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected the underlying dial error to be wrapped, got: %v", err)
	}
}

// fakeProtocolCapable and its capability wiring below stand in for a
// second, unrelated real transport protocol (NETCONF, WinRM, ...): a
// fully independent capability name, a fully independent Transport
// implementation, and a fully independent Target accessor, registered
// under a second fqcn in the same bindings map as ssh_exec.
const fakeProtocolCapability capability.Name = "FakeProtocolCapable"

type fakeProtocolCapableDevice interface {
	FakeAddr() string
}

type fakeProtocolStub struct {
	*inventorytest.Stub
	addr string
}

func (f *fakeProtocolStub) FakeAddr() string { return f.addr }

func fakeProtocolTarget(item inventory.InventoryItem) (transport.Target, bool) {
	dev, ok := item.(fakeProtocolCapableDevice)
	if !ok {
		return transport.Target{}, false
	}
	return transport.Target{Host: dev.FakeAddr(), Port: 9999}, true
}

// TestTransportActionExecutor_DispatchesToASecondUnrelatedProtocol is
// this phase's Adversarial Pattern Justification, proven as a test rather
// than argued in prose: transportActionExecutor is exercised with TWO
// bindings in the same map, "ssh_exec" bound to capability.NameSSHTransport
// and a real-shaped fakeTransport, and "fake_exec" bound to a completely
// independent capability, Transport implementation, and Target accessor
// invented only for this test. Both dispatch correctly to their own
// Transport with zero change to transportActionExecutor's own code,
// proving the "Strategy keyed by capability" seam genuinely generalizes
// past SSH rather than secretly assuming it, as the checklist's "defend
// the transport seam against a second protocol" item requires.
func TestTransportActionExecutor_DispatchesToASecondUnrelatedProtocol(t *testing.T) {
	var sshCalled, fakeCalled bool

	bindings := map[string]engine.TransportBinding{
		"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			sshCalled = true
			return transport.Result{ExitCode: 0}, nil
		}),
		"fake_exec": {
			Capability: fakeProtocolCapability,
			Transport: &fakeTransport{exec: func(_ context.Context, target transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
				fakeCalled = true
				if target.Host != "fake-host" {
					t.Errorf("expected the fake protocol's own Target accessor to run, got host %q", target.Host)
				}
				return transport.Result{ExitCode: 0}, nil
			}},
			Target: fakeProtocolTarget,
		},
	}
	actions := engine.NewTransportActionExecutor(
		bindings,
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)

	sshDev := newSSHDevice("router1", "10.0.0.1", 22)
	fakeDev := &fakeProtocolStub{
		Stub: &inventorytest.Stub{StubName: "fake1", Caps: []capability.Name{fakeProtocolCapability}},
		addr: "fake-host",
	}

	if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, sshDev); err != nil {
		t.Fatalf("ssh_exec failed: %v", err)
	}
	if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "fake_exec", Params: map[string]interface{}{"command": "status"}}, fakeDev); err != nil {
		t.Fatalf("fake_exec failed: %v", err)
	}

	if !sshCalled {
		t.Error("expected ssh_exec to dispatch to the SSH-bound transport")
	}
	if !fakeCalled {
		t.Error("expected fake_exec to dispatch to the second, unrelated transport")
	}

	// Cross-wiring must be rejected too: a device that only declares the
	// fake protocol's capability must not satisfy ssh_exec's binding, and
	// vice versa.
	if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, fakeDev); err == nil {
		t.Error("expected ssh_exec to reject a device that only declares the fake protocol's capability")
	}
}

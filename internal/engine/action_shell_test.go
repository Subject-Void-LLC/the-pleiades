// Tests for shell selection on the transport actions (Phase 75): which
// port method a task reaches, what the device contributes, and every
// refusal, driven through the real transportActionExecutor.
package engine_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/windows"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// windowsStub is a device with WinRM and Windows shell accessors, the
// shape windows.Server has.
type windowsStub struct {
	*inventorytest.Stub
}

func (w *windowsStub) WinRMHost() string        { return "win1" }
func (w *windowsStub) WinRMPort() int           { return 5985 }
func (w *windowsStub) WorkingDirectory() string { return `C:\Work` }
func (w *windowsStub) CmdPath() string          { return `D:\cmd.exe` }
func (w *windowsStub) PowerShellPath() string   { return `D:\pwsh.exe` }

// newWindowsDevice returns a device declaring WinRM and Windows shell
// capabilities.
func newWindowsDevice() *windowsStub {
	return &windowsStub{Stub: &inventorytest.Stub{
		StubName: "win1",
		Caps:     []capability.Name{capability.NameWinRM, capability.NameWindowsShell},
	}}
}

// shellTransport records the ExecShell and Exec calls it receives.
type shellTransport struct {
	shellCalls []transport.ShellRequest
	execCalls  []string
}

func (s *shellTransport) Exec(_ context.Context, _ transport.Target, _ credential.Credential, command string) (transport.Result, error) {
	s.execCalls = append(s.execCalls, command)
	return transport.Result{}, nil
}

func (s *shellTransport) ExecShell(_ context.Context, _ transport.Target, _ credential.Credential, req transport.ShellRequest) (transport.Result, error) {
	s.shellCalls = append(s.shellCalls, req)
	return transport.Result{Stdout: "ran"}, nil
}

// winrmExecutor returns an executor whose winrm_exec binding uses tr.
func winrmExecutor(tr transport.Transport) engine.ActionExecutor {
	return engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"winrm_exec": {Capability: capability.NameWinRM, Transport: tr, Target: engine.WinRMTarget}},
		fakeCredentialStore{cred: credential.Credential{Username: "lab", Password: "pw"}},
		nil,
		engine.NewBuiltinActionExecutor(),
	)
}

func TestShellSelection_ReachesExecShellWithTheDevicesPaths(t *testing.T) {
	tests := []struct {
		shell           any
		wantShell       transport.Shell
		wantInterpreter string
	}{
		{shell: nil, wantShell: transport.ShellNone, wantInterpreter: ""},
		{shell: "none", wantShell: transport.ShellNone, wantInterpreter: ""},
		{shell: "cmd", wantShell: transport.ShellCmd, wantInterpreter: `D:\cmd.exe`},
		{shell: "PowerShell", wantShell: transport.ShellPowerShell, wantInterpreter: `D:\pwsh.exe`},
	}
	for _, tt := range tests {
		tr := &shellTransport{}
		params := map[string]any{"command": "dir", "env": map[string]any{"NAME": "value", "COUNT": 3}}
		if tt.shell != nil {
			params["shell"] = tt.shell
		}
		res, err := winrmExecutor(tr).Execute(context.Background(), &engine.Task{FQCN: "winrm_exec", Params: params}, newWindowsDevice())
		if err != nil {
			t.Fatalf("shell %v: %v", tt.shell, err)
		}
		if len(tr.shellCalls) != 1 || len(tr.execCalls) != 0 {
			t.Fatalf("shell %v: ExecShell %d, Exec %d; a ShellTransport is always reached through ExecShell", tt.shell, len(tr.shellCalls), len(tr.execCalls))
		}
		req := tr.shellCalls[0]
		if req.Shell != tt.wantShell || req.Interpreter != tt.wantInterpreter || req.WorkingDirectory != `C:\Work` || req.Script != "dir" {
			t.Errorf("shell %v: request = %+v", tt.shell, req)
		}
		if req.Env["PLEIADES_NAME"] != "value" || req.Env["PLEIADES_COUNT"] != "3" || len(req.Env) != 2 {
			t.Errorf("shell %v: env = %v, want every name prefixed with %s", tt.shell, req.Env, engine.EnvPrefix)
		}
		if res.Stats["stdout"] != "ran" {
			t.Errorf("shell %v: stats = %v", tt.shell, res.Stats)
		}
	}
}

func TestShellSelection_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		tr     transport.Transport
		params map[string]any
		want   string
	}{
		{name: "a shell on a transport with none", tr: &fakeTransport{}, params: map[string]any{"shell": "cmd"}, want: "no shell to choose"},
		{name: "env on a transport that cannot set it", tr: &fakeTransport{}, params: map[string]any{"env": map[string]any{"A": "1"}}, want: "cannot set"},
		{name: "a shell that is not a string", tr: &shellTransport{}, params: map[string]any{"shell": 3}, want: "must be a string"},
		{name: "an unknown shell", tr: &shellTransport{}, params: map[string]any{"shell": "bash"}, want: "valid values"},
		{name: "env that is not a map", tr: &shellTransport{}, params: map[string]any{"env": "A=1"}, want: "must be a map"},
		{name: "a nested env value", tr: &shellTransport{}, params: map[string]any{"env": map[string]any{"A": []any{1}}}, want: "must be a string, number or boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["command"] = "dir"
			_, err := winrmExecutor(tt.tr).Execute(context.Background(), &engine.Task{FQCN: "winrm_exec", Params: tt.params}, newWindowsDevice())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestShellSelection_PlainTransportUnchanged proves a transport that is
// not a ShellTransport is reached exactly as before Phase 75.
func TestShellSelection_PlainTransportUnchanged(t *testing.T) {
	var got string
	plain := &fakeTransport{exec: func(_ context.Context, _ transport.Target, _ credential.Credential, command string) (transport.Result, error) {
		got = command
		return transport.Result{}, nil
	}}
	if _, err := winrmExecutor(plain).Execute(context.Background(), &engine.Task{FQCN: "winrm_exec", Params: map[string]any{"command": "ipconfig", "shell": "none"}}, newWindowsDevice()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "ipconfig" {
		t.Errorf("Exec got %q", got)
	}
}

func TestWinRMTarget(t *testing.T) {
	target, ok := engine.WinRMTarget(newWindowsDevice())
	ep, epOK := target.Endpoint.(transport.NetworkEndpoint)
	if !ok || !epOK || ep.Host != "win1" || ep.Port != 5985 {
		t.Errorf("WinRMTarget = %+v, %v", target, ok)
	}
	if _, ok := engine.WinRMTarget(&inventorytest.Stub{StubName: "plain"}); ok {
		t.Error("WinRMTarget accepted a device with no WinRM accessors")
	}
}

// TestWinRMTarget_CarriesTheDevicesPin builds a real windows_server whose
// record pins an authority, as add-host writes one, and checks the Target
// the winrm_exec binding hands the transport carries that pin. A device
// that pins nothing hands over the zero value, the system's roots.
func TestWinRMTarget_CarriesTheDevicesPin(t *testing.T) {
	caPEM := selfSignedPEM(t)
	item, err := windows.NewServer(record.Record{ID: "w1", Name: "w1", Type: "windows_server",
		Properties: map[string]inventory.PropertyValue{"host": "win1", "port": 5986, devicetls.CAPEMProperty: caPEM}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	target, ok := engine.WinRMTarget(item)
	if !ok || !target.TLS.PinnedCA() || string(target.TLS.CAPEM()) != caPEM {
		t.Errorf("WinRMTarget(pinned) = pinned %v, ok %v", target.TLS.PinnedCA(), ok)
	}
	plain, _ := engine.WinRMTarget(newWindowsDevice())
	if plain.TLS.PinnedCA() {
		t.Error("a device pinning nothing handed over a pin")
	}
}

// selfSignedPEM returns a self-signed CA certificate as PEM.
func selfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

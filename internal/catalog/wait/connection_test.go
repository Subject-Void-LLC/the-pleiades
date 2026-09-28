// Tests for wait.connection against the same real, in-process SSH server
// the other waits run against (wait_test.go), and against WinRM ports
// nothing answers on: a machine not up yet looks the same from here as
// one whose port refuses, and a real refusal is what each try meets.
package wait_test

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// deadPort returns a local TCP port nothing listens on: one a listener
// held and gave back.
func deadPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// lateTarget is an SSH device that is not up for its first few tries:
// its port is one nothing answers on until then, as a machine still
// booting looks from outside.
type lateTarget struct {
	*waitTarget
	dead  int
	tries atomic.Int32
	after int32
}

func (d *lateTarget) SSHPort() int {
	if d.tries.Add(1) <= d.after {
		return d.dead
	}
	return d.waitTarget.SSHPort()
}

// winrmTarget is a device reached over WinRM, at a port nothing answers.
type winrmTarget struct {
	*inventorytest.Stub
	port int
}

func (d *winrmTarget) WinRMHost() string { return "127.0.0.1" }
func (d *winrmTarget) WinRMPort() int    { return d.port }

func TestConnection_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.connection")
	if !ok || d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Fatalf("wait.connection: %+v", d.Manifest)
	}
	if d.Manifest.Reversibility.Reversible || d.Check != nil || d.Manifest.NoCheckReason == "" {
		t.Errorf("a wait changes nothing, has no check, and says why: %+v", d.Manifest)
	}
}

func TestConnection_AnswersOverSSH(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	params := map[string]any{"timeout": 30, "sleep": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	if result, err := wait.Connection(context.Background(), rc, newWaitTarget(server), params); err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["transport"] != "ssh" || rc.stats["elapsed"] != 0 {
		t.Errorf("stats %v", rc.stats)
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	if diff[sdk.DiffBefore].(map[string]any)["transport"] != "ssh" {
		t.Errorf("diff %v", rc.stats[sdk.StatDiff])
	}
}

func TestConnection_WaitsForAMachineComingUp(t *testing.T) {
	server := startWaitServer(t)
	late := &lateTarget{waitTarget: newWaitTarget(server), dead: deadPort(t), after: 2}
	rc := newWaitContext(server)
	params := map[string]any{"timeout": 30, "sleep": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	if _, err := wait.Connection(context.Background(), rc, late, params); err != nil {
		t.Fatal(err)
	}
	if elapsed := rc.stats["elapsed"].(int); elapsed < 2 || late.tries.Load() < 3 {
		t.Errorf("answered after %ds and %d tries, before the machine was up", elapsed, late.tries.Load())
	}
}

func TestConnection_GivesUpQuotingTheLastTry(t *testing.T) {
	server := startWaitServer(t)
	dead := &lateTarget{waitTarget: newWaitTarget(server), dead: deadPort(t), after: 1 << 30}
	params := map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	start := time.Now()
	_, err := wait.Connection(context.Background(), newWaitContext(server), dead, params)
	if err == nil || !strings.Contains(err.Error(), "did not answer over ssh") || !strings.Contains(err.Error(), "timed out after 2s; the last try said") {
		t.Errorf("gave up with %v", err)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("gave up after %s, well past its 2s timeout", took)
	}
}

func TestConnection_TriesWinRMForADeviceReachedByIt(t *testing.T) {
	device := &winrmTarget{Stub: &inventorytest.Stub{StubName: "win-lab", Caps: []capability.Name{capability.NameWinRM, capability.NameNetworkAddressable}}, port: deadPort(t)}
	rc := &waitContext{secrets: map[string]string{"username": "Administrator", "password": "Lab-Passw0rd-1"}, stats: map[string]any{}}
	_, err := wait.Connection(context.Background(), rc, device, map[string]any{"timeout": 1, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "win-lab did not answer over winrm") {
		t.Errorf("a WinRM device nothing answers for: %v", err)
	}
	both := &waitContext{secrets: map[string]string{wire.SecretPFXBase64: "AAAA", wire.SecretCertificatePEM: "c"}, stats: map[string]any{}}
	if _, err := wait.Connection(context.Background(), both, device, map[string]any{"timeout": 1}); err == nil {
		t.Error("a credential WinRM cannot use was tried")
	}
}

func TestConnection_Refusals(t *testing.T) {
	server := startWaitServer(t)
	for why, params := range map[string]map[string]any{
		"no timeout":             {"timeout": 0},
		"a negative sleep":       {"sleep": -1},
		"no sleep":               {"sleep": 0},
		"no connect timeout":     {"connect_timeout": 0},
		"a delay past timeout":   {"timeout": 5, "delay": 5},
		"a timeout not a number": {"timeout": "soon"},
	} {
		if _, err := wait.Connection(context.Background(), newWaitContext(server), newWaitTarget(server), params); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), newWaitUnreachable(), nil); err == nil || !strings.Contains(err.Error(), "neither SSH nor WinRM") {
		t.Errorf("a device reached neither way: %v", err)
	}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), nil, nil); err == nil {
		t.Error("waited for no device")
	}
}

func TestConnection_StopsWhenTheRunIsTornDown(t *testing.T) {
	server := startWaitServer(t)
	dead := &lateTarget{waitTarget: newWaitTarget(server), dead: deadPort(t), after: 1 << 30}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := wait.Connection(ctx, newWaitContext(server), dead, map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("a torn-down run: %v", err)
	}
	delayed, stop := context.WithCancel(context.Background())
	stop()
	if _, err := wait.Connection(delayed, newWaitContext(server), newWaitTarget(server), map[string]any{"delay": 5}); err == nil {
		t.Error("a cancelled run waited out its delay")
	}
}

func TestConnection_RecordingFailures(t *testing.T) {
	server := startWaitServer(t)
	for _, stat := range []string{sdk.StatDiff, "transport", "elapsed"} {
		rc := newWaitContext(server)
		rc.failOn = stat
		if _, err := wait.Connection(context.Background(), rc, newWaitTarget(server), map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}); err == nil {
			t.Errorf("recording %s failed and the wait did not", stat)
		}
	}
}

func TestConnection_ALoginThatCannotRunACommandIsNotReady(t *testing.T) {
	server := startWaitServerWithSessionBudget(t, 0)
	params := map[string]any{"timeout": 1, "sleep": 1, "connect_timeout": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), newWaitTarget(server), params); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("a machine that logs in but runs nothing: %v", err)
	}
}

// winrmOnlyByName declares WinRM but does not implement it.
type winrmOnlyByName struct{ *inventorytest.Stub }

func TestConnection_ADeviceThatOnlyNamesWinRM(t *testing.T) {
	device := winrmOnlyByName{&inventorytest.Stub{StubName: "odd", Caps: []capability.Name{capability.NameWinRM}}}
	if _, err := wait.Connection(context.Background(), &waitContext{stats: map[string]any{}}, device, nil); err == nil || !strings.Contains(err.Error(), "neither SSH nor WinRM") {
		t.Errorf("a device naming WinRM it cannot be reached by: %v", err)
	}
}

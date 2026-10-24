// Tests for wait.connection against the same real, in-process SSH server
// the other waits run against (wait_test.go), and against WinRM ports
// nothing answers on: a machine not up yet looks the same from here as
// one whose port refuses, and a real refusal is what each try meets.
package wait_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// deadPort returns a local TCP port that refuses every connection while
// the test runs (internal/testsupport/refusing).
//
// It also gives the calling test its own remoteexec memo, discarded when
// the test ends. Every dial at the dead port is a failure the shared
// Runner's breaker counts against 127.0.0.1:<port>, with no decay, and the
// kernel hands a given-back port out again: a later test's in-process
// server that happened to land on it met "circuit open" for its own live
// address (seen once under `make test-repeat`, in
// TestPath_DiffRecordFailureIsReported). See remoteexec.SnapshotForTest.
func deadPort(t *testing.T) int {
	t.Helper()
	t.Cleanup(remoteexec.SnapshotForTest())
	return refusing.Port(t)
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

// cliTarget is a network device reached over SSH whose terminal is not a
// shell: it declares its own CLI prompt.
type cliTarget struct {
	*waitTarget
	prompt string
}

func (d *cliTarget) CLIPrompt() string { return d.prompt }

// startCLIDevice starts a scripted CLI printing prompt, which runs no
// command a POSIX machine would, and returns a server to reach it by.
func startCLIDevice(t *testing.T, prompt string) (waitServer, *remoteexectest.Server) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{Prompt: prompt}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return waitServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password, srv: srv}, srv
}

// TestConnection_WaitsForANetworkDevicesPrompt: an IOS device, or one that
// declares its prompt, is ready when a fresh login reaches that prompt,
// since its CLI has no command that does nothing. What the device received
// shows the try opened a terminal rather than running a command.
func TestConnection_WaitsForANetworkDevicesPrompt(t *testing.T) {
	params := map[string]any{"timeout": 30, "sleep": 1, sdk.ParamInsecureSkipHostKeyVerify: true}

	server, srv := startCLIDevice(t, "sw1#")
	ios := &waitTarget{Stub: &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS}}, host: server.host, port: server.port}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), ios, params); err != nil {
		t.Fatal(err)
	}
	if got := srv.Commands(); len(got) != 2 || got[0] != "terminal length 0" || got[1] != "terminal width 512" {
		t.Errorf("the IOS device received %q, want only IOS's session setup (paging and wrapping off)", got)
	}

	server, srv = startCLIDevice(t, "edge-1>")
	generic := &cliTarget{waitTarget: &waitTarget{Stub: &inventorytest.Stub{StubName: "edge-1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameNetworkCLI}}, host: server.host, port: server.port}, prompt: "edge-1>"}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), generic, params); err != nil {
		t.Fatal(err)
	}
	if got := srv.Commands(); len(got) != 0 {
		t.Errorf("the device that declares its prompt received %q, want nothing", got)
	}
}

// TestConnection_ANetworkDeviceThatNeverPromptsIsNotReady: a login that
// never reaches the prompt is a failed try, and the wait times out.
func TestConnection_ANetworkDeviceThatNeverPromptsIsNotReady(t *testing.T) {
	server, _ := startCLIDevice(t, "login: ")
	ios := &waitTarget{Stub: &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS}}, host: server.host, port: server.port}
	params := map[string]any{"timeout": 3, "sleep": 1, "connect_timeout": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	if _, err := wait.Connection(context.Background(), newWaitContext(server), ios, params); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
}

// TestConnection_ANetworkDeviceNotLettingATerminalInIsNotReady: a try that
// cannot log in, or logs in and is refused a terminal, is a failed try.
func TestConnection_ANetworkDeviceNotLettingATerminalInIsNotReady(t *testing.T) {
	params := map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	ios := func(server waitServer) *waitTarget {
		return &waitTarget{Stub: &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS}}, host: server.host, port: server.port}
	}

	down := waitServer{host: "127.0.0.1", port: deadPort(t), username: "u", password: "p"}
	if _, err := wait.Connection(context.Background(), newWaitContext(down), ios(down), params); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a device refusing the connection: %v", err)
	}

	srv, err := remoteexectest.Start(remoteexectest.Options{SessionLimit: remoteexectest.Limit(0), Device: &remoteexectest.Device{Prompt: "sw1#"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	busy := waitServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password, srv: srv}
	if _, err := wait.Connection(context.Background(), newWaitContext(busy), ios(busy), params); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a device refusing the terminal: %v", err)
	}
	if srv.Logins() == 0 {
		t.Error("the refused terminal was never asked for: no try logged in")
	}
}

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
	for _, stat := range []string{sdk.StatDiff, "transport", "elapsed", "tries", "failures"} {
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

// pooledWaitContext is a waitContext whose run keeps SSH connections open
// between tasks, as `pleiades run` does by default.
type pooledWaitContext struct {
	*waitContext
	pool *remoteexec.Pool
}

func (c pooledWaitContext) ConnectionPool() *remoteexec.Pool { return c.pool }

// TestConnection_EveryTryLogsInAfresh: two waits in one run that keeps its
// connections each log in, rather than the second borrowing the session
// the first left in the run's pool. A wait that reuses a session proves
// nothing about whether the machine lets the account in now: after a
// password or sshd change, or a reboot the session outlived, it would pass
// on what was true before.
func TestConnection_EveryTryLogsInAfresh(t *testing.T) {
	server := startWaitServer(t)
	pool := remoteexec.NewPool(time.Minute)
	t.Cleanup(func() { _ = pool.Close() })
	rc := pooledWaitContext{newWaitContext(server), pool}
	params := map[string]any{"timeout": 30, "sleep": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	for range 2 {
		if _, err := wait.Connection(context.Background(), rc, newWaitTarget(server), params); err != nil {
			t.Fatal(err)
		}
	}
	if logins := server.srv.Logins(); logins != 2 {
		t.Errorf("two waits logged in %d time(s): a try borrowed a pooled session", logins)
	}
}

// TestConnection_ASteadyStreakIsThatManyLogins: steady 3 answers only after
// three tries in a row, each its own login on the real server, with the
// tries and failures recorded; a machine coming up late is waited for with
// an exponential backoff first.
func TestConnection_ASteadyStreakIsThatManyLogins(t *testing.T) {
	server := startWaitServer(t)
	late := &lateTarget{waitTarget: newWaitTarget(server), dead: deadPort(t), after: 2}
	rc := newWaitContext(server)
	params := map[string]any{"timeout": 30, "backoff": "exponential", "sleep": 1, "max_sleep": 2,
		"steady": 3, "steady_interval": 1, "connect_timeout": 2, sdk.ParamInsecureSkipHostKeyVerify: true}
	if _, err := wait.Connection(context.Background(), rc, late, params); err != nil {
		t.Fatal(err)
	}
	if logins := server.srv.Logins(); logins != 3 {
		t.Errorf("a streak of 3 made %d login(s)", logins)
	}
	if rc.stats["tries"] != 5 || rc.stats["failures"] != 2 {
		t.Errorf("stats %v", rc.stats)
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

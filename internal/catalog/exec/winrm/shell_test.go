package winrm_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/exec/winrm"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// There is no in-process WinRM server to test against, and a stub one
// would only prove this method agrees with the stub. What is covered
// here is everything that happens before the network: parameter
// validation, shell parsing, the device accessor, and the manifest's own
// claims. The real proof is
// cmd/pleiades/winrm_static_ip_release_gate_test.go against a live
// Windows host.

// winrmDevice declares and structurally implements WinRMCapable.
type winrmDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *winrmDevice) WinRMHost() string { return d.host }
func (d *winrmDevice) WinRMPort() int    { return d.port }

func device() *winrmDevice {
	return &winrmDevice{
		Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM}},
		host: "192.0.2.1",
		port: 5985,
	}
}

type ctxStub struct{ stats map[string]any }

func (c *ctxStub) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "secret"}
}
func (c *ctxStub) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *ctxStub) EmitFact(k string, v any) error { return c.SetStat(k, v) }

func invoke(t *testing.T, params map[string]any) error {
	t.Helper()
	desc, ok := collection.Lookup("exec.winrm.shell")
	if !ok {
		t.Fatal("exec.winrm.shell is not registered")
	}
	_, err := desc.Invoke(context.Background(), &ctxStub{stats: map[string]any{}}, device(), params)
	return err
}

// TestRegistered checks what the catalog claims about this method, from
// the live registry rather than from a list.
func TestRegistered(t *testing.T) {
	desc, ok := collection.Lookup("exec.winrm.shell")
	if !ok {
		t.Fatal("exec.winrm.shell is not registered")
	}
	if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
		t.Fatalf("Status = %v, Invoke nil = %v; want implemented with an implementation",
			desc.Manifest.Status, desc.Invoke == nil)
	}
	caps := desc.Manifest.RequiredCapabilities
	if len(caps) != 1 || caps[0] != capability.NameWinRM {
		t.Errorf("RequiredCapabilities = %v, want [WinRMCapable]", caps)
	}
	// A method answering "not reversible" must say why, which Register
	// enforces; this checks the answer was considered rather than left as
	// a zero value with a throwaway note.
	if desc.Manifest.Reversibility.Reversible {
		t.Error("Reversible = true, but an arbitrary script's effect cannot be inspected or undone")
	}
	if desc.Manifest.Reversibility.Notes == "" {
		t.Error("declares itself not reversible with no reason")
	}
}

// TestRefusesBadParams covers every authoring mistake, each of which
// must be caught before anything is dialed so the error names the
// runbook rather than the device.
func TestRefusesBadParams(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]any
		wantText string
	}{
		{name: "no command", params: map[string]any{"shell": "powershell"}, wantText: "command"},
		{name: "no shell", params: map[string]any{"command": "hostname"}, wantText: "shell"},
		{
			// The refusal that matters most: "none" would mean running a
			// program with an argument vector nothing parses, and this
			// transport cannot promise that.
			name:     "shell none is refused",
			params:   map[string]any{"command": "hostname", "shell": "none"},
			wantText: "WINRS_SKIP_CMD_SHELL",
		},
		{
			name:     "unknown shell lists the valid values",
			params:   map[string]any{"command": "hostname", "shell": "bash"},
			wantText: "powershell",
		},
		{
			name:     "cmd script closing the CDATA section",
			params:   map[string]any{"command": "echo ]]> hi", "shell": "cmd"},
			wantText: "CDATA",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := invoke(t, tt.params)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantText)
			}
			if !strings.Contains(err.Error(), "exec.winrm.shell") {
				t.Errorf("error = %v, want it to name the method", err)
			}
		})
	}
}

// invokeOn runs the method against a device pointed at addr, returning
// the recorded stats alongside the error so a test can assert on what
// the task claimed as well as whether it failed.
func invokeOn(t *testing.T, host string, port int, params map[string]any) (map[string]any, error) {
	t.Helper()
	desc, ok := collection.Lookup("exec.winrm.shell")
	if !ok {
		t.Fatal("exec.winrm.shell is not registered")
	}
	dev := device()
	dev.host = host
	dev.port = port
	rc := &ctxStub{stats: map[string]any{}}
	_, err := desc.Invoke(context.Background(), rc, dev, params)
	return rc.stats, err
}

// deafListener accepts connections and never answers, which is what a
// host looks like once a script has reconfigured its own network out
// from under the request.
func deafListener(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestTimeoutIsBounded proves the task gives up rather than hanging on a
// device that accepts a connection and never answers.
//
// Measured against a real defect: the library discards both the context
// and its own configured timeout on the encrypted path, and a task that
// changed a device's address blocked for nearly three minutes with no way
// to shorten it.
func TestTimeoutIsBounded(t *testing.T) {
	port := deafListener(t)

	start := time.Now()
	_, err := invokeOn(t, "127.0.0.1", port, map[string]any{
		"command": "echo hi",
		"shell":   "powershell",
		"timeout": 2,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a device that never answers was reported as success")
	}
	if elapsed > 30*time.Second {
		t.Errorf("the task ran for %s against timeout: 2, so the bound is not reaching the transport", elapsed)
	}
}

// TestExpectDisconnectStillFailsWhenTheDeviceNeverReturns is the half of
// the reconnect contract that keeps the other half honest.
//
// expect_disconnect must not become a way to make any failure look like
// success. A device that is not there cannot come back, so it stays a
// failure, and the message has to say the change may be half applied
// rather than reporting a clean error about a connection.
func TestExpectDisconnectStillFailsWhenTheDeviceNeverReturns(t *testing.T) {
	port := deafListener(t)

	stats, err := invokeOn(t, "127.0.0.1", port, map[string]any{
		"command":           "netsh interface ipv4 set address name=Ethernet static 192.0.2.10 255.255.255.0",
		"shell":             "powershell",
		"timeout":           1,
		"expect_disconnect": true,
		"reconnect_timeout": 3,
	})
	if err == nil {
		t.Fatal("a device that never came back was reported as success")
	}
	if !strings.Contains(err.Error(), "never came back") {
		t.Errorf("error = %q, want it to say the device did not return", err)
	}
	if !strings.Contains(err.Error(), "console") {
		t.Errorf("error = %q, want it to say the host may need attention at the console", err)
	}
	if _, recorded := stats["result_known"]; recorded {
		t.Error("stats were recorded for a task that failed, which would let a later task read an outcome that never happened")
	}
}

// TestReconnectTimeoutWithoutExpectDisconnectIsRefused covers the
// combination that would silently do nothing.
//
// Setting a reconnect timeout is a statement that this task expects to
// lose its connection. Accepting it while never waiting would leave an
// author believing they had configured a safeguard they had not.
func TestReconnectTimeoutWithoutExpectDisconnectIsRefused(t *testing.T) {
	err := invoke(t, map[string]any{
		"command":           "echo hi",
		"shell":             "powershell",
		"reconnect_timeout": 30,
	})
	if err == nil {
		t.Fatal("reconnect_timeout without expect_disconnect was accepted")
	}
	if !strings.Contains(err.Error(), "expect_disconnect") {
		t.Errorf("error = %q, want it to name the parameter that was missing", err)
	}
}

// TestRefusesABadTimeout covers the seconds parser's own rules.
func TestRefusesABadTimeout(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "zero would give up before looking", value: 0, want: "below 1"},
		{name: "negative", value: -5, want: "below 1"},
		{name: "milliseconds written as seconds", value: 300000, want: "ceiling"},
		{name: "not a number", value: "30s", want: "not a whole number"},
		{name: "fractional", value: 1.5, want: "not a whole number"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := invoke(t, map[string]any{"command": "echo hi", "shell": "powershell", "timeout": tt.value})
			if err == nil {
				t.Fatalf("timeout %v was accepted", tt.value)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestAcceptsAFloatTimeout proves a whole number that crossed a JSON
// boundary is still a whole number.
//
// The runner hands a Collection method its params through a per-task
// subprocess, so timeout: 30 written in YAML arrives as float64(30) on
// the Walk tier and int(30) on the Crawl tier. Refusing one of those
// would make the same runbook work on one tier and fail on the other.
func TestAcceptsAFloatTimeout(t *testing.T) {
	port := deafListener(t)
	_, err := invokeOn(t, "127.0.0.1", port, map[string]any{
		"command": "echo hi",
		"shell":   "powershell",
		"timeout": float64(2),
	})
	if err == nil {
		t.Fatal("expected the deaf listener to produce a timeout")
	}
	if strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("a float carrying a whole number was refused: %v", err)
	}
}

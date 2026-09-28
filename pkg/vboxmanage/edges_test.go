// Tests for the paths a real host rarely takes: answers VBoxManage should
// not give, a runner that cannot run anything, and a host that cannot be
// reached.
package vboxmanage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// failing is a runner that cannot run anything.
type failing struct{}

func (failing) Run(context.Context, string, []string) (Output, error) {
	return Output{}, errors.New("connection refused")
}

func (failing) PowerShell(context.Context, string, string) (Output, error) {
	return Output{}, errors.New("connection refused")
}

// answering is a runner that answers every call with out.
type answering struct{ out Output }

func (a answering) Run(context.Context, string, []string) (Output, error) { return a.out, nil }

func (a answering) PowerShell(context.Context, string, string) (Output, error) { return a.out, nil }

func TestHost_EdgeAnswers(t *testing.T) {
	ctx := context.Background()
	if _, err := (Host{}).List(ctx); err == nil {
		t.Error("a host with no runner ran")
	}
	if err := (Host{Runner: failing{}, Path: vbox}).Start(ctx, "vm1"); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("a runner failure: %v", err)
	}
	strange := Host{Runner: answering{Output{Stdout: "not a list line\r\n"}}, Path: vbox}
	if _, err := strange.List(ctx); err == nil || !strings.Contains(err.Error(), "does not recognize") {
		t.Errorf("an unknown list line: %v", err)
	}
	if _, err := strange.TakeSnapshot(ctx, "vm1", "s1", ""); err == nil || !strings.Contains(err.Error(), "did not report the UUID") {
		t.Errorf("a take with no UUID: %v", err)
	}
	for _, text := range []string{"name=\"a\"\r\nUUID=\"u\"\r\nVMState=\"running\"\r\nmemory=lots\r\n", "memory=64\r\n"} {
		if _, err := (Host{Runner: answering{Output{Stdout: text}}, Path: vbox}).Machine(ctx, "vm1"); err == nil {
			t.Errorf("showvminfo %q was accepted", text)
		}
	}
	if got := firstErrorLine(Output{}); got != "no output" {
		t.Errorf("firstErrorLine of nothing = %q", got)
	}
	if got := firstErrorLine(Output{Stdout: "\r\n  only stdout\r\n"}); got != "only stdout" {
		t.Errorf("firstErrorLine of stdout = %q", got)
	}
}

func TestWindowsAutostart_ServiceName(t *testing.T) {
	a := WindowsAutostart{Host: Host{Runner: answering{account}, Path: vbox}}
	if name, err := a.ServiceName(context.Background()); err != nil || name != "VBoxAutostartSvcvengeancepleiades-gate" {
		t.Errorf("ServiceName = %q, %v", name, err)
	}
	a = WindowsAutostart{Host: Host{Runner: failing{}, Path: vbox}}
	if _, err := a.ServiceName(context.Background()); err == nil {
		t.Error("a runner failure gave a service name")
	}
}

// TestWinRMRunner_RefusesAndReportsUnreachable runs the real WinRM runner:
// a program path no Windows command line can carry is refused before any
// connection, and a host nothing answers on is an error, not an Output.
func TestWinRMRunner_RefusesAndReportsUnreachable(t *testing.T) {
	r := WinRMRunner{Target: winrmexec.Target{Host: "127.0.0.1", Port: 1}, Auth: winrmexec.Auth{Username: "u", Password: "p"},
		Options: winrmexec.Options{Timeout: 5 * time.Second}}
	if _, err := r.Run(context.Background(), `C:\a"b\VBoxManage.exe`, nil); err == nil {
		t.Error("a program path with a quote was sent")
	}
	if _, err := r.Run(context.Background(), `C:\VBoxManage.exe`, []string{"list", "vms"}); err == nil {
		t.Error("an unreachable host gave an Output")
	}
}

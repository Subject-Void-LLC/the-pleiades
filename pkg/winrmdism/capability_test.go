// Package winrmdism: tests for the capability calls, with the SYSTEM
// task's WinRM call replaced (runSystem); the real host is the lab's
// win-check.
package winrmdism

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// TestCheckCapabilityName accepts the identities DISM lists and refuses
// anything else, above all what a command line would read as more.
func TestCheckCapabilityName(t *testing.T) {
	for _, ok := range []string{
		"ServerCore.AppCompatibility~~~~0.0.1.0",
		"Language.Basic~~~en-US~0.0.1.0",
		"Browser.InternetExplorer~~~~0.0.11.0",
		"OpenSSH.Server~~~~0.0.1.0",
	} {
		if err := CheckCapabilityName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "ServerCore.AppCompatibility", "ServerCore.AppCompatibility~~~0.0.1.0",
		"a~~~~0.0.1.0 /source:x", "a~~~~0.0.1.0\"", "a~~~~0.0.1.0&calc", "-a~~~~1", "a~~~~1%PATH%",
		"a~~~~x", "a~~~~1\n",
	} {
		if err := CheckCapabilityName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestCheckArgumentPath refuses what a scheduled task's arguments would
// change: Task Scheduler expands %NAME%, a quote ends the argument.
func TestCheckArgumentPath(t *testing.T) {
	for _, ok := range []string{`C:\Windows\Logs\DISM\dism.log`, `D:\sources\sxs`, `\\fileserver\fod\2025`, `C:\Program Files\x`} {
		if err := checkArgumentPath("p", ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{`C:\%TEMP%\x`, `C:\a"b`, "C:\\a\nb", "C:\\a\x00b"} {
		if err := checkArgumentPath("p", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestCapabilityArguments: the change never restarts the host, never asks,
// logs in English to the device's log, and quotes a path with spaces as
// the standard Windows parser reads it.
func TestCapabilityArguments(t *testing.T) {
	got := capabilityArguments("/add-capability", "A.B~~~~0.0.1.0", `C:\Log Dir\dism.log`, "/source:"+winrmexec.QuoteArg(`D:\fod`), "/limitaccess")
	want := `/online /english /add-capability /capabilityname:A.B~~~~0.0.1.0 /norestart /quiet /logpath:"C:\Log Dir\dism.log" /source:D:\fod /limitaccess`
	if got != want {
		t.Errorf("arguments\n got %s\nwant %s", got, want)
	}
}

// TestCapabilityState_Predicates: a pending state counts as the verb's
// own, done but for a restart.
func TestCapabilityState_Predicates(t *testing.T) {
	for state, want := range map[string][2]bool{
		"Installed":           {true, false},
		"Install Pending":     {true, false},
		"Not Present":         {false, true},
		"Staged":              {false, true},
		"Uninstall Pending":   {false, true},
		"Partially Installed": {false, false},
		"Superseded":          {false, false},
	} {
		c := CapabilityState{Name: "x", Exists: true, State: state}
		if c.Installed() != want[0] || c.Removed() != want[1] {
			t.Errorf("%s: installed %v removed %v, want %v", state, c.Installed(), c.Removed(), want)
		}
	}
	if m := (CapabilityState{Name: "n", Exists: true, State: "Installed"}).Map(); m["state"] != "Installed" || m["exists"] != true || m["name"] != "n" {
		t.Errorf("Map = %v", m)
	}
}

// TestAsSystemScript_FitsTheCommandLine: the script travels as
// -EncodedCommand inside the 8191 characters the service's cmd.exe takes.
func TestAsSystemScript_FitsTheCommandLine(t *testing.T) {
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, asSystemScript, winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}

// systemAnswer replaces runSystem with one answering out and code,
// recording the request it was sent and the timeout it was given.
func systemAnswer(t *testing.T, out string, code int, err error) (*map[string]any, *time.Duration) {
	t.Helper()
	old := runSystem
	t.Cleanup(func() { runSystem = old })
	sent := map[string]any{}
	var timeout time.Duration
	runSystem = func(_ context.Context, _ Session, stdin string, opts winrmexec.Options) (winrmexec.Result, error) {
		if jerr := json.Unmarshal([]byte(stdin), &sent); jerr != nil {
			t.Fatalf("stdin is not JSON: %v", jerr)
		}
		timeout = opts.Timeout
		return winrmexec.Result{Stdout: out, ExitCode: code}, err
	}
	return &sent, &timeout
}

// TestAddCapability_RunsDISMAsSystemAndReadsItsExitCode: the request names
// a fresh task, the arguments and the limit; 0 and 3010 are success, the
// second needing a restart; a failure names its code and what it means.
func TestAddCapability_RunsDISMAsSystemAndReadsItsExitCode(t *testing.T) {
	sent, timeout := systemAnswer(t, "state=Ready\r\nresult=3010\r\n", 0, nil)
	res, err := AddCapability(context.Background(), Session{}, `C:\Windows\Logs\DISM\dism.log`, "A.B~~~~0.0.1.0", `D:\fod`, 30*time.Minute)
	if err != nil || !res.RebootRequired {
		t.Fatalf("3010: %+v, %v", res, err)
	}
	if task, _ := (*sent)["task"].(string); !strings.HasPrefix(task, "dism-") || len(task) != len("dism-")+16 {
		t.Errorf("task %q", task)
	}
	if args, _ := (*sent)["arguments"].(string); !strings.Contains(args, "/add-capability /capabilityname:A.B~~~~0.0.1.0") || !strings.HasSuffix(args, `/source:D:\fod /limitaccess`) {
		t.Errorf("arguments %q", args)
	}
	if (*sent)["seconds"] != float64(1800) || *timeout != 32*time.Minute {
		t.Errorf("seconds %v, WinRM timeout %s", (*sent)["seconds"], *timeout)
	}

	systemAnswer(t, "state=Ready\nresult=0\n", 0, nil)
	if res, err := RemoveCapability(context.Background(), Session{}, `C:\dism.log`, "A.B~~~~0.0.1.0", time.Minute); err != nil || res.RebootRequired {
		t.Errorf("0: %+v, %v", res, err)
	}

	systemAnswer(t, "state=Ready\nresult=2148469076\n", 0, nil) // 0x800F0954
	if _, err := AddCapability(context.Background(), Session{}, `C:\dism.log`, "A.B~~~~0.0.1.0", "", time.Minute); err == nil ||
		!strings.Contains(err.Error(), "0x800F0954") || !strings.Contains(err.Error(), "Windows Update") {
		t.Errorf("0x800F0954: %v", err)
	}
}

// TestRunAsSystem_ReportsWhatItCouldNotLearn: a run past its limit, a task
// never started, a script that failed, an answer with no code, a WinRM
// failure and a zero timeout are each errors saying which.
func TestRunAsSystem_ReportsWhatItCouldNotLearn(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		code int
		err  error
		want string
	}{
		"still running": {"state=Running\nresult=267009\n", 0, nil, "still running"},
		"never started": {"state=Ready\nresult=267011\n", 0, nil, "never started"},
		"script failed": {"", 1, nil, "failed (exit 1)"},
		"no exit code":  {"state=Ready\n", 0, nil, "gave no exit code"},
		"winrm failure": {"", 0, errors.New("connection refused"), "connection refused"},
	} {
		systemAnswer(t, tc.out, tc.code, tc.err)
		if _, err := runAsSystem(context.Background(), Session{}, "/online", time.Minute); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	if _, err := runAsSystem(context.Background(), Session{}, "/online", 0); err == nil {
		t.Error("a zero timeout was accepted")
	}
}

// TestChangeCapability_RefusesBeforeContact: a bad name, log path or source
// is refused with nothing sent.
func TestChangeCapability_RefusesBeforeContact(t *testing.T) {
	sent, _ := systemAnswer(t, "state=Ready\nresult=0\n", 0, nil)
	for name, call := range map[string]func() error{
		"name": func() error {
			_, err := AddCapability(context.Background(), Session{}, `C:\l`, "x /all", "", time.Minute)
			return err
		},
		"log": func() error {
			_, err := RemoveCapability(context.Background(), Session{}, `C:\%x%`, "A~~~~1", time.Minute)
			return err
		},
		"source": func() error {
			_, err := AddCapability(context.Background(), Session{}, `C:\l`, "A~~~~1", `D:\"x`, time.Minute)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("a bad %s was accepted", name)
		}
	}
	if len(*sent) != 0 {
		t.Errorf("a refused change reached the device: %v", *sent)
	}
}

// TestCapabilityStatus_RefusesABadNameAndReportsAnUnreachableHost.
func TestCapabilityStatus_RefusesABadNameAndReportsAnUnreachableHost(t *testing.T) {
	if _, err := CapabilityStatus(context.Background(), unreachableSession(), testLogPath, "nope"); err == nil || !strings.Contains(err.Error(), "capability identity") {
		t.Errorf("a bad name: %v", err)
	}
	if _, err := CapabilityStatus(context.Background(), unreachableSession(), testLogPath, "A.B~~~~0.0.1.0"); err == nil || !strings.Contains(err.Error(), "reading the state of capability") {
		t.Errorf("an unreachable host: %v", err)
	}
}

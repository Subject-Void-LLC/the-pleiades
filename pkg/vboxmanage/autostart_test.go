// Tests for WindowsAutostart's sequence, through a scripted runner that
// answers each program with the outputs a real host gave, in order. The
// Release Gate runs the same sequence against a real host.
package vboxmanage

import (
	"context"
	"strings"
	"testing"
	"time"
)

// scripted answers each call by program (and VBoxManage's first argument),
// taking answers from a queue; the last one repeats.
type scripted struct {
	t       *testing.T
	answers map[string][]Output
	calls   []string
}

// key names a call: the program's base name, and VBoxManage's verb.
func key(program string, args []string) string {
	base := program[strings.LastIndex(program, `\`)+1:]
	if base == "VBoxManage.exe" && len(args) > 0 {
		return base + " " + args[0]
	}
	if base == "sc.exe" && len(args) > 0 {
		return base + " " + args[0]
	}
	return base
}

func (s *scripted) Run(_ context.Context, program string, args []string) (Output, error) {
	k := key(program, args)
	s.calls = append(s.calls, k+" "+strings.Join(args, " "))
	queue := s.answers[k]
	if len(queue) == 0 {
		s.t.Fatalf("no answer scripted for %s", k)
	}
	out := queue[0]
	if len(queue) > 1 {
		s.answers[k] = queue[1:]
	}
	return out, nil
}

// autostartWith returns a WindowsAutostart whose host answers from answers.
func autostartWith(t *testing.T, answers map[string][]Output) (WindowsAutostart, *scripted) {
	s := &scripted{t: t, answers: answers}
	return WindowsAutostart{Host: Host{Runner: s, Path: vbox}, Poll: time.Millisecond, Timeout: 200 * time.Millisecond}, s
}

// Answers a real host gave on the way through a start.
var (
	noneRunning = Output{}
	oneRunning  = Output{Stdout: "\"other\" {11111111-1111-1111-1111-111111111111}\r\n"}
	account     = Output{Stdout: "vengeance\\pleiades-gate\r\n"}
	svcPresent  = Output{Stdout: "\"VBoxSVC.exe\",\"15008\",\"Services\",\"0\",\"25,000 K\"\r\n"}
	svcGone     = Output{Stdout: "INFO: No tasks are running which match the specified criteria.\r\n"}
	scRunning   = Output{Stdout: "        STATE              : 4  RUNNING\r\n"}
	scStopped   = Output{Stdout: "        STATE              : 1  STOPPED\r\n"}
)

func TestWindowsAutostart_PlainStartWhileAVMRuns(t *testing.T) {
	a, s := autostartWith(t, map[string][]Output{
		"VBoxManage.exe list":    {oneRunning},
		"VBoxManage.exe startvm": {{}},
	})
	viaService, err := a.Start(context.Background(), "pleiades-probe")
	if err != nil || viaService {
		t.Fatalf("Start = %v, %v; want a plain start", viaService, err)
	}
	if got := s.calls[len(s.calls)-1]; got != "VBoxManage.exe startvm startvm pleiades-probe --type headless" {
		t.Errorf("last call = %q", got)
	}
}

func TestWindowsAutostart_StartsThroughTheService(t *testing.T) {
	a, s := autostartWith(t, map[string][]Output{
		"VBoxManage.exe list":       {noneRunning},
		"whoami.exe":                {account},
		"VBoxManage.exe modifyvm":   {{}},
		"tasklist.exe":              {svcPresent, svcGone},
		"sc.exe start":              {scRunning},
		"sc.exe query":              {scRunning, scStopped},
		"VBoxManage.exe showvminfo": {{Stdout: fixture(t, "showvminfo-running.stdout")}},
	})
	viaService, err := a.Start(context.Background(), "pleiades-probe")
	if err != nil || !viaService {
		t.Fatalf("Start = %v, %v", viaService, err)
	}
	var order []string
	for _, c := range s.calls {
		order = append(order, strings.SplitN(c, " ", 3)[0]+" "+strings.SplitN(c+" ", " ", 3)[1])
	}
	want := "VBoxManage.exe list whoami.exe  VBoxManage.exe modifyvm tasklist.exe /FI tasklist.exe /FI " +
		"sc.exe start sc.exe query sc.exe query VBoxManage.exe showvminfo"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("order = %q\nwant    %q", got, want)
	}
	// Nothing reads VirtualBox between the server's exit and the service
	// stopping: the one ordering rule this sequence exists to keep.
	joined := strings.Join(s.calls, "\n")
	between := joined[strings.LastIndex(joined, "tasklist.exe"):strings.LastIndex(joined, "sc.exe query")]
	if strings.Contains(between, "VBoxManage.exe") {
		t.Errorf("VBoxManage was called while the service ran:\n%s", between)
	}
	if !strings.Contains(joined, `tasklist.exe /FI IMAGENAME eq VBoxSVC.exe /FI USERNAME eq vengeance\pleiades-gate /NH /FO CSV`) {
		t.Errorf("tasklist was not filtered to the account:\n%s", joined)
	}
	if !strings.Contains(joined, "sc.exe start start VBoxAutostartSvcvengeancepleiades-gate") {
		t.Errorf("the service name was not the account's:\n%s", joined)
	}
}

func TestWindowsAutostart_Failures(t *testing.T) {
	base := func() map[string][]Output {
		return map[string][]Output{
			"VBoxManage.exe list": {noneRunning}, "whoami.exe": {account}, "VBoxManage.exe modifyvm": {{}},
			"tasklist.exe": {svcGone}, "sc.exe start": {scRunning}, "sc.exe query": {scStopped},
			"VBoxManage.exe showvminfo": {{Stdout: fixture(t, "showvminfo-running.stdout")}},
		}
	}
	for name, tt := range map[string]struct {
		change func(map[string][]Output)
		want   string
	}{
		"whoami without a domain": {func(m map[string][]Output) { m["whoami.exe"] = []Output{{Stdout: "gate\r\n"}} }, `not DOMAIN\user`},
		"the service refuses to start": {func(m map[string][]Output) {
			m["sc.exe start"] = []Output{{ExitCode: 1056, Stdout: "[SC] StartService FAILED 1056:\r\n\r\nAn instance of the service is already running.\r\n"}}
		}, "FAILED 1056"},
		"the VM is not running after the service": {func(m map[string][]Output) {
			m["VBoxManage.exe showvminfo"] = []Output{{Stdout: fixture(t, "showvminfo-poweroff.stdout")}}
		}, "VBoxAutostart.log"},
		"the server never exits":  {func(m map[string][]Output) { m["tasklist.exe"] = []Output{svcPresent} }, "did not happen within"},
		"the service never stops": {func(m map[string][]Output) { m["sc.exe query"] = []Output{scRunning} }, "did not happen within"},
	} {
		answers := base()
		tt.change(answers)
		a, _ := autostartWith(t, answers)
		if _, err := a.Start(context.Background(), "pleiades-probe"); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	answers := base()
	answers["tasklist.exe"] = []Output{svcPresent}
	a, _ := autostartWith(t, answers)
	a.Timeout = time.Hour
	if _, err := a.Start(ctx, "pleiades-probe"); err == nil {
		t.Error("a cancelled start kept waiting")
	}
}

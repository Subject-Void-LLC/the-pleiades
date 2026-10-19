// A lab check's machine as a VirtualBox VM: started when a run needs it and
// put back afterwards with its state saved, so a box holds the host's
// memory only while a check runs on it (the lab's bsd-check takes 4 GB).
package oscheck

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Box is a lab machine's VM on a VirtualBox host its project manages.
type Box struct {
	// Host drives the VirtualBox host (a device with virtualbox: true).
	Host Pleiades
	// Device drives the machine itself, to wait for it once started.
	Device Pleiades
	// VM is the machine's name on the host.
	VM string
}

// LabBox reads a lab check's VM settings: the VirtualBox host in the
// project, and the VM's name there, which is the device's when it is not
// set. No host is no box (empty host, nil error): the machine is up on its
// own and the check never starts or stops it.
func LabBox(s Settings, hostKey, vmKey, device string) (host, vm string, err error) {
	if hostKey == "" {
		return "", "", nil
	}
	host, vm = s.Values[hostKey], s.Values[vmKey]
	switch {
	case host == "" && vm != "":
		return "", "", fmt.Errorf("%s names the VM on the VirtualBox host %s names; set %s too, or neither", vmKey, hostKey, hostKey)
	case host == "":
		return "", "", nil
	case !deviceName.MatchString(host):
		return "", "", fmt.Errorf("%s=%q is not a device name", hostKey, host)
	case vm == "":
		vm = device
	}
	if !deviceName.MatchString(vm) {
		return "", "", fmt.Errorf("%s=%q is not a VM name", vmKey, vm)
	}
	return host, vm, nil
}

// bootWait bounds how long a started box may take to answer a login: a
// saved one resumes in seconds, a cold one boots (Windows on one CPU takes
// a few minutes).
const bootWait = 10 * time.Minute

// PutBackGrace bounds putting a box back, whatever ended the run, so a
// host that stopped answering cannot hold the check open.
const PutBackGrace = 5 * time.Minute

// steadyAnswers is how many times in a row, steadyGap apart, a box this
// run started must pass the liveness check the flow makes next (Lab's
// Reachable) before the run uses it. A win-check resumed from its saved
// state answered one login and then none for over 20 seconds (2026-10-18;
// its clock was 75 minutes slow until Guest Additions set it, two minutes
// after the resume), so one answer is not a box that is up.
const steadyAnswers = 3

// steadyGap is the pause between those checks.
const steadyGap = 10 * time.Second

// boxNow and boxSleep are the clock and the pause Up waits with; a test
// replaces them.
var (
	boxNow   = time.Now
	boxSleep = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
)

// stateStat is virt.vbox.vm.info's return holding the VM's state.
const stateStat = "state"

// adhocStats runs one method and returns its task's stats; a test replaces
// it.
var adhocStats = func(ctx context.Context, p Pleiades, method string, params ...string) (map[string]any, error) {
	return p.AdhocStats(ctx, method, params...)
}

// Up starts the box when it is not running, waits until the machine
// answers logins steadily, and reports whether it started it.
func (b Box) Up(ctx context.Context, tool string, stdout io.Writer) (bool, error) {
	stats, err := adhocStats(ctx, b.Host, "virt.vbox.vm.info", "name="+b.VM)
	if err != nil {
		return false, fmt.Errorf("reading the VM %s on %s: %v", b.VM, b.Host.Device, err)
	}
	state, _ := stats[stateStat].(string)
	if state == "running" {
		return false, nil
	}
	fmt.Fprintf(stdout, "%s: starting the VM %s on %s (it was %s); it is put back, saved, when the run ends\n", tool, b.VM, b.Host.Device, FirstOf(state, "in no state it named"))
	if _, err := adhocStats(ctx, b.Host, "virt.vbox.vm.start", "name="+b.VM); err != nil {
		return false, fmt.Errorf("starting the VM %s on %s: %v", b.VM, b.Host.Device, err)
	}
	if err := b.waitSteady(ctx); err != nil {
		return true, fmt.Errorf("the VM %s started, and %s did not answer logins steadily within %s: %w", b.VM, b.Device.Device, bootWait, err)
	}
	return true, nil
}

// waitSteady waits, within bootWait, until the machine answers a login and
// then passes the flow's liveness check steadyAnswers times in a row; a
// miss starts the wait again.
func (b Box) waitSteady(ctx context.Context) error {
	deadline := boxNow().Add(bootWait)
	for {
		left := int(deadline.Sub(boxNow()) / time.Second)
		if left < 1 {
			return fmt.Errorf("it never passed %d liveness checks in a row", steadyAnswers)
		}
		if _, err := adhocStats(ctx, b.Device, "wait.connection", fmt.Sprintf("timeout=%d", left), "connect_timeout=10", "sleep=5"); err != nil {
			return err
		}
		steady, err := b.answersSteadily(ctx)
		if err != nil || steady {
			return err
		}
	}
}

// answersSteadily reports whether the machine passes the flow's liveness
// check steadyAnswers times, steadyGap apart; an error is the run's
// context ending.
func (b Box) answersSteadily(ctx context.Context) (bool, error) {
	for range steadyAnswers {
		if err := boxSleep(ctx, steadyGap); err != nil {
			return false, err
		}
		if _, err := adhocStats(ctx, b.Device, "wait.connection", fmt.Sprintf("timeout=%d", waitSeconds), "connect_timeout=5", "sleep=5"); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
	}
	return true, nil
}

// PutBack saves the box's state, which frees its memory on the host; the
// next Up resumes it. It has a context of its own, bounded by PutBackGrace,
// since it runs after an interrupt too.
func (b Box) PutBack(tool string, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), PutBackGrace)
	defer cancel()
	if _, err := adhocStats(ctx, b.Host, "virt.vbox.vm.stop", "name="+b.VM, "mode=save"); err != nil {
		return fmt.Errorf("putting the VM %s back on %s: %v; it is still running, and virt.vbox.vm.stop with mode save puts it away", b.VM, b.Host.Device, err)
	}
	fmt.Fprintf(stdout, "%s: put the VM %s back on %s, its state saved\n", tool, b.VM, b.Host.Device)
	return nil
}

// startedMark is the line a prepare phase adds to its ready file when it
// started the box, so the test phase reading that file puts it back.
const startedMark = "started-box"

// markStarted adds startedMark to the ready file.
func markStarted(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- the ready file the caller named
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, startedMark); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// startedByPrepare reports whether the ready file path says its prepare
// phase started the box.
func startedByPrepare(path string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the ready file the caller named
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == startedMark {
			return true
		}
	}
	return false
}

// runWithBox runs flow against leg with the box up for it: started when it
// is not running, and put back when the run ends, interrupted or not, if
// this run (or the prepare phase whose ready file it was given) started
// it. A prepare phase leaves a box it started running for its test phase,
// and says so in its ready file.
func runWithBox(ctx context.Context, o *Options, flow Flow, leg Leg, box Box, stdout, stderr io.Writer) int {
	started := o.Phase == PhaseTest && startedByPrepare(o.ReadyFile)
	up, err := box.Up(ctx, flow.Tool, stdout)
	started = started || up
	code := 2
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n%s\n", flow.Tool, err, flow.Advice)
		rec := flow.record(o)
		stopped(&rec, StepBox, err)
		code = flow.writeRecord(o, rec, code, stderr)
	} else {
		code = flow.Run(ctx, o, stdout, stderr, leg)
	}
	if !started {
		return code
	}
	if o.Phase == PhasePrepare && code == 0 && o.ReadyFile != "" {
		if err := markStarted(o.ReadyFile); err == nil {
			return code
		}
	}
	if err := box.PutBack(flow.Tool, stdout); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", flow.Tool, err)
		if code == 0 {
			code = 2
		}
	}
	return code
}

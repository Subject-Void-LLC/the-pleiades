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
	// Wait is wait.connection's parameters for a box this run started:
	// SteadyWait when empty.
	Wait []string
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

// SteadyWait is how a box this run started is waited for, as any
// Pleiades user would wait for one: wait.connection, within bootWait, for
// three fresh logins in a row ten seconds apart, backing off exponentially
// from two seconds to thirty while it refuses. A win-check resumed from its
// saved state answered one login and then none for over 20 seconds
// (2026-10-18; its clock was 75 minutes slow until Guest Additions set it,
// two minutes after the resume), so one answer is not a box that is up.
// This was a loop of the check's own until wait.connection could ask for a
// streak.
var SteadyWait = []string{
	fmt.Sprintf("timeout=%d", int(bootWait/time.Second)), "backoff=exponential", "sleep=2", "max_sleep=30",
	"steady=3", "steady_interval=10", "connect_timeout=10",
}

// WindowsWait is SteadyWait held over two minutes: six fresh logins in a
// row, twenty seconds apart. Measured through Pleiades on 2026-10-19 and
// 2026-10-20, win-check resumed from its saved state missed connections from
// about 30 s to 90-130 s after virt.vbox.vm.start began at one vCPU (three
// resumes of three) and to 87 s at two (one of two), answering in between,
// so a 20-second streak could pass inside that window and the next step
// meet a host that did not answer. FreeBSD on the same host and network
// answered every probe from the start, and keeps SteadyWait.
var WindowsWait = []string{
	fmt.Sprintf("timeout=%d", int(bootWait/time.Second)), "backoff=exponential", "sleep=2", "max_sleep=30",
	"steady=6", "steady_interval=20", "connect_timeout=10",
}

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
	wait := b.Wait
	if len(wait) == 0 {
		wait = SteadyWait
	}
	if _, err := adhocStats(ctx, b.Device, "wait.connection", wait...); err != nil {
		return true, fmt.Errorf("the VM %s started, and %s did not answer logins steadily within %s: %w", b.VM, b.Device.Device, bootWait, err)
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

// A lab check's machine as a VirtualBox VM: started when a run needs it and
// put back afterwards, shut down, so a box holds the host's memory only
// while a check runs on it (the lab's bsd-check takes 4 GB). It is shut
// down rather than saved (owner, 2026-10-29): a saved VM's disks cannot be
// compacted, and its saved state holds the guest's whole memory on the
// host's disk. The next start is a cold boot, about two minutes at four
// CPUs, which bootWait allows for.
package oscheck

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmrun"
)

// Box is a lab machine's VM on a hypervisor host its project manages.
type Box struct {
	// Host drives the hypervisor host (a device with virtualbox: true, or a
	// Mac granted VMwareFusionCapable).
	Host Pleiades
	// Device drives the machine itself, to wait for it once started.
	Device Pleiades
	// VM is the machine's identity on the host: its name on VirtualBox, the
	// path of its vmx file on Fusion.
	VM string
	// Wait is wait.connection's parameters for a box this run started:
	// SteadyWait when empty.
	Wait []string
	// Methods are the methods that read, start and put back the VM:
	// VirtualBoxBox when unset.
	Methods BoxMethods
}

// BoxMethods are the Collection methods a box is driven through, which is all
// that differs between hypervisors: each reads the VM's state, starts it, and
// puts it back.
type BoxMethods struct {
	// Hypervisor names it in what a check prints ("VirtualBox").
	Hypervisor string
	// Info, Start and Stop are the methods' names.
	Info, Start, Stop string
	// Param is the parameter that names the VM.
	Param string
	// Running is the state Info reports for a running VM.
	Running string
	// PutBack are Stop's parameters that put the VM back: a shutdown through
	// the guest, or a suspend.
	PutBack []string
	// Fallback are Stop's parameters tried when PutBack fails, or none: a VM
	// whose guest did not shut down on its power button has its power cut,
	// since a check's VM holds nothing a cut loses but a run's own caches.
	Fallback []string
	// PutBackWord says how the VM was put back ("shut down", "suspended").
	PutBackWord string
	// CheckVM refuses a VM identity this hypervisor cannot have.
	CheckVM func(string) error
}

// ShutdownWait is how many seconds a VirtualBox box's guest gets to shut
// down on its power button before its power is cut: a FreeBSD or Windows
// Server guest with nothing running shuts down in well under a minute.
const ShutdownWait = 300

// VirtualBoxBox is a box on a VirtualBox host (virt.vbox.*), named by its VM
// name, shut down when put back (its power button, then its power cut).
var VirtualBoxBox = BoxMethods{
	Hypervisor: "VirtualBox", Info: "virt.vbox.vm.info", Start: "virt.vbox.vm.start", Stop: "virt.vbox.vm.stop",
	Param: "name", Running: "running",
	PutBack: []string{"mode=acpi", fmt.Sprintf("timeout=%d", ShutdownWait)}, Fallback: []string{"mode=poweroff"}, PutBackWord: "shut down",
	CheckVM: func(vm string) error {
		if !deviceName.MatchString(vm) {
			return fmt.Errorf("%q is not a VM name", vm)
		}
		return nil
	},
}

// FusionBox is a box on a Mac running VMware Fusion (virt.fusion.*), named by
// its vmx file, suspended when put back. Its start holds the Mac awake for the
// VM, without which macOS throttles a VM a check starts over SSH.
var FusionBox = BoxMethods{
	Hypervisor: "VMware Fusion", Info: "virt.fusion.vm.info", Start: "virt.fusion.vm.start", Stop: "virt.fusion.vm.stop",
	Param: "vmx", Running: "on", PutBack: []string{"mode=suspend"}, PutBackWord: "suspended",
	CheckVM: vmrun.CheckVMX,
}

// methods is b's methods, VirtualBox's when unset.
func (b Box) methods() BoxMethods {
	if b.Methods.Info == "" {
		return VirtualBoxBox
	}
	return b.Methods
}

// LabBox reads a lab check's VM settings: the VirtualBox host in the
// project, and the VM's name there, which is the device's when it is not
// set. No host is no box (empty host, nil error): the machine is up on its
// own and the check never starts or stops it.
func LabBox(s Settings, hostKey, vmKey, device string) (host, vm string, err error) {
	return LabBoxOf(s, hostKey, vmKey, device, VirtualBoxBox)
}

// LabBoxOf is LabBox for a box driven through m. A VirtualBox VM's name
// defaults to the device's; a Fusion VM's vmx path has no default.
func LabBoxOf(s Settings, hostKey, vmKey, device string, m BoxMethods) (host, vm string, err error) {
	if hostKey == "" {
		return "", "", nil
	}
	host, vm = s.Values[hostKey], s.Values[vmKey]
	switch {
	case host == "" && vm != "":
		return "", "", fmt.Errorf("%s names the VM on the %s host %s names; set %s too, or neither", vmKey, m.Hypervisor, hostKey, hostKey)
	case host == "":
		return "", "", nil
	case !deviceName.MatchString(host):
		return "", "", fmt.Errorf("%s=%q is not a device name", hostKey, host)
	case vm == "" && m.Param == VirtualBoxBox.Param:
		vm = device
	case vm == "":
		return "", "", fmt.Errorf("%s names the %s host, and %s, the VM's %s, is not set", hostKey, m.Hypervisor, vmKey, m.Param)
	}
	if err := m.CheckVM(vm); err != nil {
		return "", "", fmt.Errorf("%s: %v", vmKey, err)
	}
	return host, vm, nil
}

// bootWait bounds how long a started box may take to answer a login: a
// shut-down one boots cold (about two minutes at four CPUs, measured
// 2026-10-27; Windows on one CPU took a few).
const bootWait = 10 * time.Minute

// PutBackGrace bounds putting a box back, whatever ended the run, so a
// host that stopped answering cannot hold the check open: the guest's
// ShutdownWait, then the power cut and a read.
const PutBackGrace = time.Duration(ShutdownWait)*time.Second + 3*time.Minute

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
	m := b.methods()
	stats, err := adhocStats(ctx, b.Host, m.Info, m.Param+"="+b.VM)
	if err != nil {
		return false, fmt.Errorf("reading the VM %s on %s: %v", b.VM, b.Host.Device, err)
	}
	state, _ := stats[stateStat].(string)
	if state == m.Running {
		return false, nil
	}
	fmt.Fprintf(stdout, "%s: starting the VM %s on %s (it was %s); it is put back, %s, when the run ends\n", tool, b.VM, b.Host.Device, FirstOf(state, "in no state it named"), m.PutBackWord)
	if _, err := adhocStats(ctx, b.Host, m.Start, m.Param+"="+b.VM); err != nil {
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

// PutBack puts the box back (its methods' PutBack: a shutdown through the
// guest, or a suspend), which frees its memory on the host and, shut down,
// leaves its disks free to compact; a shutdown the guest did not finish is
// followed by the Fallback, its power cut. The next Up starts it again. It
// has a context of its own, bounded by PutBackGrace, since it runs after an
// interrupt too.
func (b Box) PutBack(tool string, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), PutBackGrace)
	defer cancel()
	m := b.methods()
	params := append([]string{m.Param + "=" + b.VM}, m.PutBack...)
	_, err := adhocStats(ctx, b.Host, m.Stop, params...)
	if err != nil && len(m.Fallback) > 0 {
		fmt.Fprintf(stdout, "%s: the VM %s on %s did not shut down on its own (%v); cutting its power, which loses nothing a check's VM keeps\n", tool, b.VM, b.Host.Device, err)
		params = append([]string{m.Param + "=" + b.VM}, m.Fallback...)
		_, err = adhocStats(ctx, b.Host, m.Stop, params...)
	}
	if err != nil {
		return fmt.Errorf("putting the VM %s back on %s: %v; it is still running, and %s with %s puts it away", b.VM, b.Host.Device, err, m.Stop, strings.Join(params[1:], " "))
	}
	fmt.Fprintf(stdout, "%s: put the VM %s back on %s, %s\n", tool, b.VM, b.Host.Device, m.PutBackWord)
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

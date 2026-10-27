// A check's machine when it is a device of a project that already manages
// it, as the VirtualBox lab's win-check and bsd-check are: the project and
// the device are two settings, and the check drives that device with this
// branch's pleiades, built privately, using the credential the project
// already holds, so no secret is copied anywhere.
package oscheck

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// deviceName is what a device setting must look like, since it reaches
// pleiades as an argument: no leading "-", which it would read as a flag.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// LabTarget reads a lab check's two settings: the project directory (a
// leading ~/ expanded) and the device in it. Neither set is no machine
// (empty project and device, nil error); one alone, a device name pleiades
// would misread, or a directory that is not a project is an error.
func LabTarget(s Settings, projectKey, deviceKey string) (project, device string, err error) {
	project, device = s.Values[projectKey], s.Values[deviceKey]
	switch {
	case project == "" && device == "":
		return "", "", nil
	case project == "" || device == "":
		return "", "", fmt.Errorf("%s and %s name the machine together; set both, or neither", projectKey, deviceKey)
	case !deviceName.MatchString(device):
		return "", "", fmt.Errorf("%s=%q is not a device name", deviceKey, device)
	}
	project = ExpandHome(project)
	if !FileExists(filepath.Join(project, "inventory.yaml")) {
		return "", "", fmt.Errorf("%s=%s is not a Pleiades project: it holds no inventory.yaml", projectKey, project)
	}
	return project, device, nil
}

// Lab is the part of a lab check every machine shares: its pleiades, and
// the liveness check.
type Lab struct {
	// Pleiades drives the device, from a binary under the worktree's own
	// git directory.
	Pleiades Pleiades
	// Target names the machine in what is printed.
	Target string
}

// NewLab is the lab machine device in project, driven by a pleiades built
// in tool's private directory.
func NewLab(tool, project, device string) (Lab, error) {
	dir, err := PrivateDir("pleiades-" + tool)
	if err != nil {
		return Lab{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Lab{}, err
	}
	return Lab{Pleiades: Pleiades{Binary: filepath.Join(dir, "pleiades"), Project: project, Device: device}, Target: device}, nil
}

// waitSeconds bounds the liveness check: wait.connection logs in, every
// five seconds, until it does or this many seconds pass.
const waitSeconds = 20

// Reachable asks wait.connection whether the device answers a login,
// building pleiades first when this worktree has none yet.
func (l Lab) Reachable() error {
	if !FileExists(l.Pleiades.Binary) {
		if err := l.Pleiades.Build(); err != nil {
			return err
		}
	}
	if _, err := l.Pleiades.Adhoc(context.Background(), "wait.connection", fmt.Sprintf("timeout=%d", waitSeconds), "connect_timeout=5", "sleep=5"); err != nil {
		return fmt.Errorf("%s (in %s) did not answer a login within %d seconds: %v\n  Is it running? A lab VM starts with virt.vbox.vm.start, run from its project", l.Target, l.Pleiades.Project, waitSeconds, err)
	}
	return nil
}

// LabCheck is a lab check's words for its machine and where its two
// settings come from.
type LabCheck struct {
	// Tool names the command ("bsdcheck").
	Tool string
	// Kind names the kind of machine ("FreeBSD machine").
	Kind string
	// OS names its operating system ("FreeBSD").
	OS string
	// EnvFile is the settings file, relative to the repository's top level.
	EnvFile string
	// ProjectKey and DeviceKey name the project and the device in it.
	ProjectKey, DeviceKey string
	// VMHostKey and VMKey, both optional, name the machine as a VirtualBox
	// VM: the host in the same project, and the VM's name there (the
	// device's when unset). With a host named, a run starts the VM when it
	// is not running and saves its state when the run ends (LabBox, Box).
	VMHostKey, VMKey string
	// BoxWait is how a VM this run started is waited for (Box.Wait):
	// SteadyWait when empty.
	BoxWait []string
	// Box is the hypervisor the VM is on: VirtualBoxBox when unset.
	Box BoxMethods
}

// boxMethods is c's hypervisor, VirtualBox's when unset.
func (c LabCheck) boxMethods() BoxMethods {
	if c.Box.Info == "" {
		return VirtualBoxBox
	}
	return c.Box
}

// keys is every setting c reads.
func (c LabCheck) keys() []string {
	keys := []string{c.ProjectKey, c.DeviceKey}
	if c.VMHostKey != "" {
		keys = append(keys, c.VMHostKey, c.VMKey)
	}
	return keys
}

// Run is a lab check's main, with its arguments, its output and the
// machine given (leg makes it from the project and device the settings
// name), so tests drive every step but the real device.
func (c LabCheck) Run(ctx context.Context, args []string, stdout, stderr io.Writer, leg func(project, device string) (Leg, error)) int {
	fs := flag.NewFlagSet(c.Tool, flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := Flags(fs, "the "+c.Kind)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o.Packages = fs.Args()
	if err := o.Check(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", c.Tool, err)
		return 2
	}
	s, err := Load(c.EnvFile, c.keys()...)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", c.Tool, err)
		return 2
	}
	advice := LabAdvice(s.File, c.EnvFile, c.ProjectKey, c.DeviceKey, c.Kind)
	project, device, err := LabTarget(s, c.ProjectKey, c.DeviceKey)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n%s\n", c.Tool, err, advice)
		return 2
	}
	if device == "" {
		if o.Optional {
			fmt.Fprintf(stdout, "%s: no %s configured (%s and %s in %s), so %s was NOT checked\n", c.Tool, c.Kind, c.ProjectKey, c.DeviceKey, c.EnvFile, c.OS)
			return RecordNotConfigured(o, c.Tool, c.OS, fmt.Sprintf("no %s configured (%s and %s in %s)", c.Kind, c.ProjectKey, c.DeviceKey, c.EnvFile), stderr)
		}
		fmt.Fprintf(stderr, "%[1]s: no %[2]s is configured. Name one, then run this again:\n  cp %[3]s.example %[3]s, and set %[4]s and %[5]s in it\n", c.Tool, c.Kind, c.EnvFile, c.ProjectKey, c.DeviceKey)
		return 2
	}
	host, vm, err := LabBoxOf(s, c.VMHostKey, c.VMKey, device, c.boxMethods())
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n%s\n", c.Tool, err, advice)
		return 2
	}
	l, err := leg(project, device)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", c.Tool, err)
		return 2
	}
	flow := Flow{Tool: c.Tool, Machine: "the " + c.Kind, OS: c.OS, Target: device, Advice: advice}
	if host == "" || len(o.Packages) == 0 {
		return flow.Run(ctx, o, stdout, stderr, l)
	}
	box, err := c.box(project, device, host, vm)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", c.Tool, err)
		return 2
	}
	return runWithBox(ctx, o, flow, l, box, stdout, stderr)
}

// buildPleiades builds p's binary from this tree; a test replaces it.
var buildPleiades = func(p Pleiades) error { return p.Build() }

// box is the VM vm on host, driven by the pleiades c builds for device in
// project from this tree, built first every time: a binary an earlier run
// left is another tree's, and once waited for a box with parameters it did
// not know (FAILURE_PATTERNS 526). go build makes an unchanged tree's in
// seconds.
func (c LabCheck) box(project, device, host, vm string) (Box, error) {
	lab, err := NewLab(c.Tool, project, device)
	if err != nil {
		return Box{}, err
	}
	if err := buildPleiades(lab.Pleiades); err != nil {
		return Box{}, err
	}
	onHost := lab.Pleiades
	onHost.Device = host
	return Box{Host: onHost, Device: lab.Pleiades, VM: vm, Wait: c.BoxWait, Methods: c.boxMethods()}, nil
}

// LabAdvice is what ends a lab check's refusals: how to point it at
// another machine, and how to stop using one. file is the settings file
// that named it, or "" when the environment did.
func LabAdvice(file, envFile, projectKey, deviceKey, machine string) string {
	if file == "" {
		return fmt.Sprintf("  To test on another %[1]s:   set %[2]s and %[3]s (a project that manages it, and its name there)\n"+
			"  To stop testing on a %[1]s: unset %[2]s and %[3]s, and delete %[4]s if one names a %[1]s", machine, projectKey, deviceKey, envFile)
	}
	return fmt.Sprintf("  To test on another %[1]s:   set %[2]s and %[3]s in %[4]s\n"+
		"  To stop testing on a %[1]s: delete %[4]s", machine, projectKey, deviceKey, file)
}

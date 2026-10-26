// Package vmrun drives VMware's vmrun and vmcli command-line tools on the host
// that runs the virtual machines: VMware Fusion on a Mac, or Workstation on
// Windows or Linux. One host layer serves both products, which differ by the
// -T value vmrun takes and by where the tools live, and nothing else here.
//
// A Host runs every command through a Runner, which a Collection builds from
// the device's SSH connection (SSHRunner). Commands are argument lists and
// never shell lines: the Runner quotes each argument for the host's login
// shell, so a VM path with a space in it ("/Users/me/Virtual Machines/vm.vmx")
// is one argument.
//
// Nothing here puts a secret on a command line. vmrun's guest commands take
// the guest's password as an argument, which any process on the host can read
// in the process list, so this package offers none that does.
package vmrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Product is which VMware product a host runs. It is the value of vmrun's -T
// flag, and it names the product a method targets, since the two share a tool
// but not a host: Fusion is a Mac's, Workstation is Windows' or Linux's.
type Product string

const (
	// Fusion is VMware Fusion on a Mac (vmrun -T fusion).
	Fusion Product = "fusion"
	// Workstation is VMware Workstation (vmrun -T ws).
	Workstation Product = "ws"
)

// Valid reports whether p is a product this package drives. vmrun itself
// accepts any -T value without complaint, so the check is here.
func (p Product) Valid() bool { return p == Fusion || p == Workstation }

// Where Fusion keeps its tools on a Mac. vmrun and vmcli are in the app's
// Public directory, which is the one VMware documents for them.
const (
	FusionVMRun = "/Applications/VMware Fusion.app/Contents/Public/vmrun"
	FusionVMCLI = "/Applications/VMware Fusion.app/Contents/Public/vmcli"
)

// Output is what a command printed and how it ended.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs one command on the host to completion. argv[0] is the program;
// the rest are its arguments, passed as they are. A non-zero exit is an
// Output with that ExitCode, not an error; an error means the command could
// not be run at all.
type Runner interface {
	Run(ctx context.Context, argv []string) (Output, error)
}

// Host is a machine running VMware, and the tools to drive it.
type Host struct {
	// Runner runs commands on the machine.
	Runner Runner
	// Product is the VMware product the machine runs.
	Product Product
	// VMRun and VMCLI are the absolute paths of the two tools.
	VMRun, VMCLI string
}

// Sentinels a caller tests with errors.Is. An *Error wraps one when the tool's
// own message says which.
var (
	// ErrNotFound is a VM or snapshot that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotRunning is a command that needs a running VM, given one that is not.
	ErrNotRunning = errors.New("not running")
	// ErrExists is a snapshot or VM that already exists.
	ErrExists = errors.New("already exists")
	// ErrNoTools is a command that needs VMware Tools in the guest (a soft
	// stop or reset, the guest's address, any guest operation) given a guest
	// where the tools are not running.
	ErrNoTools = errors.New("vmware tools are not running")
)

// Error is a tool that ran and reported a failure. Message is the tool's own
// line (vmrun prints "Error: ..."), with its prefix removed.
type Error struct {
	// Op names the operation ("start", "snapshot").
	Op string
	// Message is what the tool said.
	Message string
	// ExitCode is the tool's exit status.
	ExitCode int
	kind     error
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("vmware %s failed (exit %d): %s", e.Op, e.ExitCode, e.Message)
}

// Unwrap lets errors.Is find the sentinel the message mapped to.
func (e *Error) Unwrap() error { return e.kind }

// classify maps a tool's message to the sentinel it means, or nil. The
// phrases are vmrun 1.17's own, captured from a real Fusion
// (testdata/vmrun-errors.txt).
func classify(msg string) error {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "not powered on"):
		return ErrNotRunning
	case strings.Contains(m, "tools are not running"):
		return ErrNoTools
	case strings.Contains(m, "already exists"):
		return ErrExists
	case strings.Contains(m, "cannot be found"), strings.Contains(m, "does not exist"):
		return ErrNotFound
	}
	return nil
}

// failure builds the *Error for a command that exited non-zero: the tool's
// first line saying Error, or else its first non-empty line.
func failure(op string, out Output) *Error {
	msg := firstLine(out.Stdout+"\n"+out.Stderr, "Error:")
	if msg == "" {
		msg = firstLine(out.Stdout+"\n"+out.Stderr, "")
	}
	msg = strings.TrimSpace(strings.TrimPrefix(msg, "Error:"))
	return &Error{Op: op, Message: msg, ExitCode: out.ExitCode, kind: classify(msg)}
}

// firstLine is the first line of s that starts with prefix after trimming
// ("" matches any non-empty line), or "" when there is none.
func firstLine(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// run runs program with args and returns its output, or an *Error when it
// exits non-zero. op names the operation in the error.
func (h Host) run(ctx context.Context, op, program string, args ...string) (Output, error) {
	if h.Runner == nil {
		return Output{}, errors.New("vmrun: the host has no runner")
	}
	out, err := h.Runner.Run(ctx, append([]string{program}, args...))
	if err != nil {
		return out, fmt.Errorf("running %s: %w", op, err)
	}
	if out.ExitCode != 0 {
		return out, failure(op, out)
	}
	return out, nil
}

// vmrun runs a vmrun command: the product flag first, as vmrun requires its
// authentication flags before the command.
func (h Host) vmrun(ctx context.Context, op string, args ...string) (Output, error) {
	if !h.Product.Valid() {
		return Output{}, fmt.Errorf("vmrun: %q is not a VMware product this package drives (fusion or ws)", h.Product)
	}
	if err := CheckTool(h.VMRun); err != nil {
		return Output{}, fmt.Errorf("vmrun path: %w", err)
	}
	return h.run(ctx, op, h.VMRun, append([]string{"-T", string(h.Product)}, args...)...)
}

// vmcli runs a vmcli command, with the VM's vmx first when vmx is not "".
func (h Host) vmcli(ctx context.Context, op, vmx string, args ...string) (Output, error) {
	if err := CheckTool(h.VMCLI); err != nil {
		return Output{}, fmt.Errorf("vmcli path: %w", err)
	}
	if vmx != "" {
		if err := CheckVMX(vmx); err != nil {
			return Output{}, err
		}
		args = append([]string{vmx}, args...)
	}
	return h.run(ctx, op, h.VMCLI, args...)
}

// CheckTool refuses a tool path that is not absolute, that holds a control
// character, or that is empty. A space is allowed: Fusion's own path has two.
func CheckTool(path string) error {
	if path == "" || path[0] != '/' {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	return checkText("path", path, 1024)
}

// CheckVMX refuses a VM path that is not an absolute path to a .vmx file, or
// that holds a control character. It is the VM's identity to every command, so
// it is checked before any runs.
func CheckVMX(path string) error {
	if path == "" || path[0] != '/' {
		return fmt.Errorf("vm path %q is not an absolute path", path)
	}
	if !strings.HasSuffix(path, ".vmx") {
		return fmt.Errorf("vm path %q does not name a .vmx file", path)
	}
	return checkText("vm path", path, 1024)
}

// CheckName refuses a VM name that cannot be a file name: it begins with a
// letter or digit and holds letters, digits, dot, underscore and hyphen, at
// most 63 characters. kind names what is being checked in the error.
func CheckName(kind, name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("%s %q must be 1 to 63 characters", kind, name)
	}
	for i, r := range name {
		letter := r < unicode.MaxASCII && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		if !(letter || i > 0 && (r == '.' || r == '_' || r == '-')) {
			return fmt.Errorf("%s %q must begin with a letter or digit and hold only letters, digits, dot, underscore and hyphen", kind, name)
		}
	}
	return nil
}

// CheckSnapshotName refuses a snapshot name that begins with a hyphen (a tool
// would read it as an option), that holds a control character, or that is
// empty or over 128 characters. Spaces are allowed: a snapshot is named in
// the product's own window with any text.
func CheckSnapshotName(name string) error {
	if name == "" || len(name) > 128 || name[0] == '-' {
		return fmt.Errorf("snapshot name %q must be 1 to 128 characters and not begin with a hyphen", name)
	}
	return checkText("snapshot name", name, 128)
}

// checkText refuses text over max bytes or holding a control or format
// character, which a login shell, a log or a terminal would act on.
func checkText(what, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%s is %d bytes, over the limit of %d", what, len(s), max)
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return fmt.Errorf("%s %q holds the character %U, which is not allowed", what, s, r)
		}
	}
	return nil
}

// Package vboxmanage runs Oracle VirtualBox's VBoxManage on a host and
// reads what it answers: machine state, snapshots, and the errors that
// mean "no such machine" or "not running" rather than a failure.
//
// It does not know how the host is reached. A Runner does that: over
// WinRM for a Windows host (WinRMRunner), and over SSH for a Linux, macOS
// or Solaris host when one is added. Every call is a program and an
// argument vector, never a shell line, and every VM and snapshot name is
// checked against a strict character set before it is sent, so no name
// can become syntax on the host however it is reached.
package vboxmanage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Output is what one VBoxManage run produced.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs program with args on the VirtualBox host, each argument
// reaching the program exactly as given. A non-zero exit is an Output,
// not an error: an error means the program could not be run at all.
type Runner interface {
	Run(ctx context.Context, program string, args []string) (Output, error)
	// PowerShell runs script in Windows PowerShell on the host, with
	// stdin written to its standard input and never to a command line.
	PowerShell(ctx context.Context, script, stdin string) (Output, error)
}

// Host is a VirtualBox host: how to reach it, and where VBoxManage is.
type Host struct {
	Runner Runner
	// Path is VBoxManage's absolute path on the host.
	Path string
}

// ErrNotFound is wrapped by an error for a machine or snapshot that does
// not exist.
var ErrNotFound = errors.New("not found")

// ErrNotRunning is wrapped by an error for a machine that is not running
// when an operation needs it to be.
var ErrNotRunning = errors.New("not running")

// ErrLocked is wrapped by an error for a machine or medium another
// VirtualBox client or task held a lock on at that moment, such as a
// screenshot being taken or a DVD just ejected: it can be tried again.
var ErrLocked = errors.New("locked")

// Error is a VBoxManage run that exited non-zero.
type Error struct {
	// Args is what VBoxManage was asked, which never holds a secret: no
	// call in this package passes one on the command line.
	Args []string
	// Output is what it answered.
	Output Output
	// kind is ErrNotFound, ErrNotRunning, ErrLocked, or nil.
	kind error
}

// Error names the command and quotes VBoxManage's own first error line,
// which is the sentence an operator can act on.
func (e *Error) Error() string {
	return fmt.Sprintf("VBoxManage %s exited %d: %s", strings.Join(e.Args, " "), e.Output.ExitCode, firstErrorLine(e.Output))
}

// Unwrap lets errors.Is find ErrNotFound, ErrNotRunning or ErrLocked.
func (e *Error) Unwrap() error { return e.kind }

// errorLine is how VBoxManage starts each line of an error report.
var errorLine = regexp.MustCompile(`(?m)^VBoxManage(?:\.exe)?: error: (.*?)\r?$`)

// firstErrorLine returns VBoxManage's first error sentence, or the
// output's first non-empty line when there is none.
func firstErrorLine(out Output) string {
	if m := errorLine.FindStringSubmatch(out.Stderr); m != nil {
		return m[1]
	}
	for _, text := range []string{out.Stderr, out.Stdout} {
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return "no output"
}

// classify returns the kind of a failed run, from the codes and sentences
// VBoxManage writes for each, as captured from VirtualBox 7.2 on Windows.
func classify(out Output) error {
	text := out.Stderr + out.Stdout
	switch {
	case strings.Contains(text, "VBOX_E_OBJECT_NOT_FOUND"),
		strings.Contains(text, "Could not find a registered machine"),
		strings.Contains(text, "Could not find a snapshot"):
		return ErrNotFound
	case strings.Contains(text, "is not currently running"):
		return ErrNotRunning
	case strings.Contains(text, "already has a lock request pending"),
		strings.Contains(text, "is already locked for a session"),
		strings.Contains(text, "is locked for reading by another task"),
		strings.Contains(text, "is locked for writing by another task"):
		return ErrLocked
	}
	return nil
}

// run runs VBoxManage with args, returning its output, or an *Error when
// it exits non-zero.
func (h Host) run(ctx context.Context, args ...string) (Output, error) {
	if h.Runner == nil || h.Path == "" {
		return Output{}, errors.New("vboxmanage: the host has no runner or no VBoxManage path")
	}
	out, err := h.Runner.Run(ctx, h.Path, args)
	if err != nil {
		return Output{}, fmt.Errorf("vboxmanage: running VBoxManage %s: %w", strings.Join(args, " "), err)
	}
	if out.ExitCode != 0 {
		return out, &Error{Args: args, Output: out, kind: classify(out)}
	}
	return out, nil
}

// namePattern is every VM or snapshot name this package sends. A VM's
// name becomes its folder's and settings file's name, so the set is what
// is safe as a file name on every host VirtualBox runs on, and what no
// command line on any of them treats as syntax.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// CheckName refuses a name outside namePattern, naming kind ("VM",
// "snapshot") and the rule. A name is refused rather than quoted: nothing
// a user needs to call a VM requires more.
func CheckName(kind, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters", kind, name)
	}
	return nil
}

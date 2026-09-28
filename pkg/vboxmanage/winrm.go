// Reaching a Windows VirtualBox host over WinRM.
package vboxmanage

import (
	"context"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// WinRMRunner runs VBoxManage on a Windows host over WinRM, with no shell
// reading the command: winrmexec.CommandLine quotes each argument for the
// standard Windows argument parser VBoxManage uses, and the none mode
// escapes the line for the cmd.exe the WinRM service puts in front of it.
type WinRMRunner struct {
	Target  winrmexec.Target
	Auth    winrmexec.Auth
	Options winrmexec.Options
}

// Run implements Runner.
func (r WinRMRunner) Run(ctx context.Context, program string, args []string) (Output, error) {
	line, err := winrmexec.CommandLine(program, args...)
	if err != nil {
		return Output{}, err
	}
	res, err := winrmexec.Execute(ctx, r.Target, r.Auth, winrmexec.Command{Shell: winrmexec.ShellNone, Script: line}, r.Options)
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, nil
}

// WithTimeout implements TimeoutRunner.
func (r WinRMRunner) WithTimeout(timeout time.Duration) Runner {
	r.Options.Timeout = timeout
	return r
}

// PowerShell implements Runner.
func (r WinRMRunner) PowerShell(ctx context.Context, script, stdin string) (Output, error) {
	res, err := winrmexec.RunWithStdin(ctx, r.Target, r.Auth, winrmexec.ShellPowerShell, script, stdin, r.Options)
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, nil
}

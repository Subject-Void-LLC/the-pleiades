// Package vmrun: running the tools over the SSH connection a Collection method
// holds to the VMware host.
package vmrun

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// SSHRunner is a Runner over an SSH connection to the host, the connection a
// Collection method gets from sdk.Connect. It sends each command as one
// quoted line for the host's login shell, so no argument is split or expanded,
// and reads at most remoteexec.DefaultOutputLimit bytes from each stream.
type SSHRunner struct {
	// Conn is the open connection. It is not safe for concurrent use, and the
	// caller closes it.
	Conn *remoteexec.Conn
}

// Run implements Runner.
func (r SSHRunner) Run(ctx context.Context, argv []string) (Output, error) {
	if r.Conn == nil {
		return Output{}, errors.New("vmrun: no SSH connection")
	}
	if len(argv) == 0 || argv[0] == "" {
		return Output{}, errors.New("vmrun: no program to run")
	}
	res, err := r.Conn.Run(ctx, remoteexec.QuoteCommand(argv))
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, nil
}

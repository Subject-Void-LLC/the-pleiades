package sdk

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// ParamInsecureSkipHostKeyVerify is the per-task escape hatch from host
// key verification, off by default.
//
// It is a task parameter rather than a setting on purpose. Skipping the
// check is a real exposure to a machine in the middle, and a runbook
// parameter sits next to the command it applies to where review can see
// it, while a deployment setting is set once and forgotten. There is
// deliberately no environment variable or chart value that turns
// verification off; PLEIADES_KNOWN_HOSTS configures where the host keys
// ARE, never whether they are checked.
const ParamInsecureSkipHostKeyVerify = "insecure_skip_host_key_verify"

// Connect opens one SSH connection to the device a task targets. The
// caller must Close the returned connection.
//
// It returns a connection rather than running one command because a
// converging method needs several: a probe that reads the current state,
// then the command that changes it, then sometimes a re-read. Paying for
// a fresh TCP connect, key exchange and authentication round for each
// would make reading the state the expensive part of the task.
//
// fqcn is the caller's own method name, used only to prefix errors, so
// an operator reading a failure knows which task produced it.
func Connect(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*remoteexec.Conn, error) {
	return connect(ctx, rc, device, params, fqcn, 0)
}

// ConnectPort is Connect against an explicit TCP port rather than the
// device's declared SSH port.
//
// It exists for the protocols that ride an SSH connection but do not
// live on the SSH port. NETCONF is the first: it is a subsystem on an
// ordinary SSH connection, so port 22 SHOULD serve it, and a real Cisco
// IOS XE device accepts the subsystem request on 22 and then
// immediately ends the channel, serving NETCONF only on 830. The port a
// protocol is reached on is therefore the protocol's own capability to
// declare (capability.NetconfCapable's NetconfPort), not something
// derivable from SSHPort.
//
// A port of 0 means "use the device's declared SSH port", which is what
// Connect passes.
func ConnectPort(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, port int) (*remoteexec.Conn, error) {
	return connect(ctx, rc, device, params, fqcn, port)
}

func connect(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, port int) (*remoteexec.Conn, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", fqcn)
	}

	sshDev, ok := device.(capability.SSHTransportCapable)
	if !ok {
		return nil, fmt.Errorf("%s: device %q is not reachable over SSH (it does not implement %s)",
			fqcn, device.Name(), capability.NameSSHTransport)
	}
	if port == 0 {
		port = sshDev.SSHPort()
	}

	// Credentials arrive through InjectSecrets rather than through params,
	// because params come from the runbook file and a runbook file is
	// committed to version control.
	auth, err := remoteexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}

	// Shared rather than New: a Collection method is invoked once per task
	// with nowhere to keep a Runner in between, so a fresh one every time
	// would carry a circuit breaker that has never seen a failure and
	// could therefore never open.
	runner := remoteexec.Shared(remoteexec.Options{
		InsecureSkipHostKeyVerify: BoolParam(params, ParamInsecureSkipHostKeyVerify),
	})

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: sshDev.SSHHost(), Port: port}, auth)
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	return conn, nil
}

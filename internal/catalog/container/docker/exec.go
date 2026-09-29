package docker

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/dockerexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file implements "container.docker.exec", Phase 73's Workstream F.
//
// # Why this is a Collection method, not a generic TransportBinding-dispatched fqcn
//
// Every other exec-shaped transport this phase adds (serial_exec,
// serialtcp_exec, telnet_exec) goes through engine.TransportBinding,
// whose Target func(inventory.InventoryItem) (transport.Target, bool)
// resolves everything it needs from the device alone. Docker exec cannot
// fit that shape: WHICH container to reach is a per-task detail, exactly
// like container.docker.run/stop/remove's own paramName, not a
// device-level property -- DockerCapable only ever advertises the
// daemon's own socket (internal/inventory/devices/container's own doc
// comment says so explicitly), because one Docker host legitimately runs
// many containers. TransportBinding.Target has no access to a task's
// params at all, so it structurally cannot resolve a container id; a
// Collection method's Invoke(ctx, rc, device, params) already receives
// params directly, the same way run/stop/remove already read paramName.
// This method therefore imports pkg/dockerexec directly and calls it
// itself, exactly as run/stop/remove already import pkg/remoteexec
// directly, rather than going through internal/transport or
// engine.TransportBinding at all.
//
// # A second real value for SupportedTransports
//
// run/stop/remove's own SupportedTransports: ["ssh"] stays exactly as it
// is -- an exec-only Docker client deliberately cannot create, stop, or
// remove a container, so those three still need a shell on the host, and
// "ssh" already says that correctly. "docker" here is what the plan
// anticipated as this phase's second legitimate value: a method that
// reaches the daemon socket directly instead.
const paramCmd = "cmd"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.docker.exec",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"docker"},
			RequiredCapabilities: []capability.Name{capability.NameDocker},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "An arbitrary command's effect inside a container is unknown to this platform, the same " +
					"reasoning exec.command and exec.shell already record for the identical shape over SSH.",
			},
			NoCheckReason: "a command run inside a container can change anything the container can reach, and what it " +
				"changes is known only once it has run; this method has no creates or removes guard for a check to read",
			Doc: execDoc(),
		},
		Invoke: Exec,
	})
}

func execDoc() collection.Doc {
	return collection.Doc{
		Summary: "Runs one command inside a running Docker container, reached directly through the daemon socket.",
		Description: "Runs cmd inside the container named name, through /bin/sh -c on the daemon's own exec " +
			"endpoints (pkg/dockerexec), never over SSH. The container must already be running; this method " +
			"does not start one (see container.docker.run). Reports the command's real exit code, stdout and " +
			"stderr. A non-zero exit is an error, not a result to inspect, the same line exec.command draws.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The running container to exec into."},
			{Name: paramCmd, Type: "string", Required: true, Description: "The command line, run inside the container's own /bin/sh -c. Pipes, redirects and quoting all work, because the container's shell sees them.", Format: collection.ParamFormatCommand},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The container this task acted on."},
			{Name: statExitCode, Type: "int", Returned: "always", Description: "The command's real exit status, reported by the Docker daemon."},
			{Name: statStdout, Type: "string", Returned: "always", Description: "Everything the command wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "always", Description: "Everything the command wrote to standard error."},
		},
		Examples: []collection.Example{
			{
				Name:        "Check a running container's own view of a file",
				RunbookYAML: "- name: Read the app's version file\n  container.docker.exec:\n    name: web\n    cmd: cat /opt/app/VERSION\n  register: version\n",
			},
		},
		SeeAlso: []string{"container.docker.run", "container.docker.stop", "container.docker.remove", "exec.shell"},
	}
}

const (
	statExitCode = "exit_code"
	statStdout   = "stdout"
	statStderr   = "stderr"
)

// Exec implements "container.docker.exec": it runs one command inside
// one already-running container, reached directly through the target's
// own Docker daemon socket (capability.DockerCapable), never over SSH.
//
// A non-zero exit status is an error, not a result to inspect, exactly
// the line exec.command draws for the identical shape over SSH: the
// command reached the container and reported failure, and a task whose
// command failed has failed. The exit code, stdout and stderr are
// recorded as stats before the error is returned, the same order
// Command's own doc comment states the reasoning for.
func Exec(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.docker.exec"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	cmd, err := sdk.RequiredStringParam(params, paramCmd)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	socket, err := dockerSocket(device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	result, err := dockerexec.Exec(ctx, string(socket), name, dockerexec.Options{}, cmd)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statExitCode, result.ExitCode); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statStdout, result.Stdout); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statStderr, result.Stderr); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if result.ExitCode != 0 {
		return collection.Result{}, fmt.Errorf("%s: command exited %d\nstdout: %s\nstderr: %s", fqcn, result.ExitCode, result.Stdout, result.Stderr)
	}

	// Changed defaults to true, the same call exec.command's own doc
	// comment explains: an arbitrary command inside a container cannot
	// be inspected, so this platform cannot know whether it altered
	// anything. Unlike exec.command, there is no creates/removes escape
	// hatch here yet -- narrower scope for a first cut, not an oversight.
	return collection.Result{Changed: true}, nil
}

// dockerSocket resolves device's own Docker daemon socket address,
// mirroring net/catalyst's own clientForDevice: HasCapability first
// (the real structural guarantee), then the type assertion (defensive;
// unreachable through a correctly built device type, since HasCapability
// already ANDs the structural check).
func dockerSocket(device inventory.InventoryItem) (capability.SocketAddress, error) {
	if device == nil {
		return "", fmt.Errorf("no target device: container.docker.exec addresses a specific Docker host, set the task's target")
	}
	if !device.HasCapability(capability.NameDocker) {
		return "", fmt.Errorf("device %q does not have %s", device.Name(), capability.NameDocker)
	}
	dockerDev, ok := device.(capability.DockerCapable)
	if !ok {
		return "", fmt.Errorf("device %q declares %s but does not implement it", device.Name(), capability.NameDocker)
	}
	return dockerDev.DockerEndpoint(), nil
}

// Shell selection for the transport actions: the one dispatch-logic
// change the WinRM transport needed (Phase 75).
package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// EnvPrefix is sdk.EnvPrefix, named here for the engine's own callers
// and tests.
const EnvPrefix = sdk.EnvPrefix

// execCommand runs command on target through t.
//
// A transport that is not a transport.ShellTransport is called through
// Exec exactly as every transport was before Phase 75, and a task naming
// a shell or passing params.env against it is refused, naming the device
// and what it asked for, rather than running the command some other way.
// A ShellTransport is always called through ExecShell, with ShellNone
// when the task names no shell, so the device's own working directory and
// interpreter paths reach it either way.
func execCommand(ctx context.Context, t transport.Transport, target transport.Target, cred credential.Credential, device inventory.InventoryItem, task *Task, command string) (transport.Result, error) {
	shell, err := taskShell(task)
	if err != nil {
		return transport.Result{}, err
	}
	env, err := taskEnv(task)
	if err != nil {
		return transport.Result{}, err
	}

	st, ok := t.(transport.ShellTransport)
	if !ok {
		if shell != transport.ShellNone {
			return transport.Result{}, fmt.Errorf("fqcn %q on device %q: params.shell %q is not available: this device's transport runs a command directly and has no shell to choose", task.FQCN, device.Name(), shell)
		}
		if len(env) > 0 {
			return transport.Result{}, fmt.Errorf("fqcn %q on device %q: params.env is not available: this device's transport cannot set a command's environment", task.FQCN, device.Name())
		}
		return t.Exec(ctx, target, cred, command)
	}

	req := transport.ShellRequest{Shell: shell, Script: command, Env: env}
	if dev, ok := device.(capability.WindowsShellCapable); ok {
		req.WorkingDirectory = dev.WorkingDirectory()
		switch shell {
		case transport.ShellCmd:
			req.Interpreter = dev.CmdPath()
		case transport.ShellPowerShell:
			req.Interpreter = dev.PowerShellPath()
		}
	}
	return st.ExecShell(ctx, target, cred, req)
}

// taskShell reads params.shell, which must be a string when present.
func taskShell(task *Task) (transport.Shell, error) {
	raw, present := task.Params["shell"]
	if !present || raw == nil {
		return transport.ShellNone, nil
	}
	token, ok := raw.(string)
	if !ok {
		return transport.ShellNone, fmt.Errorf("fqcn %q: params.shell must be a string, got %T", task.FQCN, raw)
	}
	shell, err := transport.ParseShell(token)
	if err != nil {
		return transport.ShellNone, fmt.Errorf("fqcn %q: params.shell: %w", task.FQCN, err)
	}
	return shell, nil
}

// taskEnv reads params.env through sdk.EnvParam, the same reader
// exec.winrm.shell uses, so one runbook value is spelled the same way in
// both.
func taskEnv(task *Task) (map[string]string, error) {
	env, err := sdk.EnvParam(task.Params, "env")
	if err != nil {
		return nil, fmt.Errorf("fqcn %q: %w", task.FQCN, err)
	}
	return env, nil
}

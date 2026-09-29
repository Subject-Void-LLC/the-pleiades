// The "wait.connection" method: waiting until a device answers a command
// over the connection its other tasks use, as Ansible's
// wait_for_connection does.
//
// It is its own loop rather than wait.path's and wait.search's waitPoll,
// because the two treat a failed look the other way round: a probe error
// ends their wait, since a connection that failed will not start holding
// a file by being asked again, while a failed login is exactly what this
// method waits to see stop happening.
package wait

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "wait.connection",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetworkAddressable,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// It observes and never acts, so a rollback has nothing to move
			// back, and it never emits an inverse.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "This method waits for a device to answer and changes nothing on it, so there is nothing to undo.",
				ReadOnly:   true,
			},
			// Only reads, and still not checkable: see NoCheckReason.
			NoCheckReason: "what it waits for is usually a machine an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: collection.Doc{
				Summary:     "Waits until the target answers a command over its own connection, SSH or WinRM.",
				Description: "Tries the device over the connection its other tasks use (WinRM for a device reached that way, such as a Windows server, and SSH for any other) until a command runs there, and fails when the timeout runs out first. It is Ansible's wait_for_connection: the task to put after one that starts or restarts a machine, before the tasks that need it. Each try logs in with the device's credential and runs a command that does nothing, so a machine whose port answers before its account can log in is not taken for ready. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay is spent out of the timeout rather than added to it, as wait_for_connection does. It changes nothing.",
				Params: []collection.Param{
					{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
					{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
					{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries. Must be more than 0."},
					{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take before it counts as failed. Must be more than 0."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, on a device reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
					{Name: "transport", Type: "string", Returned: "always", Description: "The connection that answered: ssh or winrm."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The connection that answered. Both halves are identical, since a wait changes nothing."},
				},
				Examples: []collection.Example{
					{Name: "Wait for a new Windows VM's first boot", RunbookYAML: "- name: Wait for the Windows lab VM to let its Administrator in\n  wait.connection:\n    timeout: 1800\n    sleep: 15\n"},
					{Name: "Wait out a reboot", RunbookYAML: "- name: Give the machine time to go down, then wait for it\n  wait.connection:\n    delay: 30\n    timeout: 600\n"},
				},
				SeeAlso: []string{"wait.path", "virt.vbox.vm.start", "pleiades.builtin.wait.port"},
			},
		},
		Invoke: Connection,
	})
}

// The connection wait's parameters and stats beyond wait_for's own.
const (
	connectionParamConnectTimeout = "connect_timeout"
	connectionStatTransport       = "transport"
)

// The connections a device can be tried over.
const (
	transportSSH   = "ssh"
	transportWinRM = "winrm"
)

// connectionRequest is one connection wait's validated timing.
type connectionRequest struct {
	timeout, delay, sleep, perTry time.Duration
}

// readConnection reads and checks the timing parameters.
func readConnection(params map[string]any) (connectionRequest, error) {
	var r connectionRequest
	for _, p := range []struct {
		key      string
		fallback int
		zero     bool
		into     *time.Duration
	}{
		{waitParamTimeout, 600, false, &r.timeout},
		{waitParamDelay, 0, true, &r.delay},
		{waitParamSleep, 5, false, &r.sleep},
		{connectionParamConnectTimeout, 20, false, &r.perTry},
	} {
		n, set, err := sdk.IntParam(params, p.key)
		if err != nil {
			return r, err
		}
		if !set {
			n = p.fallback
		}
		if n < 0 || (n == 0 && !p.zero) {
			return r, fmt.Errorf("%s must be more than 0, not %d", p.key, n)
		}
		*p.into = time.Duration(n) * time.Second
	}
	if r.delay >= r.timeout {
		return r, fmt.Errorf("delay (%s) must be shorter than timeout (%s), which it is spent out of", r.delay, r.timeout)
	}
	return r, nil
}

// Connection implements "wait.connection".
func Connection(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "wait.connection"
	req, err := readConnection(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	try, transport, err := connectionProbe(rc, device, params, fqcn, req.perTry)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	elapsed, err := waitForConnection(ctx, req, try)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s did not answer over %s: %w", fqcn, device.Name(), transport, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{connectionStatTransport: transport})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(connectionStatTransport, transport); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(waitStatElapsed, int(elapsed.Seconds())); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: false}, nil
}

// waitForConnection tries until try succeeds, each try bounded by
// req.perTry, and returns how long that took. It gives up at the
// timeout, quoting the last try's failure.
func waitForConnection(ctx context.Context, req connectionRequest, try func(context.Context) error) (time.Duration, error) {
	start := time.Now()
	deadline := start.Add(req.timeout)
	interval := req.delay
	for {
		if interval > 0 {
			if err := waitSleep(ctx, interval); err != nil {
				return 0, err
			}
		}
		tryCtx, cancel := context.WithTimeout(ctx, req.perTry)
		last := try(tryCtx)
		cancel()
		if last == nil {
			return time.Since(start), nil
		}
		// A run torn down during the try is seen by the sleep below.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, fmt.Errorf("timed out after %s; the last try said: %w", req.timeout, last)
		}
		interval = min(req.sleep, remaining)
	}
}

// connectionProbe returns one try at device over the connection its other
// tasks use, WinRM for a device reached that way and SSH otherwise, and
// the name of that connection. A try logs in and runs a command that does
// nothing, so a port that answers before the account can log in is not
// taken for a ready machine.
func connectionProbe(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, perTry time.Duration) (func(context.Context) error, string, error) {
	if device == nil {
		return nil, "", fmt.Errorf("needs a target device")
	}
	if winrm, ok := device.(capability.WinRMCapable); ok && device.HasCapability(capability.NameWinRM) {
		auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
		if err != nil {
			return nil, "", err
		}
		target := winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}
		opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: perTry}, devicetls.For(device))
		return func(ctx context.Context) error { return winrmexec.Reachable(ctx, target, auth, opts) }, transportWinRM, nil
	}
	if _, ok := device.(capability.SSHTransportCapable); ok && device.HasCapability(capability.NameSSHTransport) {
		return func(ctx context.Context) error { return sshTry(ctx, rc, device, params, fqcn) }, transportSSH, nil
	}
	return nil, "", fmt.Errorf("device %q is reached over neither SSH nor WinRM", device.Name())
}

// sshTry logs in over SSH and runs a command that does nothing. A command
// that ran, whatever it answered, shows the machine lets the account in.
func sshTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) error {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() // a try's connection has nothing left to report once it answered
	_, err = conn.Run(ctx, "exit 0")
	return err
}

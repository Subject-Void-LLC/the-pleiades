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
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
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
			NoBecomeReason: "its question is whether this login gets in, and a sudo in front of it would answer whether the login may become another account",
			// Only reads, and still not checkable: see NoCheckReason.
			NoCheckReason: "what it waits for is usually a machine an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: collection.Doc{
				Summary:     "Waits until the target answers a command over its own connection, SSH or WinRM.",
				Description: "Tries the device over the connection its other tasks use (WinRM for a device reached that way, such as a Windows server, and SSH for any other) until a command runs there, and fails when the timeout runs out first. It is Ansible's wait_for_connection: the task to put after one that starts or restarts a machine, before the tasks that need it. Each try logs in afresh with the device's credential and runs a command that does nothing, so a machine whose port answers before its account can log in is not taken for ready, and a session kept open from an earlier task is never taken for a login. A network device's CLI is not a shell with a command that does nothing, so for one that declares CiscoIOSCapable, or a cli_prompt, a try opens its terminal and waits for its prompt instead. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay is spent out of the timeout rather than added to it, as wait_for_connection does. With backoff: exponential the gap after each failed try doubles from sleep up to max_sleep, each gap a random point in its upper half so machines restarted together do not try in step. With steady above 1 the machine must answer that many tries in a row, steady_interval apart, which a machine just resumed or restarted that answers once and then refuses for a while does not; a refusal mid-streak starts the streak, and the backoff, again. It changes nothing.",
				Params: []collection.Param{
					{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
					{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
					{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0."},
					{Name: "backoff", Type: "string", Default: "fixed", Choices: []string{"fixed", "exponential"}, Description: "How the gap between failed tries grows: fixed keeps it at sleep, as wait_for_connection does; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half."},
					{Name: "max_sleep", Type: "int", Default: "60", Description: "With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing."},
					{Name: "steady", Type: "int", Default: "1", Description: "How many tries in a row must answer before the machine counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay."},
					{Name: "steady_interval", Type: "int", Default: "10", Description: "With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing."},
					{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take before it counts as failed. Must be more than 0."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, on a device reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
					{Name: "transport", Type: "string", Returned: "always", Description: "The connection that answered: ssh or winrm."},
					{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
					{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The connection that answered. Both halves are identical, since a wait changes nothing."},
				},
				Examples: []collection.Example{
					{Name: "Wait for a new Windows VM's first boot", RunbookYAML: "- name: Wait for the Windows lab VM to let its Administrator in\n  wait.connection:\n    timeout: 1800\n    sleep: 15\n"},
					{Name: "Wait out a reboot", RunbookYAML: "- name: Give the machine time to go down, then wait for it\n  wait.connection:\n    delay: 30\n    timeout: 600\n"},
					{Name: "Wait for a resumed VM that keeps answering", RunbookYAML: "- name: Wait until the resumed VM lets its account in three times running\n  wait.connection:\n    timeout: 900\n    backoff: exponential\n    sleep: 2\n    max_sleep: 60\n    steady: 3\n    steady_interval: 10\n"},
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
	connectionParamBackoff        = "backoff"
	connectionParamMaxSleep       = "max_sleep"
	connectionParamSteady         = "steady"
	connectionParamSteadyInterval = "steady_interval"
	connectionStatTransport       = "transport"
	connectionStatTries           = "tries"
	connectionStatFailures        = "failures"
)

// The ways the gap between failed tries can grow.
const (
	backoffFixed       = "fixed"
	backoffExponential = "exponential"
)

// The connections a device can be tried over.
const (
	transportSSH   = "ssh"
	transportWinRM = "winrm"
)

// connectionRequest is one connection wait's validated timing.
type connectionRequest struct {
	timeout, delay, sleep, perTry time.Duration
	// exponential doubles the gap after each failed try, up to maxSleep.
	exponential bool
	maxSleep    time.Duration
	// steady is how many tries in a row must answer, steadyInterval apart.
	steady         int
	steadyInterval time.Duration
}

// readConnection reads and checks the timing parameters, refusing one that
// would do nothing as the others are set.
func readConnection(params map[string]any) (connectionRequest, error) {
	r := connectionRequest{steady: 1}
	given := map[string]bool{}
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
		{connectionParamMaxSleep, 60, false, &r.maxSleep},
		{connectionParamSteadyInterval, 10, false, &r.steadyInterval},
	} {
		n, set, err := sdk.IntParam(params, p.key)
		if err != nil {
			return r, err
		}
		given[p.key] = set
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

	backoff, err := sdk.OptionalStringParam(params, connectionParamBackoff)
	if err != nil {
		return r, err
	}
	switch backoff {
	case "", backoffFixed:
		if given[connectionParamMaxSleep] {
			return r, fmt.Errorf("max_sleep bounds an exponential backoff; with backoff %s every gap is sleep, so set backoff: exponential or leave max_sleep out", backoffFixed)
		}
	case backoffExponential:
		r.exponential = true
		if r.maxSleep < r.sleep {
			return r, fmt.Errorf("max_sleep (%s) must not be less than sleep (%s), the first gap it bounds", r.maxSleep, r.sleep)
		}
	default:
		return r, fmt.Errorf("backoff must be %s or %s, not %q", backoffFixed, backoffExponential, backoff)
	}

	steady, set, err := sdk.IntParam(params, connectionParamSteady)
	if err != nil {
		return r, err
	}
	if set {
		if steady < 1 {
			return r, fmt.Errorf("steady must be at least 1, not %d", steady)
		}
		r.steady = steady
	}
	if r.steady == 1 && given[connectionParamSteadyInterval] {
		return r, fmt.Errorf("steady_interval spaces the answers of a streak; with steady 1 there is one answer, so set steady above 1 or leave steady_interval out")
	}
	if streak := time.Duration(r.steady-1) * r.steadyInterval; r.delay+streak >= r.timeout {
		return r, fmt.Errorf("a streak of %d answers %s apart takes %s, which with the delay (%s) does not fit in the timeout (%s)",
			r.steady, r.steadyInterval, streak, r.delay, r.timeout)
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
	waited, err := waitForConnection(ctx, req, try, connectionPace{jitter: connectionJitter, sleep: waitSleep})
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s did not answer over %s: %w", fqcn, device.Name(), transport, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{connectionStatTransport: transport})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(connectionStatTransport, transport); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	for _, stat := range []struct {
		key   string
		value int
	}{
		{waitStatElapsed, int(waited.elapsed.Seconds())},
		{connectionStatTries, waited.tries},
		{connectionStatFailures, waited.failures},
	} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// connectionWaited is how a wait that ended well went.
type connectionWaited struct {
	elapsed         time.Duration
	tries, failures int
}

// connectionPace is how a connection wait spends the time between tries:
// jitter turns a backoff gap into the gap slept, and sleep sleeps it. A
// test hands in its own, to read the schedule without living through it.
type connectionPace struct {
	jitter func(time.Duration) time.Duration
	sleep  func(context.Context, time.Duration) error
}

// connectionJitter is the gap actually slept for a backoff gap of d: a
// random point in its upper half, so machines restarted together, waited
// for by one run, do not try in step. It reads crypto/rand, which costs
// nothing here and leaves no weak generator for a scanner to question.
// rand.Read never returns an error (it ends the program when the system's
// generator fails), so there is none to handle.
var connectionJitter = func(d time.Duration) time.Duration {
	half := d / 2
	var b [8]byte
	_, _ = rand.Read(b[:])
	return half + time.Duration(binary.BigEndian.Uint64(b[:])%(uint64(half)+1))
}

// waitForConnection tries until req.steady tries in a row succeed, each
// try bounded by req.perTry, and returns how that went. After a failed try
// it waits req.sleep, or with an exponential backoff a gap doubling from
// req.sleep up to req.maxSleep, passed through pace.jitter; between answers of
// a streak it waits req.steadyInterval. A failed try ends a streak and
// starts the backoff again from req.sleep, since a machine that was just
// answering is likely to again soon. It gives up at the timeout, quoting
// the last failure and, when the machine answered, its longest streak.
func waitForConnection(ctx context.Context, req connectionRequest, try func(context.Context) error, pace connectionPace) (connectionWaited, error) {
	start := time.Now()
	deadline := start.Add(req.timeout)
	var w connectionWaited
	var last error
	streak, longest := 0, 0
	gap := req.sleep
	interval := req.delay
	for {
		if interval > 0 {
			if err := pace.sleep(ctx, interval); err != nil {
				return w, err
			}
		}
		tryCtx, cancel := context.WithTimeout(ctx, req.perTry)
		err := try(tryCtx)
		cancel()
		w.tries++
		if err == nil {
			streak++
			longest = max(longest, streak)
			if streak >= req.steady {
				w.elapsed = time.Since(start)
				return w, nil
			}
			interval, gap = req.steadyInterval, req.sleep
		} else {
			w.failures++
			last, streak = err, 0
			interval = gap
			if req.exponential {
				interval = pace.jitter(gap)
				gap = min(2*gap, req.maxSleep)
			}
		}
		// A run torn down during the try is seen by the sleep above.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return w, timedOut(req, longest, last)
		}
		interval = min(interval, remaining)
	}
}

// timedOut says why a wait gave up: the last failure, and when the machine
// answered at all, the longest streak it managed of the one asked for.
func timedOut(req connectionRequest, longest int, last error) error {
	switch {
	case last == nil:
		return fmt.Errorf("timed out after %s with %d of %d answers in a row and no refusal: the streak, with each try's own time, did not fit in what was left", req.timeout, longest, req.steady)
	case longest > 0:
		return fmt.Errorf("timed out after %s: it answered %d time(s) in a row at most, of the %d asked for; the last refusal said: %w", req.timeout, longest, req.steady, last)
	}
	return fmt.Errorf("timed out after %s; the last try said: %w", req.timeout, last)
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
		if dialect, cli := cliDialect(device); cli {
			return func(ctx context.Context) error { return cliTry(ctx, rc, device, params, fqcn, dialect) }, transportSSH, nil
		}
		return func(ctx context.Context) error { return sshTry(ctx, rc, device, params, fqcn) }, transportSSH, nil
	}
	return nil, "", fmt.Errorf("device %q is reached over neither SSH nor WinRM", device.Name())
}

// cliDialect is the CLI convention of a network device whose terminal is
// not a POSIX shell: Cisco IOS's for a device declaring CiscoIOSCapable,
// else the one its own cli_prompt names. A device with neither is tried
// with a command, as any other SSH device is.
func cliDialect(device inventory.InventoryItem) (netcli.Dialect, bool) {
	if device.HasCapability(capability.NameCiscoIOS) {
		return netcli.IOS, true
	}
	if cli, ok := device.(capability.NetworkCLICapable); ok && device.HasCapability(capability.NameNetworkCLI) && cli.CLIPrompt() != "" {
		return netcli.FromPrompt(cli.CLIPrompt()), true
	}
	return netcli.Dialect{}, false
}

// cliTry logs in afresh over SSH, opens a terminal and waits for the
// device's prompt: what a network device answers once it is ready, where
// a POSIX machine answers a command.
func cliTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, dialect netcli.Dialect) error {
	conn, err := sdk.ConnectFresh(ctx, rc, device, params, fqcn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() // a try's connection has nothing left to report once it answered
	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		return err
	}
	session, err := netcli.Open(ctx, shell, dialect, netcli.Options{})
	if err != nil {
		_ = shell.Close()
		return err
	}
	return session.Close()
}

// sshTry logs in over SSH and runs a command that does nothing. A command
// that ran, whatever it answered, shows the machine lets the account in.
// It always logs in afresh (sdk.ConnectFresh): a session borrowed from the
// run's pool would show only that a login once worked.
func sshTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) error {
	conn, err := sdk.ConnectFresh(ctx, rc, device, params, fqcn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() // a try's connection has nothing left to report once it answered
	_, err = conn.Run(ctx, "exit 0")
	return err
}

// win.reboot: a Windows host restarted over WinRM, and waited for until it
// answers with a new boot time.
package win

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const fqcnReboot = "win.reboot"

// win.reboot's parameters, Ansible's win_reboot names.
const (
	paramRebootTimeout   = "reboot_timeout"
	paramPreRebootDelay  = "pre_reboot_delay"
	paramPostRebootDelay = "post_reboot_delay"
	paramMsg             = "msg"
	paramConnectTimeout  = "connect_timeout"
)

// defaultRebootMsg is what shutdown.exe shows and records when a task
// names no message.
const defaultRebootMsg = "Reboot initiated by The Pleiades"

// maxRebootMsg is shutdown.exe's own limit on /c.
const maxRebootMsg = 512

// maxPreRebootDelay is shutdown.exe's own limit on /t that this method
// accepts: ten minutes, Ansible's ceiling too.
const maxPreRebootDelay = 600

// shutdownExe is shutdown.exe by its absolute path, for the reason
// pkg/winrmexec names its interpreters absolutely: cmd.exe looks in the
// working directory before the system's.
const shutdownExe = `C:\Windows\System32\shutdown.exe`

// errShutdownScheduled is shutdown.exe's exit status when a restart or
// shutdown is already scheduled (ERROR_SHUTDOWN_IS_SCHEDULED).
const errShutdownScheduled = 1190

// rebootProbeInterval is how long a failed try waits before the next.
const rebootProbeInterval = 5 * time.Second

// bootTimeScript prints the host's last boot time, in UTC.
const bootTimeScript = `[Console]::OutputEncoding = [Text.Encoding]::UTF8
(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')`

// rebootRun, rebootSleep and rebootNow are how win.reboot reaches the host,
// waits and reads the clock; a test replaces them.
var (
	rebootRun = func(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, shell winrmexec.Shell, script string, opts winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Run(ctx, target, auth, shell, script, opts)
	}
	rebootSleep = func(ctx context.Context, d time.Duration) error {
		if d <= 0 {
			return ctx.Err()
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	rebootNow = time.Now
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "win.reboot",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("WinRMCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
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
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "A restart cannot be taken back: once the host has restarted there is nothing to put back, so " +
					"no undo is recorded.",
			},
			// A check reads the boot time and sends nothing.
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Restarts a Windows host over WinRM and waits until it is back, as ansible.windows.win_reboot does.",
				Description: "Reads the host's last boot time, has shutdown.exe restart it pre_reboot_delay seconds later (closing applications that do not exit, and recorded as a planned restart for maintenance), then waits until the host answers over WinRM again with a later boot time, which is what shows it restarted rather than only answered, and then post_reboot_delay seconds more. reboot_timeout bounds the wait from the moment the restart is asked for; a try the host refuses, or does not answer within connect_timeout, is made again five seconds later. A restart already scheduled on the host is cancelled and replaced by this one. A run always reports a change, and a restart cannot be undone. A check reads the boot time and sends nothing.",
				Params: []collection.Param{
					{Name: "reboot_timeout", Type: "int", Default: "600", Description: "How many seconds the host may take to come back, from the moment the restart is asked for."},
					{Name: "pre_reboot_delay", Type: "int", Default: "2", Description: "How many seconds shutdown.exe waits before restarting, at least 0 and at most 600."},
					{Name: "post_reboot_delay", Type: "int", Default: "0", Description: "How many seconds to wait once the host is back, for services that start after WinRM does."},
					{Name: "msg", Type: "string", Default: "Reboot initiated by The Pleiades", Description: "The message shutdown.exe shows signed-in users and records, at most 512 characters on one line."},
					{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try to reach the host may take."},
				},
				Returns: []collection.ReturnField{
					{Name: "rebooted", Type: "bool", Returned: "always", Description: "Whether the host restarted: true on a run, false in a check."},
					{Name: "elapsed", Type: "int", Returned: "on a run", Description: "How many seconds the restart took, from asking for it to the host being back."},
					{Name: "previous_boot_time", Type: "string", Returned: "always", Description: "When the host last booted before this task, in UTC (RFC 3339)."},
					{Name: "boot_time", Type: "string", Returned: "on a run", Description: "When the host booted this time, in UTC (RFC 3339)."},
				},
				Examples: []collection.Example{
					{Name: "Restart after a change that needs it", RunbookYAML: "- name: Install Server Core's application compatibility feature\n  win.capability.install:\n    name: ServerCore.AppCompatibility~~~~0.0.1.0\n  register: appcompat\n\n- name: Restart to finish it\n  win.reboot:\n    reboot_timeout: 900\n  when:\n    - result.appcompat.reboot_required\n"},
				},
				SeeAlso: []string{"win.capability.install", "win.feature.install", "wait.connection"},
			},
		},
		Invoke: Reboot,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return reboot(ctx, rc, d, p, true)
		},
	})
}

// rebootSpec is one task's parameters, read and checked before contact.
type rebootSpec struct {
	timeout, pre, post, connect time.Duration
	msg                         string
}

// parseReboot reads a task's parameters with Ansible's defaults.
func parseReboot(params map[string]any) (rebootSpec, error) {
	seconds := func(key string, fallback, least, most int) (time.Duration, error) {
		v, given, err := sdk.IntParam(params, key)
		if err != nil {
			return 0, err
		}
		if !given {
			v = fallback
		}
		if v < least || (most > 0 && v > most) {
			if most > 0 {
				return 0, fmt.Errorf("%s must be from %d to %d seconds", key, least, most)
			}
			return 0, fmt.Errorf("%s must be at least %d seconds", key, least)
		}
		return time.Duration(v) * time.Second, nil
	}
	var s rebootSpec
	var err error
	if s.timeout, err = seconds(paramRebootTimeout, 600, 1, 0); err != nil {
		return s, err
	}
	if s.pre, err = seconds(paramPreRebootDelay, 2, 0, maxPreRebootDelay); err != nil {
		return s, err
	}
	if s.post, err = seconds(paramPostRebootDelay, 0, 0, 0); err != nil {
		return s, err
	}
	if s.connect, err = seconds(paramConnectTimeout, 20, 1, 0); err != nil {
		return s, err
	}
	msg, err := sdk.OptionalStringParam(params, paramMsg)
	if err != nil {
		return s, err
	}
	if _, given := params[paramMsg]; !given {
		msg = defaultRebootMsg
	}
	if len([]rune(msg)) > maxRebootMsg || strings.ContainsAny(msg, "\r\n") {
		return s, fmt.Errorf("%s must be at most %d characters on one line", paramMsg, maxRebootMsg)
	}
	if err := winrmexec.CheckText(paramMsg, msg); err != nil {
		return s, err
	}
	s.msg = msg
	return s, nil
}

// bootTime reads when the host last booted, one try bounded by limit.
func bootTime(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, opts winrmexec.Options, limit time.Duration) (time.Time, error) {
	opts.Timeout = limit
	res, err := rebootRun(ctx, target, auth, winrmexec.ShellPowerShell, bootTimeScript, opts)
	if err != nil {
		return time.Time{}, err
	}
	if res.ExitCode != 0 {
		return time.Time{}, fmt.Errorf("reading the boot time exited %d: %s", res.ExitCode, bounded(res.Stderr))
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(res.Stdout))
	if err != nil {
		// The parse error is not wrapped: it quotes the host's answer again,
		// whole, which bounded has already quoted once within its bound.
		return time.Time{}, fmt.Errorf("the boot time %s is not an RFC 3339 time", bounded(res.Stdout))
	}
	return t.UTC(), nil
}

// askRestart has shutdown.exe restart the host after s.pre, forcing
// applications closed (/f), recorded as planned maintenance (/d p:4:1). A
// restart already scheduled is cancelled (/a) and asked for again once.
// sent reports whether the request may have reached the host, which is
// true for any failure after the command was sent: the connection dropping
// as the host goes down looks like one.
func askRestart(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, opts winrmexec.Options, s rebootSpec) (sent bool, err error) {
	args := []string{"/r", "/f", "/t", strconv.Itoa(int(s.pre / time.Second)), "/d", "p:4:1"}
	if s.msg != "" {
		args = append(args, "/c", s.msg)
	}
	restart, err := winrmexec.CommandLine(shutdownExe, args...)
	if err != nil {
		return false, err
	}
	for attempt := 0; ; attempt++ {
		res, err := rebootRun(ctx, target, auth, winrmexec.ShellNone, restart, opts)
		if err != nil {
			var notStarted *winrmexec.NotStartedError
			return !errors.As(err, &notStarted), err
		}
		switch {
		case res.ExitCode == 0:
			return true, nil
		case res.ExitCode == errShutdownScheduled && attempt == 0:
			abort, _ := winrmexec.CommandLine(shutdownExe, "/a")
			if _, err := rebootRun(ctx, target, auth, winrmexec.ShellNone, abort, opts); err != nil {
				return false, fmt.Errorf("cancelling the restart already scheduled: %w", err)
			}
		default:
			return false, fmt.Errorf("shutdown.exe exited %d: %s", res.ExitCode, bounded(res.Stderr+res.Stdout))
		}
	}
}

// Reboot implements win.reboot.
func Reboot(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return reboot(ctx, rc, device, params, false)
}

// reboot is win.reboot's body; check reads the boot time and stops there.
func reboot(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnReboot, err)
	}
	s, err := parseReboot(params)
	if err != nil {
		return fail(err)
	}
	target, auth, opts, err := session(rc, device)
	if err != nil {
		return fail(err)
	}
	before, err := bootTime(ctx, target, auth, opts, s.connect)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat("previous_boot_time", before.Format(time.RFC3339)); err != nil {
		return fail(err)
	}
	if check {
		if err := rc.SetStat("rebooted", false); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, nil
	}

	start := rebootNow()
	deadline := start.Add(s.timeout)
	if sent, err := askRestart(ctx, target, auth, opts, s); err != nil && !sent {
		return fail(err)
	}
	// Until the delay is out, the host answers with the boot time it had.
	if err := rebootSleep(ctx, s.pre); err != nil {
		return fail(err)
	}
	why := "it kept answering with the boot time it had"
	for {
		after, err := bootTime(ctx, target, auth, opts, s.connect)
		if err == nil && !after.Equal(before) {
			elapsed := int(rebootNow().Sub(start).Round(time.Second) / time.Second)
			if err := rebootSleep(ctx, s.post); err != nil {
				return fail(err)
			}
			for k, v := range map[string]any{"rebooted": true, "elapsed": elapsed, "boot_time": after.Format(time.RFC3339)} {
				if err := rc.SetStat(k, v); err != nil {
					return fail(err)
				}
			}
			return collection.Result{Changed: true}, nil
		}
		if err != nil {
			why = "the last try answered: " + err.Error()
		}
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		if !rebootNow().Add(rebootProbeInterval).Before(deadline) {
			return fail(fmt.Errorf("the host did not come back restarted within %s (%s)", s.timeout, why))
		}
		if err := rebootSleep(ctx, rebootProbeInterval); err != nil {
			return fail(err)
		}
	}
}

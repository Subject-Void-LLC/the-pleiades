// Package ios implements "net.ios.config": the Cisco-IOS-specific
// configuration method, built on netcli.IOS's own real paging,
// configuration-mode and error conventions rather than a device's
// declared cli_prompt property. internal/catalog/net/cli's generic
// "net.cli.*" siblings are what a device with no vendor-specific method
// yet falls back to.
package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// paramLines and paramBackup match cisco.ios.ios_config's own parameter
// names, the same reuse-Ansible's-vocabulary reasoning
// internal/catalog/exec and internal/catalog/net/cli both already
// follow.
const (
	paramLines  = "lines"
	paramBackup = "backup"
)

// paramCheckpoint turns the checkpoint a change's undo returns to on or
// off.
const paramCheckpoint = "checkpoint"

// statBackup is the stat name backup: true records the pre-change
// running-config under.
const statBackup = "backup"

// The stats a change records: the checkpoint it can be returned to, the
// device's own differences it made, and, where there is no checkpoint, the
// lines that undo it.
const (
	statCheckpoint  = "checkpoint"
	statDifferences = "differences"
	statUndoLines   = "undo_lines"
)

// configFQCN and replaceFQCN are the two methods a change and its undo run.
const (
	configFQCN  = "net.ios.config"
	replaceFQCN = "net.ios.replace"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.config",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"telnet",
				"console",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameCiscoIOS,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo returns the device to a checkpoint of its running configuration kept on the device (net.ios.replace, named by the file's path alone, so no configuration text reaches the journal), refusing when the device has changed since. A device with nowhere to keep one records no undo: the configuration lines that would undo the change are shown, masked, for the task's rollback: list, since lines read off the device never reach the journal.",
				Inverses: []sdk.InverseSpec{
					{FQCN: replaceFQCN, Record: []string{"checkpoint", "expect", sdk.ParamInsecureTelnet}, MayKeepOnFailure: true},
				},
			},
			NoCheckReason: "what a configuration line changes is decided by IOS's own parser as it applies the line, and IOS " +
				"offers no way to ask without applying it",
			Doc: configDoc(),
		},
		Invoke:   Config,
		UndoCall: configUndoCall,
	})
}

func configDoc() collection.Doc {
	return collection.Doc{
		Summary: "Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly.",
		Description: "Opens an interactive session over SSH (or over Telnet, with insecure_telnet), using netcli.IOS's own paging, configuration-mode and error conventions, and applies lines as a batch: \"configure terminal\", each line in order, then \"end\". Aborts on the first line the device rejects (an IOS \"% ...\" error), still leaving configuration mode before returning that error. " +
			"Before the lines, it copies the running configuration to a checkpoint file on the device (its default writable disk or flash, such as unix: or flash:), and after them the device's own show archive config differences says what changed: no difference reports unchanged and deletes the checkpoint, so a second run of the same lines changes nothing. A change copies the configuration it left to a second checkpoint and records its undo as net.ios.replace naming both files, which returns the device exactly to the first (configure replace) and refuses if the device no longer matches the second. The files stay on the device until that undo runs. " +
			"A device with no writable file system, or no configure replace, records no undo, with a warning: the configuration lines that would undo the change, written from its configuration read before and after it (the negation of each added line, each removed line put back), are reported under undo_lines, masked, for the task's rollback: list, since lines read off a device never reach the journal. A change inside an access list, a banner or a certificate has no such lines. checkpoint: false does the same on any device. The differences this reports are masked as net.batfish.check masks a candidate.",
		Params: []collection.Param{
			{Name: paramLines, Type: "list of string", Required: true, Description: "The configuration lines to apply, in order, WITHOUT \"configure terminal\" or \"end\": this method supplies both itself.", Format: collection.ParamFormatCommand},
			{Name: paramCheckpoint, Type: "bool", Default: "true", Description: "Keep a checkpoint of the running configuration on the device before the change, which its undo returns to exactly. false records no undo, and reports the lines that would undo the change for the task's rollback: list."},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Capture the device's running-config with \"show running-config\" before applying any line, recorded under the backup stat."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			{Name: sdk.ParamInsecureTelnet, Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
		},
		Returns: []collection.ReturnField{
			{Name: statCheckpoint, Type: "string", Returned: "when a checkpoint was kept and the lines changed the configuration", Description: "The checkpoint file on the device holding the configuration before the change, which the undo returns to."},
			{Name: statDifferences, Type: "list of string", Returned: "when a checkpoint was kept", Description: "The device's own contextual differences the change made (+ added, - removed), masked."},
			{Name: statUndoLines, Type: "list of string", Returned: "when no checkpoint was kept and the lines changed the configuration", Description: "The configuration lines that would undo the change, masked; copy them into the task's rollback: list, putting each masked secret back."},
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own \"type 7\" is trivially reversible) encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
		},
		Examples: []collection.Example{
			{
				Name:        "Create a loopback interface, undoable by pleiades rollback",
				RunbookYAML: "- name: Add a loopback interface\n  net.ios.config:\n    lines:\n      - interface Loopback0\n      - description managed by pleiades\n",
			},
		},
		SeeAlso: []string{"net.ios.replace", "net.ios.parse", "net.cli.command", "net.cli.config"},
	}
}

// configUndoCall is net.ios.config's answer, from a call alone, to whether
// it records an undo: a call with checkpoint anything but true (false, or a
// template whose value is not known before the run) records none. A device
// with nowhere to keep a checkpoint records none either, which only the
// run can find; the undo then stops naming the task.
func configUndoCall(params map[string]any) string {
	if v, present := params[paramCheckpoint]; present && v != true {
		return "checkpoint is not true, so no checkpoint is kept and the undo is lines only shown, never recorded"
	}
	return ""
}

// Config implements the "net.ios.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = configFQCN

	lines, present, err := sdk.StringSlice(params, paramLines)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !present || len(lines) == 0 {
		return collection.Result{}, fmt.Errorf("%s: %s is required and must be a non-empty list", fqcn, paramLines)
	}
	// undo_lines are masked, so lines pasted from them unchanged would set
	// each secret on the device to its placeholder.
	if err := netcfg.RefuseMasked(paramLines, strings.Join(lines, "\n")); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	backup, err := sdk.BoolParamOr(params, paramBackup, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	checkpoint, err := sdk.BoolParamOr(params, paramCheckpoint, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	var running string
	if backup {
		if running, err = session.Command(ctx, "show running-config"); err != nil {
			return collection.Result{}, fmt.Errorf("%s: capturing backup: %w", fqcn, err)
		}
		if err := rc.SetStat(statBackup, running); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	var warnings []string
	if checkpoint {
		before, after, err := takeCheckpoint(ctx, session)
		switch {
		case err == nil:
			applyErr := session.Config(ctx, lines)
			return finishCheckpoint(ctx, rc, session, params, before, after, applyErr)
		case !errors.Is(err, errNoCheckpoint):
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		warnings = append(warnings, fmt.Sprintf("no checkpoint was kept: %v", err))
	}
	if running == "" {
		if running, err = session.Command(ctx, "show running-config"); err != nil {
			return collection.Result{}, fmt.Errorf("%s: reading the configuration before the change: %w", fqcn, err)
		}
	}
	applyErr := session.Config(ctx, lines)
	return finishLines(ctx, rc, session, running, applyErr, warnings)
}

// finishCheckpoint ends a change made with a checkpoint: it asks the device
// what the lines changed, deletes the checkpoint when nothing did, and
// otherwise saves the configuration the change left and records the undo
// that returns to the checkpoint. A change that failed part way keeps an
// undo with no second checkpoint, since whatever reached the device is
// on it.
func finishCheckpoint(ctx context.Context, rc sdk.RunbookContext, session iosSession, params map[string]any, before, after string, applyErr error) (collection.Result, error) {
	const fqcn = configFQCN
	diffs, diffErr := differences(ctx, session, before, runningConfig)
	if applyErr != nil || diffErr != nil {
		if recErr := recordReplace(rc, params, before, "", true); recErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, recErr)
		}
		if applyErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
		}
		return collection.Result{}, fmt.Errorf("%s: after the change: %w", fqcn, diffErr)
	}
	if err := rc.SetStat(statDifferences, diffs); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if len(diffs) == 0 {
		if err := deleteFile(ctx, session, before); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: false}, nil
	}
	if err := rc.SetStat(statCheckpoint, before); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := saveRunning(ctx, session, after); err != nil {
		// The change is on the device and its checkpoint is kept: the
		// undo stands, only without the refusal a later change would get.
		if recErr := recordReplace(rc, params, before, "", true); recErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, recErr)
		}
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordReplace(rc, params, before, after, true); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// recordReplace records the undo that returns the device to before,
// refusing if it no longer matches after when after is not empty. It
// reaches the device the way the change did.
func recordReplace(rc sdk.RunbookContext, params map[string]any, before, after string, keepOnFailure bool) error {
	undo := map[string]any{"checkpoint": before}
	if after != "" {
		undo["expect"] = after
	}
	carryTransport(params, undo)
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          replaceFQCN,
		Params:        undo,
		Description:   fmt.Sprintf("Returns the device's configuration to the checkpoint %s, kept before this change.", before),
		KeepOnFailure: keepOnFailure,
	})
}

// carryTransport copies the change's Telnet opt-in into its undo: a device
// reached only over Telnet has no other way in, and the exposure is the
// one the change's author already accepted for that device. Skipping host
// key verification is never carried (pkg/collection refuses it): an undo
// checks the host key like any task.
func carryTransport(params, undo map[string]any) {
	if sdk.BoolParam(params, sdk.ParamInsecureTelnet) {
		undo[sdk.ParamInsecureTelnet] = true
	}
}

// finishLines ends a change made without a checkpoint: it reads the
// configuration again and writes, masked, the lines that would undo the
// difference, for the task's rollback: list. It records no undo: the
// lines are read off the device, which the journal never holds.
func finishLines(ctx context.Context, rc sdk.RunbookContext, session iosSession, before string, applyErr error, warnings []string) (collection.Result, error) {
	const fqcn = configFQCN
	after, err := session.Command(ctx, "show running-config")
	if err != nil {
		if applyErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
		}
		return collection.Result{}, fmt.Errorf("%s: reading the configuration after the change: %w", fqcn, err)
	}
	undo, invErr := iosconfig.Inverse(iosconfig.Parse(before), iosconfig.Parse(after))
	changed := invErr != nil || len(undo) > 0
	switch {
	case invErr != nil:
		warnings = append(warnings, fmt.Sprintf("no undo was recorded, and none can be written: %v; give the task a rollback: list", invErr))
	case len(undo) > 0:
		if err := rc.SetStat(statUndoLines, maskLines(undo)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		warnings = append(warnings, "no undo was recorded: the lines that undo this change are under undo_lines (masked); copy them into the task's rollback: list")
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if applyErr != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
	}
	return collection.Result{Changed: changed}, nil
}

// maskLines masks the secrets in configuration lines.
func maskLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = netcfg.Mask(l)
	}
	return out
}

// iosSession is the narrow surface Config actually uses from a
// *netcli.Session: enough that ios_internal_test.go can swap in a
// canned double with no real I/O, and no more.
type iosSession interface {
	Command(ctx context.Context, line string) (string, error)
	Config(ctx context.Context, lines []string) error
	Dialog(ctx context.Context, line string, opts netcli.DialogOptions) (netcli.DialogResult, error)
	Close() error
}

// openSession is the seam this package's own tests swap, the same role
// internal/catalog/net/cli's own openSession plays there -- a separate
// package, so a separate small seam rather than a shared one.
var openSession = realOpenSession

// realOpenSession opens the device's CLI over SSH, or over Telnet with the
// task's opt-in (sdk.OpenShell), in IOS's dialect. The Session's Close ends
// the connection as well, so a method closes one thing.
func realOpenSession(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
	shell, err := sdk.OpenShell(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	// The enable secret, when the credential holds one, takes an account
	// that lands in user mode ("R1>") to privileged mode.
	session, err := netcli.Open(ctx, shell, netcli.IOS, netcli.Options{EnableSecret: rc.InjectSecrets()[wire.SecretEnablePassword]})
	if err != nil {
		_ = shell.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	return session, nil
}

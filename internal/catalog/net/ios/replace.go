// net.ios.replace: return a Cisco IOS device's running configuration to a
// checkpoint file the device holds, through IOS's own configure replace.
package ios

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The parameters net.ios.replace takes beside the transport opt-ins.
const (
	paramReplaceCheckpoint = "checkpoint"
	paramReplaceExpect     = "expect"
	paramReplaceKeep       = "keep"
	paramReplaceForce      = "force"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: replaceFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh", "telnet", "console"},
			RequiredCapabilities: []capability.Name{capability.NameCiscoIOS},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			NoBecomeReason:  sdk.NoBecomeCLISession,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "It is the undo net.ios.config records; what it removes is the change that checkpoint was kept before, which running that change again puts back.",
			},
			SupportsCheck: true,
			Doc:           replaceDoc(),
		},
		Invoke: Replace,
		Check:  checkReplace,
	})
}

// replaceDoc is net.ios.replace's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func replaceDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Replaces a Cisco IOS device's running configuration with a checkpoint file on the device.",
		Description: "Runs IOS's configure replace with a checkpoint file the device holds (net.ios.config writes one before each change and records this method as its undo), so the device itself puts back exactly the configuration the checkpoint holds: lines a change added are removed and lines it replaced or removed come back, with no inverse line written by anyone. When expect names a second checkpoint (the one net.ios.config writes after its change), the device must still match it, by the device's own show archive config differences, or this refuses before replacing anything, unless force is true: configure replace returns the whole configuration to the checkpoint, so a change someone made since would be undone with it. After the replace, the device must report no differences from the checkpoint, or the task fails; a line in IOS's \"% \" form the replace prints fails it only then, and when the device matches it is reported as a warning, masked. The checkpoint files net.ios.config made (named pleiades-<id>.cfg) are deleted afterwards unless keep is true; any other file, such as nvram:startup-config or a configuration kept on flash:, is never deleted. A check reports what the replace would change, from the same comparison, and changes nothing. The configuration lines this reports are masked as net.batfish.check masks a candidate: a password, secret or key is never shown.",
		Params: []collection.Param{
			{Name: paramReplaceCheckpoint, Type: "string", Required: true, Description: "The checkpoint to return to, a file on the device such as unix:pleiades-3f2a.cfg or flash:pleiades-3f2a.cfg."},
			{Name: paramReplaceExpect, Type: "string", Description: "A checkpoint the running configuration must still match before anything is replaced, refusing otherwise: the configuration as the change left it."},
			{Name: paramReplaceForce, Type: "bool", Default: "false", Description: "Replace even when the running configuration no longer matches expect, undoing whatever changed since along with the change."},
			{Name: paramReplaceKeep, Type: "bool", Default: "false", Description: "Keep the checkpoint files net.ios.config made on the device after a replace that leaves no differences, rather than deleting them. A file it did not make is never deleted."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			{Name: sdk.ParamInsecureTelnet, Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
		},
		Returns: []collection.ReturnField{
			{Name: statDifferences, Type: "list of string", Returned: "always", Description: "The device's own contextual differences between the running configuration and the checkpoint before the replace, masked: what the replace changes, or would change in a check."},
		},
		Examples: []collection.Example{
			{
				Name:        "Return a device to a checkpoint a change left",
				RunbookYAML: "- name: Undo the loopback change\n  net.ios.replace:\n    checkpoint: unix:pleiades-3f2a.cfg\n    expect: unix:pleiades-3f2a-after.cfg\n",
			},
		},
		SeeAlso: []string{"net.ios.config", "net.ios.parse"},
	}
}

// replacePlan is what a replace was asked to do, its paths checked.
type replacePlan struct {
	checkpoint, expect string
	force, keep        bool
}

// readReplace reads and checks net.ios.replace's parameters, before the
// device is reached: a path goes into commands the device runs.
func readReplace(params map[string]any) (replacePlan, error) {
	var p replacePlan
	var err error
	if p.checkpoint, err = sdk.RequiredStringParam(params, paramReplaceCheckpoint); err != nil {
		return p, err
	}
	if p.expect, err = sdk.OptionalStringParam(params, paramReplaceExpect); err != nil {
		return p, err
	}
	for _, path := range []string{p.checkpoint, p.expect} {
		if path != "" && !checkpointPath.MatchString(path) {
			return p, fmt.Errorf("%q is not a file on the device such as unix:pleiades-3f2a.cfg", path)
		}
	}
	if p.force, err = sdk.BoolParamOr(params, paramReplaceForce, false); err != nil {
		return p, err
	}
	if p.keep, err = sdk.BoolParamOr(params, paramReplaceKeep, false); err != nil {
		return p, err
	}
	return p, nil
}

// Replace implements the "net.ios.replace" collection method.
func Replace(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return replace(ctx, rc, device, params, collection.ModeExecute)
}

// checkReplace is net.ios.replace's check: the same refusals and the same
// comparison, without replacing.
func checkReplace(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return replace(ctx, rc, device, params, collection.ModeCheck)
}

// replace is Replace and its check, which share every read and refusal.
func replace(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = replaceFQCN
	plan, err := readReplace(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	if plan.expect != "" && !plan.force {
		since, err := differences(ctx, session, plan.expect, runningConfig)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if len(since) > 0 {
			return collection.Result{}, fmt.Errorf("%s: the device has changed since %s was kept, and configure replace would undo that too: %s; undo or keep it first, or set force: true to replace anyway", fqcn, plan.expect, shownDifferences(since))
		}
	}
	pending, err := differences(ctx, session, runningConfig, plan.checkpoint)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statDifferences, pending); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: len(pending) > 0}, nil
	}
	if len(pending) > 0 {
		out, err := session.Command(ctx, "configure replace "+plan.checkpoint+" force")
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// A "% " line is a failure only when the device does not match the
		// checkpoint afterwards: a replace that removes a routing protocol
		// prints its notices in IOS's error form and still succeeds, so the
		// comparison, not the wording, decides.
		complained := netcli.IOS.ErrorPattern.MatchString(out)
		left, err := differences(ctx, session, plan.checkpoint, runningConfig)
		switch {
		case complained && (err != nil || len(left) > 0):
			return collection.Result{}, fmt.Errorf("%s: configure replace %s: %s", fqcn, plan.checkpoint, deviceRefusal(out))
		case err != nil:
			return collection.Result{}, fmt.Errorf("%s: after the replace: %w", fqcn, err)
		case len(left) > 0:
			return collection.Result{}, fmt.Errorf("%s: after configure replace the device still differs from %s: %s", fqcn, plan.checkpoint, shownDifferences(left))
		case complained:
			if err := sdk.RecordWarnings(rc, replaceNotices(out, plan.checkpoint)); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	if !plan.keep {
		for _, path := range []string{plan.checkpoint, plan.expect} {
			// Only a checkpoint net.ios.config made is deleted: any other
			// file (nvram:startup-config, a golden configuration on flash:)
			// was the operator's before this ran, and stays.
			if !mintedCheckpoint.MatchString(path) {
				continue
			}
			if err := deleteFile(ctx, session, path); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	return collection.Result{Changed: len(pending) > 0}, nil
}

// errorDifferences bounds how many difference lines an error quotes; the
// whole list is the differences stat's.
const errorDifferences = 10

// shownDifferences is lines as an error quotes them: the first
// errorDifferences, and how many more there are.
func shownDifferences(lines []string) string {
	if len(lines) <= errorDifferences {
		return strings.Join(lines, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(lines[:errorDifferences], "; "), len(lines)-errorDifferences)
}

// replaceNotices are the "% " lines a replace that left the device matching
// checkpoint printed, masked, as warnings.
func replaceNotices(out, checkpoint string) []string {
	var warnings []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if netcli.IOS.ErrorPattern.MatchString(line) {
			warnings = append(warnings, fmt.Sprintf("configure replace printed %q, and the device matches %s afterwards", netcfg.Mask(line), checkpoint))
		}
	}
	return warnings
}

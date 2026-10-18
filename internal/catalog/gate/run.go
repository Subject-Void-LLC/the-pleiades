// gate.run: a project's checks, run on the device, one result each.
package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// detailLines and detailBytes bound how much of each failing check's
// output a failed task's error carries, and how much the error carries in
// all.
const (
	detailLines = 15
	detailBytes = 16 << 10
	ledgerLimit = 8
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRun,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion: ">=0.2.0",
			Status:        collection.StatusImplemented,
			// A check reads and validates the manifest, then answers unchecked
			// (CheckRun): whether a check passes is known only by running it.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "It runs the project's own checks and changes nothing of its own; what a check writes is that check's doing, which this platform cannot see to undo. It is not declared read-only, since a check is a command.",
			},
			Doc: runDoc(),
		},
		Invoke:    Run,
		Check:     CheckRun,
		CheckCall: func(map[string]any) error { return collection.CannotCheck(uncheckable) },
	})
}

// uncheckable is why no gate.run call can be checked.
const uncheckable = "whether a check passes is known only by running it"

// Run implements gate.run: it reads and validates the manifest, runs the
// selected checks, records one result each, and fails when any check
// failed (or skipped, with fail_on_skip).
func Run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRun is gate.run's check: the manifest is read and validated, the
// selection planned and the device's capabilities and timeout command
// confirmed, exactly as a run would, and then the task is reported
// unchecked, since a check is a command whose outcome running alone
// decides.
func CheckRun(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeCheck)
}

// run is Run's and CheckRun's one body.
func run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	req, err := parseRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	conn, err := connectFor(ctx, rc, device, params, fqcnRun)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	m, err := loadManifest(ctx, conn, req.manifest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	plan, err := m.Plan(req.selection)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	osName, err := preflight(ctx, conn, device, plan)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{}, collection.CannotCheck(fmt.Sprintf("the manifest is valid and %d check(s) would run (%s), but %s",
			countSelected(plan), strings.Join(selectedNames(plan), ", "), uncheckable))
	}
	_ = conn.Close()

	runner := &deviceRunner{rc: rc, device: device, params: params, manifest: req.manifest, osName: osName}
	results := gate.Run(ctx, plan, m.Parallelism(req.parallelism), runner.run)
	if err := record(rc, results); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	if err := gate.Verdict(results, req.failOnSkip); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w%s", fqcnRun, err, detail(results))
	}
	return collection.Result{Changed: false}, nil
}

// preflight confirms, before anything runs, that the device can run every
// selected check: the container engine for an image check, the timeout
// command for a directory check; and returns its operating system, lower
// case, for the os precondition.
func preflight(ctx context.Context, conn *remoteexec.Conn, device inventory.InventoryItem, plan []gate.Planned) (string, error) {
	dirChecks := false
	for _, p := range plan {
		if !p.Selected {
			continue
		}
		if why := engineRefusal(device, p.Check); why != "" && p.Check.Image != "" {
			return "", fmt.Errorf("check %s cannot run on device %q: %s", p.Check.Name, device.Name(), why)
		}
		if p.Check.Image == "" {
			dirChecks = true
		}
	}
	// The timeout command is tried in the very form a check runs under, so
	// one that lacks -k (BusyBox before 1.30 took -t SECS instead) is
	// refused here rather than failing every check.
	res, err := conn.RunWithStdinBounded(ctx, "uname -s; timeout -k "+killAfter+" 5s true >/dev/null 2>&1 && echo has-timeout || echo no-timeout", nil, probeOutputLimit)
	if err != nil {
		return "", fmt.Errorf("probing the device: %w", err)
	}
	lines := strings.Fields(res.Stdout)
	if len(lines) < 2 {
		return "", fmt.Errorf("probing the device answered %q", remoteexec.Excerpt(res.Stdout, 128))
	}
	if dirChecks && lines[len(lines)-1] != "has-timeout" {
		return "", errors.New("the device has no timeout command that takes -k and a duration (coreutils, or BusyBox 1.30 or later), which bounds each check on the device")
	}
	return strings.ToLower(lines[0]), nil
}

// record keeps the results as the task's returns, which a failed task
// reports with its failure; detail tells the same story in the error
// itself, which is the first thing a person reads.
func record(rc sdk.RunbookContext, results []gate.Result) error {
	checks := make([]any, len(results))
	var skips []gate.SkipEntry
	for i, r := range results {
		checks[i] = r.Map()
		if r.Status == gate.Skip {
			skips = append(skips, gate.SkipEntry{Name: r.Name, Reason: r.Reason})
		}
	}
	s := gate.Summarize(results)
	for key, value := range map[string]any{
		statChecks:  checks,
		statPassed:  s.Passed,
		statFailed:  s.Failed,
		statSkipped: s.Skipped,
		statSummary: s.String(),
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	if ledger := gate.Ledger("check(s)", skips, ledgerLimit); ledger != "" {
		return rc.SetStat(statLedger, ledger)
	}
	return nil
}

// detail is what a failed task's error adds after its summary: each failing
// check's reason and the end of its output, then the skips, bounded in all.
func detail(results []gate.Result) string {
	var b strings.Builder
	for _, r := range results {
		if r.Status != gate.Fail {
			continue
		}
		fmt.Fprintf(&b, "\n  %s (%s): %s", r.Name, r.Kind, r.Reason)
		if r.Tail != "" {
			kept, cut := gate.LastLines(strings.Split(r.Tail, "\n"), detailLines)
			if cut > 0 {
				kept = append([]string{gate.Omitted(cut)}, kept...)
			}
			b.WriteString("\n      " + strings.Join(kept, "\n      "))
		}
	}
	for _, r := range results {
		if r.Status == gate.Skip {
			fmt.Fprintf(&b, "\n  skipped %s: %s", r.Name, r.Reason)
		}
	}
	text := b.String()
	if len(text) > detailBytes {
		text = strings.ToValidUTF8(text[:detailBytes], "") + "\n  ... (the rest left out)"
	}
	return text
}

// countSelected and selectedNames describe a plan's selection.
func countSelected(plan []gate.Planned) int { return len(selectedNames(plan)) }

func selectedNames(plan []gate.Planned) []string {
	var names []string
	for _, p := range plan {
		if p.Selected {
			names = append(names, p.Check.Name)
		}
	}
	return names
}

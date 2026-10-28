// net.ios.facts: what a Cisco IOS device is, its interfaces and its
// running configuration (masked), read through show commands alone.
package ios

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.facts",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"telnet",
				"console",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CiscoIOSCapable"),
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
			// Read-only: it sends only show commands (show version, show
			// inventory, show ip interface brief, show interfaces, show vrf
			// or show ip vrf brief, show running-config) and never enters
			// configuration mode, so a check runs it for real.
			SupportsCheck: true,
			// A read-only fact gatherer changes nothing, so there is
			// nothing to undo. Stated the same way
			// net.catalyst.device_facts states it, rather than left
			// unanswered: collection.Register refuses an implemented
			// method that claims no reversibility without saying why.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc: factsDoc(),
		},
		Invoke: Facts,
		// Facts itself, since it only reads.
		Check: Facts,
	})
}

// Fact names mirror cisco.ios.ios_facts's own, so a converted playbook
// reading ansible_net_version keeps reading ansible_net_version. This is
// the same reuse-Ansible's-vocabulary reasoning internal/catalog/facts
// already follows for ansible_distribution and friends.
const (
	factHostname     = "ansible_net_hostname"
	factVersion      = "ansible_net_version"
	factModel        = "ansible_net_model"
	factSerialNum    = "ansible_net_serialnum"
	factImage        = "ansible_net_image"
	factUptime       = "ansible_net_uptime"
	factInterfaces   = "ansible_net_interfaces"
	factConfig       = "ansible_net_config"
	factGatherSubset = "ansible_net_gather_subset"
)

const paramGatherSubset = "gather_subset"

const (
	subsetMin        = "min"
	subsetInterfaces = "interfaces"
	subsetConfig     = "config"
	subsetAll        = "all"
)

// subsetPlan is what gather_subset asks for: the subsets to gather, and
// whether config was named itself rather than reached through all, which
// decides whether a device refusing show running-config fails the task.
type subsetPlan struct {
	on          map[string]bool
	namedConfig bool
}

// gathered is what one call has read so far: the values to record, and
// the warnings saying what was left out.
type gathered struct {
	values   map[string]any
	warnings []string
}

// Facts implements the "net.ios.facts" collection method.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.facts"

	plan, err := resolveSubsets(params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	g := &gathered{values: map[string]any{}}
	if plan.on[subsetMin] {
		if err := gatherMin(ctx, session, g); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if plan.on[subsetInterfaces] {
		if err := gatherInterfaces(ctx, session, g); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if plan.on[subsetConfig] {
		kept, err := gatherConfig(ctx, rc, session, g, plan.namedConfig)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// A configuration the device would not show is not gathered, and
		// the subset list says so.
		plan.on[subsetConfig] = kept
	}
	g.values[factGatherSubset] = sortedSubsets(plan.on)

	if err := record(rc, g); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: false}, nil
}

// record writes every value as a stat, so register, until and when read it,
// and every value but the configuration as a fact too. The configuration is
// a stat alone: its placeholders are keyed per run, so as drift data it
// would read as changed every time.
func record(rc sdk.RunbookContext, g *gathered) error {
	for _, name := range sortedKeys(g.values) {
		if err := rc.SetStat(name, g.values[name]); err != nil {
			return err
		}
		if name == factConfig {
			continue
		}
		if err := rc.EmitFact(name, g.values[name]); err != nil {
			return err
		}
	}
	return sdk.RecordWarnings(rc, g.warnings)
}

// resolveSubsets validates gather_subset and expands "all", which is also
// what an absent or empty list means. An unrecognized subset is refused
// rather than skipped, so a typo surfaces as an error naming the value
// instead of as facts that quietly never appear.
func resolveSubsets(params map[string]any, fqcn string) (subsetPlan, error) {
	requested, present, err := sdk.StringSlice(params, paramGatherSubset)
	if err != nil {
		return subsetPlan{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !present || len(requested) == 0 {
		requested = []string{subsetAll}
	}

	plan := subsetPlan{on: map[string]bool{}}
	for _, name := range requested {
		switch s := strings.TrimSpace(strings.ToLower(name)); s {
		case subsetMin, subsetInterfaces:
			plan.on[s] = true
		case subsetConfig:
			plan.on[s] = true
			plan.namedConfig = true
		case subsetAll:
			plan.on[subsetMin] = true
			plan.on[subsetInterfaces] = true
			plan.on[subsetConfig] = true
		default:
			return subsetPlan{}, fmt.Errorf("%s: %s %q is not one of %q, %q, %q, %q", fqcn, paramGatherSubset, name, subsetMin, subsetInterfaces, subsetConfig, subsetAll)
		}
	}
	return plan, nil
}

// errRefused is a show command the device answered with one of IOS's own
// refusals ("% Invalid input detected"), rather than with its data.
type errRefused struct {
	command string
	line    string
}

// Error names the command and the device's refusal, masked as every line a
// device prints is before it leaves this package.
func (e *errRefused) Error() string {
	return fmt.Sprintf("device rejected %q: %s", e.command, netcfg.Mask(e.line))
}

// show sends one show command and returns what the device printed, with
// carriage returns dropped (a Telnet session ends each line with one). A
// transport failure names the command; a device refusal is an *errRefused,
// so a caller can tell "this device will not show that" from a dead session.
func show(ctx context.Context, session iosSession, command string) (string, error) {
	out, err := session.Command(ctx, command)
	if err != nil {
		return "", fmt.Errorf("%s: %w", command, err)
	}
	out = strings.ReplaceAll(out, "\r", "")
	if line, refused := netcli.CommandRejection(out); refused {
		return "", &errRefused{command: command, line: line}
	}
	return out, nil
}

// sortedSubsets is the subsets turned on, in name order.
func sortedSubsets(subsets map[string]bool) []string {
	out := make([]string, 0, len(subsets))
	for name, on := range subsets {
		if on {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// sortedKeys is m's keys in order, so values are recorded in the same
// order on every run.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

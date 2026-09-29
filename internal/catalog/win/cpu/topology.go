// Package cpu implements the win.cpu.* methods: a Windows host's
// processors, reached over WinRM. This file is "win.cpu.topology": which
// logical processors are performance cores and which efficiency cores.
package cpu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wincpu"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "win.cpu.topology",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("WindowsShellCapable"),
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
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Reading is not changing: this reports the host's processors and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports a Windows host's logical processors: which are performance cores, which efficiency cores, and how they group.",
				Description: "Reads the host's CPU sets as its scheduler sees them, through the Windows API GetSystemCpuSetInformation, and changes nothing. Each logical processor is reported with its processor group, core, NUMA node, last-level cache and efficiency class. On a hybrid processor, such as Intel's with P-cores and E-cores, a higher efficiency class is a faster, less power-efficient core: the logical processors with the highest class are reported as performance processors and the rest as efficiency processors, with the affinity mask that selects the performance ones. On a processor whose cores are all alike, every logical processor is a performance one. No WMI class reports efficiency classes, so the host compiles a small call to the API with PowerShell's Add-Type; a host whose PowerShell runs in Constrained Language Mode refuses that, and the task fails saying so. A check is the same read.",
				Params: []collection.Param{
					{Name: "timeout", Type: "int", Default: "120", Description: "How many seconds the read may take."},
				},
				Returns: []collection.ReturnField{
					{Name: "processors", Type: "list", Returned: "always", Description: "Each logical processor as index, group, core, numa_node, last_level_cache, efficiency_class and parked, in the order Windows lists them."},
					{Name: "processor_count", Type: "int", Returned: "always", Description: "How many logical processors the host has."},
					{Name: "core_count", Type: "int", Returned: "always", Description: "How many cores they are on."},
					{Name: "hybrid", Type: "bool", Returned: "always", Description: "Whether the cores are of more than one efficiency class."},
					{Name: "performance_processors", Type: "list", Returned: "always", Description: "The indexes of the logical processors of the highest efficiency class: every one, when the host is not hybrid."},
					{Name: "efficiency_processors", Type: "list", Returned: "always", Description: "The indexes of the rest, empty when the host is not hybrid."},
					{Name: "performance_affinity_mask", Type: "string", Returned: "when the host has one processor group", Description: "The affinity mask selecting the performance processors, as hexadecimal, such as 0xC03C3: what Windows' start /affinity and a process's ProcessorAffinity take."},
				},
				Examples: []collection.Example{
					{Name: "Find a host's performance cores", RunbookYAML: "- name: Which of the lab host's cores are P-cores\n  win.cpu.topology: {}\n  register: cpus\n"},
				},
				SeeAlso: []string{"facts.gather", "virt.vbox.vm.start"},
			},
		},
		Invoke: Topology,
		Check:  Topology,
	})
}

// The stats the topology is reported under.
const (
	statProcessors              = "processors"
	statProcessorCount          = "processor_count"
	statCoreCount               = "core_count"
	statHybrid                  = "hybrid"
	statPerformanceProcessors   = "performance_processors"
	statEfficiencyProcessors    = "efficiency_processors"
	statPerformanceAffinityMask = "performance_affinity_mask"
)

// defaultTimeout bounds the read, which compiles its call to the API on
// the host first.
const defaultTimeout = 120

// run reaches the host; a test swaps it for a model.
var run = winrmexec.Run

// Topology implements "win.cpu.topology". A check is the same read.
func Topology(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "win.cpu.topology"
	timeout, set, err := sdk.IntParam(params, "timeout")
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !set {
		timeout = defaultTimeout
	}
	if timeout <= 0 {
		return collection.Result{}, fmt.Errorf("%s: timeout must be a positive number of seconds", fqcn)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", fqcn)
	}
	shell, ok1 := device.(capability.WindowsShellCapable)
	winrm, ok2 := device.(capability.WinRMCapable)
	if !ok1 || !ok2 {
		return collection.Result{}, fmt.Errorf("%s: device %q does not implement %s and %s", fqcn, device.Name(), capability.NameWindowsShell, capability.NameWinRM)
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: time.Duration(timeout) * time.Second, PowerShellPath: shell.PowerShellPath()}, devicetls.For(device))
	res, err := run(ctx, winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}, auth, winrmexec.ShellPowerShell, wincpu.Script, opts)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	switch res.ExitCode {
	case 0:
	case wincpu.ExitConstrained:
		return collection.Result{}, fmt.Errorf("%s: PowerShell on %s runs in %s, which refuses the Add-Type the read needs", fqcn, device.Name(), languageMode(res.Stdout))
	default:
		return collection.Result{}, fmt.Errorf("%s: the host's script exited %d: %s", fqcn, res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	t, err := wincpu.Parse(res.Stdout)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	for _, s := range stats(t) {
		if err := rc.SetStat(s.key, s.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{}, nil
}

// stat is one stat and its value.
type stat struct {
	key   string
	value any
}

// stats is what Topology reports of t, in the order the reference lists it.
func stats(t wincpu.Topology) []stat {
	processors := make([]any, len(t.Processors))
	for i, p := range t.Processors {
		processors[i] = map[string]any{
			"index": p.Index, "group": p.Group, "core": p.Core, "numa_node": p.NUMANode,
			"last_level_cache": p.LastLevelCache, "efficiency_class": p.EfficiencyClass, "parked": p.Parked,
		}
	}
	out := []stat{
		{statProcessors, processors},
		{statProcessorCount, len(t.Processors)},
		{statCoreCount, t.Cores()},
		{statHybrid, t.Hybrid()},
		{statPerformanceProcessors, wincpu.Indexes(t.Performance())},
		{statEfficiencyProcessors, wincpu.Indexes(t.Efficiency())},
	}
	if mask, ok := t.PerformanceMask(); ok {
		out = append(out, stat{statPerformanceAffinityMask, fmt.Sprintf("0x%X", mask)})
	}
	return out
}

// languageMode is the mode the script said PowerShell runs in.
func languageMode(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if mode, ok := strings.CutPrefix(strings.TrimSpace(line), "language="); ok && mode != "" {
			return mode
		}
	}
	return "a restricted language mode"
}

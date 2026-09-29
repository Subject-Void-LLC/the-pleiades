// The "virt.vbox.vm.log" method: the end of a VM's VirtualBox log.
package vm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.log",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("VirtualBoxCapable"),
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
				Notes:      "Reading is not changing: this reads the VM's log and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reads the end of a VM's VirtualBox log, optionally only the lines matching a pattern.",
				Description: "Reads the end of the log VirtualBox writes for a VM's current or last run (VBox.log in its folder's Logs), and reports its last lines, or its last lines matching pattern. The log says what the VM's firmware and VirtualBox did: where a boot stopped, which hypervisor interface the guest used, and why a VM aborted. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the log as a run does.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "lines", Type: "int", Default: "40", Description: "How many lines to report, from the end: 1 to 2000."},
					{Name: "pattern", Type: "string", Description: "Report only lines matching this Go RE2 regular expression, such as EFI|GIM|fatal."},
				},
				Returns: []collection.ReturnField{
					{Name: "lines", Type: "list", Returned: "always", Description: "The log's last lines, or its last lines matching pattern, oldest first. Empty when the VM has never run."},
					{Name: "path", Type: "string", Returned: "always", Description: "The log file's path on the host."},
				},
				Examples: []collection.Example{
					{Name: "See where a VM's firmware stopped", RunbookYAML: "- name: Read the lab VM's firmware lines\n  virt.vbox.vm.log:\n    name: win-lab\n    pattern: \"EFI|debug point\"\n    lines: 20\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.screenshot", "virt.vbox.vm.info"},
			},
		},
		Invoke: Log,
		Check:  Log,
	})
}

// The log method's parameters and stats.
const (
	paramLines   = "lines"
	paramPattern = "pattern"
	statLines    = "lines"
	statPath     = "path"

	// logTail is how much of the end of the log is read: a boot's lines
	// fit in it many times, and a log of a long run is far bigger.
	logTail  = 1 << 20
	maxLines = 2000
)

// Log implements "virt.vbox.vm.log". It only reads, so it is its own
// check.
func Log(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.log"
	n, set, err := sdk.IntParam(params, paramLines)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !set {
		n = 40
	}
	if n < 1 || n > maxLines {
		return collection.Result{}, fmt.Errorf("%s: lines must be 1 to %d, not %d", fqcn, maxLines, n)
	}
	var match *regexp.Regexp
	if p := sdk.StringParam(params, paramPattern); p != "" {
		if match, err = regexp.Compile(p); err != nil {
			return collection.Result{}, fmt.Errorf("%s: pattern: %w", fqcn, err)
		}
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	dir, err := vmDir(m)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	path := dir + `\Logs\VBox.log`
	data, err := h.ReadTail(ctx, path, logTail)
	if err != nil && !errors.Is(err, vboxmanage.ErrNoFile) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statLines, lastLines(data, match, n)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statPath, path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{}, nil
}

// lastLines returns the last n of the log's whole lines that match, or
// of all of them when match is nil. A read that filled logTail began part
// way through a line, which is dropped.
func lastLines(data []byte, match *regexp.Regexp, n int) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(data) >= logTail && len(lines) > 0 {
		lines = lines[1:]
	}
	kept := []string{}
	for _, line := range lines {
		if line != "" && (match == nil || match.MatchString(line)) {
			kept = append(kept, line)
		}
	}
	return kept[max(0, len(kept)-n):]
}

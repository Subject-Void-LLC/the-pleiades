// The "virt.vbox.vm.host_keys" method: a VM's SSH host keys, read from its serial console.
package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cloudinit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.host_keys",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("VirtualBoxCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Reading is not changing: this reads the VM's console log and alters nothing, so there is nothing an undo could restore."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Reads a VM's SSH host keys from what cloud-init printed on its serial console.",
				Description: "Waits for cloud-init to print the VM's SSH host keys on its serial console, which virt.vbox.vm.clone logs to a file on the host, and reports them. They are read over the host's own authenticated connection, not from the network the VM answers SSH on, so they are what a known_hosts file can trust before the first SSH connection: pleiades trust-host <device> --from-console <host> writes them there. The newest complete block is the one read. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the console once, without waiting.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait for the keys."},
				},
				Returns: []collection.ReturnField{
					{Name: "ssh_host_keys", Type: "list", Returned: "always", Description: "Each key as algorithm and base64, the form a known_hosts line holds after its host names. Empty in a check that found none yet."},
				},
				Examples: []collection.Example{
					{Name: "Wait for a new VM's host keys", RunbookYAML: "- name: Wait for the lab VM's first boot\n  virt.vbox.vm.host_keys:\n    name: ubuntu-lab\n  register: keys\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.clone"},
			},
		},
		Invoke: HostKeys,
		Check:  CheckHostKeys,
	})
}

// statHostKeys is the stat the keys are reported under.
const statHostKeys = "ssh_host_keys"

// consoleTail is how much of the console log is read: the end of a first
// boot's output, where cloud-init prints the keys, fits in it many times.
const consoleTail = 1 << 20

// HostKeys implements "virt.vbox.vm.host_keys".
func HostKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runHostKeys(ctx, rc, device, params, collection.ModeExecute)
}

// CheckHostKeys is "virt.vbox.vm.host_keys"'s check: it reads the console
// once, without waiting.
func CheckHostKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runHostKeys(ctx, rc, device, params, collection.ModeCheck)
}

// runHostKeys reads the keys, waiting for them outside a check.
func runHostKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.host_keys"
	timeout, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !set {
		timeout = 300
	}
	if timeout <= 0 {
		return collection.Result{}, fmt.Errorf("%s: timeout must be a positive number of seconds", fqcn)
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	if m.ConsoleLog == "" {
		return collection.Result{}, fmt.Errorf("%s: %q writes its serial console to no file; virt.vbox.vm.clone sets one up", fqcn, name)
	}
	keys, err := waitForKeys(ctx, h, name, m.ConsoleLog, mode == collection.ModeCheck, time.Duration(timeout)*time.Second)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statHostKeys, keys); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{}, nil
}

// waitForKeys reads the console log until it holds a complete block of
// host keys, returning an empty list when once is set and it does not.
// A VM that is not running is not waited for: nothing would print them.
func waitForKeys(ctx context.Context, h vboxmanage.Host, name, log string, once bool, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	for {
		console, err := h.ReadTail(ctx, log, consoleTail)
		if err != nil && !errors.Is(err, vboxmanage.ErrNoFile) {
			return nil, err
		}
		keys, err := cloudinit.HostKeys(string(console))
		if err == nil {
			return keys, nil
		}
		if !errors.Is(err, cloudinit.ErrNoHostKeys) {
			return nil, err
		}
		if once {
			return []string{}, nil
		}
		m, err := h.Machine(ctx, name)
		if err != nil {
			return nil, err
		}
		if m.State != vboxmanage.StateRunning {
			return nil, fmt.Errorf("%q is %s and its console shows no host keys; start it with virt.vbox.vm.start", name, m.State)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%q's console showed no host keys within %s; its log is %s", name, timeout, log)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

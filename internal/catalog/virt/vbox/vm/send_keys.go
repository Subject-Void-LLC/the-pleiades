// The "virt.vbox.vm.send_keys" method: typing into a running VM's console.
package vm

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.send_keys",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "What was typed was acted on by the guest, and no key undoes that in general."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Types into a running VM's console, as its keyboard would.",
				Description: "Types keys into a running VM's console, written as Packer's boot_command writes them: text as it is, a named key between angle brackets (<enter>, <tab>, <esc>, <bs>, <del>, <spacebar>, <up>, <down>, <left>, <right>, <home>, <end>, <pageup>, <pagedown>, <insert>, <f1> to <f12>), and <wait> or <wait5> for a pause of one or five seconds (at most 60). Text is typed with a US keyboard layout and may hold only printable ASCII. It is for a VM with no other way in yet, such as one at a first-boot screen; see what it shows with virt.vbox.vm.screenshot. Never type a secret: text travels on the host's VBoxManage command line, where any process on the host can read it. This cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the keys, and types nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "keys", Type: "string", Required: true, Description: "What to type, as Packer's boot_command writes it, such as <tab><tab><enter>."},
				},
				Returns: []collection.ReturnField{
					{Name: "strokes", Type: "int", Returned: "always", Description: "How many steps were typed, or would be, in a check: each run of text, named key and pause is one."},
				},
				Examples: []collection.Example{
					{Name: "Accept a first-boot screen", RunbookYAML: "- name: Move to the Accept button and press it\n  virt.vbox.vm.send_keys:\n    name: win-lab\n    keys: <tab><tab><enter>\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.screenshot"},
			},
		},
		Invoke: SendKeys,
		Check:  CheckSendKeys,
	})
}

// The send_keys parameter and stat.
const (
	paramKeys   = "keys"
	statStrokes = "strokes"
)

// SendKeys implements "virt.vbox.vm.send_keys".
func SendKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runSendKeys(ctx, rc, device, params, collection.ModeExecute)
}

// CheckSendKeys is "virt.vbox.vm.send_keys"'s check: it reads the VM and
// the keys, and types nothing.
func CheckSendKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runSendKeys(ctx, rc, device, params, collection.ModeCheck)
}

// runSendKeys types the keys into a running VM.
func runSendKeys(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.send_keys"
	written, err := sdk.RequiredStringParam(params, paramKeys)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	strokes, err := vboxmanage.Keys(written)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	if m.State != vboxmanage.StateRunning {
		return collection.Result{}, fmt.Errorf("%s: %q is %s; only a running VM takes keys", fqcn, name, m.State)
	}
	if err := rc.SetStat(statStrokes, len(strokes)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if err := h.Type(ctx, name, strokes); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

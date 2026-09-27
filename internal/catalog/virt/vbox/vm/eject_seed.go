// The "virt.vbox.vm.eject_seed" method: a clone's seed taken out of its DVD drive and deleted after its first boot.
package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.eject_seed",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "The seed file is deleted, and nothing keeps what it held (a hash of the login's password, or a Windows VM's Administrator password), so it cannot be put back; a VM that needs seeding again is made again with virt.vbox.vm.clone."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Takes a VM's seed out of its DVD drive and deletes it, once its first boot has read it.",
				Description: "Makes sure a VM virt.vbox.vm.clone made no longer holds its seed: the cloud-init seed or Windows answer file clone put on its DVD drive and in its folder, which holds a hash of the login's password or, for Windows, the Administrator's password itself. The drive is emptied at once, even while the VM runs and even when the guest has locked it, so the guest can never read the seed again. The seed file is then deleted; but VirtualBox keeps an image locked for as long as the VM that held it runs (measured on the lab host), so on a running VM the file stays until the VM is stopped, the task says so in a warning and reports deleted false, and running it again once the VM is stopped deletes it. Run it once the first boot has read the seed: after virt.vbox.vm.host_keys finds a Linux VM's keys, or after wait.connection reaches a Windows VM. A VM holding no seed, in a drive or on the host, reports no change. The seed cannot be put back. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "ejected", Type: "bool", Returned: "always", Description: "Whether a seed was taken out, or would be, in a check."},
					{Name: "deleted", Type: "bool", Returned: "when a seed was taken out", Description: "Whether the seed file was deleted from the host: false while the VM runs, which keeps it locked."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the VM held its seed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Delete a Windows VM's answer file after its first boot", RunbookYAML: "- name: Take the lab VM's answer file out, now that it has booted\n  virt.vbox.vm.eject_seed:\n    name: win-lab\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.host_keys", "wait.connection"},
			},
		},
		Invoke: EjectSeed,
		Check:  CheckEjectSeed,
	})
}

// The eject's stat, and the key its diff records.
const (
	statEjected = "ejected"
	statDeleted = "deleted"
	diffSeeded  = "seeded"
)

// EjectSeed implements "virt.vbox.vm.eject_seed".
func EjectSeed(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runEjectSeed(ctx, rc, device, params, collection.ModeExecute)
}

// CheckEjectSeed is "virt.vbox.vm.eject_seed"'s check: it reads the VM
// and sends nothing.
func CheckEjectSeed(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runEjectSeed(ctx, rc, device, params, collection.ModeCheck)
}

// runEjectSeed empties every drive holding the VM's seed and deletes it.
func runEjectSeed(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.eject_seed"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	dir, err := vmDir(m)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	seed := dir + `\` + seedFile
	held := holding(m, seed)
	// A seed out of every drive can still be on the host, as a run that
	// failed deleting it leaves it: that is still a seed to delete.
	_, err = h.ReadTail(ctx, seed, 1)
	onHost := err == nil
	if err != nil && !errors.Is(err, vboxmanage.ErrNoFile) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if len(held) == 0 && !onHost {
		return collection.Result{}, recordEject(rc, fqcn, false)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordEject(rc, fqcn, true)
	}
	for _, slot := range held {
		if err := h.EjectDVD(ctx, name, slot); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	err = closeSeed(ctx, h, seed)
	deleted := err == nil
	if err != nil && !(errors.Is(err, vboxmanage.ErrLocked) && m.State == vboxmanage.StateRunning) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !deleted {
		warning := fmt.Sprintf("%s's drive is empty, but VirtualBox keeps the seed image locked while the VM runs, so %s is still on the host; run virt.vbox.vm.eject_seed again once the VM is stopped to delete it", name, seed)
		if err := sdk.RecordWarnings(rc, []string{warning}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := rc.SetStat(statDeleted, deleted); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordEject(rc, fqcn, true)
}

// closeSeed forgets the seed image and deletes it. VirtualBox refuses
// while the image is locked, which a lock taken for a moment lets go of,
// so a lock is waited out a few times; a running VM keeps its images
// locked until it stops (measured 2026-09-27), which no wait outlasts.
func closeSeed(ctx context.Context, h vboxmanage.Host, seed string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = h.CloseDVD(ctx, seed, true); !errors.Is(err, vboxmanage.ErrLocked) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return err
}

// holding returns m's slots holding the image at path, compared without
// regard to case or to which separator VirtualBox wrote.
func holding(m vboxmanage.Machine, path string) []vboxmanage.Slot {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "/", `\`)) }
	var held []vboxmanage.Slot
	for _, s := range m.Slots {
		if norm(s.Medium) == norm(path) {
			held = append(held, s)
		}
	}
	return held
}

// recordEject sets an eject's stat and diff: whether the VM held its seed
// before this task, and that it does not after.
func recordEject(rc sdk.RunbookContext, fqcn string, ejected bool) error {
	if err := rc.SetStat(statEjected, ejected); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{diffSeeded: ejected}, After: map[string]any{diffSeeded: false}}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

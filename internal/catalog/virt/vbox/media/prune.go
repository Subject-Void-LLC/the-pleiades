// Package media implements "virt.vbox.media.prune": deleting what VMs
// VirtualBox no longer knows left behind under the host's machine folder.
// Scaffolded by pleiades forge new-collection from
// internal/forge/catalogdata, then implemented over pkg/vboxmanage, which
// reads the host and decides what goes (vboxmanage.PlanPrune).
package media

import (
	"context"
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
		Name: "virt.vbox.media.prune",
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
			EngineVersion:   ">=0.4.0",
			Status:          collection.StatusImplemented,
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "It deletes disks and folders no registered VM uses, and nothing keeps what was in them, so there is nothing an undo could put back; a check lists what a run would remove before anything is."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Deletes the disks no registered VM uses and the leftover folders of VMs VirtualBox no longer knows, under the host's machine folder.",
				Description: "Removes what a VM VirtualBox forgot leaves behind: a delete that failed partway, or a VM unregistered without its files. Under the host's machine folder (its vm_folder, or VirtualBox's default machine folder when it names none) it finds every disk VirtualBox has registered that no registered VM uses, in its current state or a snapshot, and every folder holding no registered VM's settings and no disk one uses, among the folders that hold a VM's settings file (a .vbox, or the .vbox-prev VirtualBox keeps beside it) and the empty ones a delete leaves; any other folder is never touched. A folder this run's disk deletions leave empty is not in this run's list, so the next run removes it. A disk is deleted with its file, or only forgotten when its file is already gone; one with a descendant that is in use, or that is not being deleted too, is left, and descendants go before their parents. A folder holding a registered VM's files is left. When VirtualBox lists a VM it cannot read, no folder is removed, since nothing can say which folder is that VM's: delete it first with virt.vbox.vm.delete, naming its UUID. Nothing outside the machine folder is touched: a disk VirtualBox names through a \"..\" segment, a UNC path or another drive is never inside it, and no disk at or under a symbolic link, junction or mount point is deleted, nor a folder that is one or holds one, since what a link reaches can be anywhere. A check lists exactly what a run would remove and removes nothing; it is how to see the list without acting on it. Nothing removed can be brought back. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs, disks and folders, and sends nothing.",
				Returns: []collection.ReturnField{
					{Name: "disks", Type: "list", Returned: "always", Description: "Each disk removed, or that a check would remove: its UUID, its file and whether its file was already gone."},
					{Name: "folders", Type: "list", Returned: "always", Description: "Each folder removed, or that a check would remove."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "How many disks and folders there were to remove before this task, and after it."},
				},
				Examples: []collection.Example{
					{Name: "See what would be removed, then remove it", RunbookYAML: "- name: Remove the disks and folders no VM uses\n  virt.vbox.media.prune: {}\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.delete", "virt.vbox.vm.list"},
			},
		},
		Invoke: Prune,
		Check:  CheckPrune,
	})
}

// hostFunc builds the VirtualBox host a task targets from its device and
// credential; a test swaps it for the model host.
var hostFunc = func(rc sdk.RunbookContext, device inventory.InventoryItem) (vboxmanage.Host, error) {
	return vboxmanage.ForDevice(device, rc.InjectSecrets(), callTimeout)
}

// callTimeout bounds each VBoxManage call, as the vm methods' does.
const callTimeout = 5 * time.Minute

// The prune's stats and the keys its diff records.
const (
	statDisks   = "disks"
	statFolders = "folders"
)

// Prune implements "virt.vbox.media.prune".
func Prune(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runPrune(ctx, rc, device, params, collection.ModeExecute)
}

// CheckPrune is "virt.vbox.media.prune"'s check: it lists what a run would
// remove and sends nothing.
func CheckPrune(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runPrune(ctx, rc, device, params, collection.ModeCheck)
}

// runPrune reads the host, plans with vboxmanage.PlanPrune, and in a run
// removes exactly the plan: each disk, descendants first, then each folder.
func runPrune(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.media.prune"
	if len(params) > 0 {
		return collection.Result{}, fmt.Errorf("%s: takes no parameters; a check lists what a run would remove", fqcn)
	}
	h, err := hostFunc(rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	plan, err := planFor(ctx, h, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := record(rc, plan); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	changed := len(plan.Disks)+len(plan.Folders) > 0
	if mode == collection.ModeCheck || !changed {
		return collection.Result{Changed: changed}, nil
	}
	for _, d := range plan.Disks {
		if err := h.CloseDisk(ctx, d.UUID, !d.Gone); err != nil {
			return collection.Result{}, fmt.Errorf("%s: removing disk %s (%s): %w", fqcn, d.UUID, d.Location, err)
		}
	}
	for _, f := range plan.Folders {
		if err := h.RemoveFolder(ctx, f); err != nil {
			return collection.Result{}, fmt.Errorf("%s: removing folder %s: %w", fqcn, f, err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// planFor reads what PlanPrune decides from: the machine folder (the
// host's vm_folder, or VirtualBox's default), the registered disks, the
// settings files of the VMs VirtualBox can read, the folders inside the
// machine folder, and the links under it.
func planFor(ctx context.Context, h vboxmanage.Host, device inventory.InventoryItem) (vboxmanage.PrunePlan, error) {
	in := vboxmanage.PruneInput{}
	if vbox, ok := device.(capability.VirtualBoxCapable); ok {
		in.Root = vbox.VMFolder()
	}
	var err error
	if in.Root == "" {
		if in.Root, err = h.MachineFolder(ctx); err != nil {
			return vboxmanage.PrunePlan{}, err
		}
	}
	// A drive's root is never a machine folder to prune: every folder on
	// the drive would be in reach.
	if trimmed := strings.TrimRight(in.Root, `\/`); len(trimmed) <= 2 {
		return vboxmanage.PrunePlan{}, fmt.Errorf("the machine folder %q is a drive's root; give the host a vm_folder of its own", in.Root)
	}
	if in.Media, err = h.Media(ctx); err != nil {
		return vboxmanage.PrunePlan{}, err
	}
	refs, err := h.List(ctx)
	if err != nil {
		return vboxmanage.PrunePlan{}, err
	}
	for _, ref := range refs {
		if ref.Inaccessible() {
			in.Unreadable = true
			continue
		}
		m, err := h.Machine(ctx, ref.UUID)
		if err != nil {
			return vboxmanage.PrunePlan{}, err
		}
		in.Configs = append(in.Configs, m.ConfigFile)
	}
	if in.Folders, err = h.MachineFolders(ctx, in.Root); err != nil {
		return vboxmanage.PrunePlan{}, err
	}
	// What a link under the machine folder reaches can be anywhere, so the
	// plan keeps everything at or under one (vboxmanage.PlanPrune).
	if in.Links, err = h.Links(ctx, in.Root); err != nil {
		return vboxmanage.PrunePlan{}, err
	}
	return vboxmanage.PlanPrune(in), nil
}

// record sets the prune's stats and diff: what it removes, and how many
// disks and folders there were to remove before it and after.
func record(rc sdk.RunbookContext, plan vboxmanage.PrunePlan) error {
	disks := make([]any, 0, len(plan.Disks))
	for _, d := range plan.Disks {
		disks = append(disks, map[string]any{"uuid": d.UUID, "location": d.Location, "gone": d.Gone})
	}
	folders := make([]any, 0, len(plan.Folders))
	for _, f := range plan.Folders {
		folders = append(folders, f)
	}
	if err := rc.SetStat(statDisks, disks); err != nil {
		return err
	}
	if err := rc.SetStat(statFolders, folders); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{
		Before: map[string]any{statDisks: len(plan.Disks), statFolders: len(plan.Folders)},
		After:  map[string]any{statDisks: 0, statFolders: 0},
	})
}

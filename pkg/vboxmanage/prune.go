// Finding what VMs VirtualBox no longer knows left behind under the
// machine folder: disks no registered VM uses, and folders holding no
// registered VM's files. PlanPrune decides from what the host reported;
// the Host methods here read and remove.
package vboxmanage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// The first line of the folder scripts, which names what each does.
const (
	tagFolders      = "# vboxmanage: folders"
	tagRemoveFolder = "# vboxmanage: remove folder"
)

// StateInaccessible is the state list --long hdds gives a disk whose file
// VirtualBox cannot open, such as one deleted behind its back.
const StateInaccessible = "inaccessible"

// PruneDisk is a disk a prune removes.
type PruneDisk struct {
	UUID     string
	Location string
	// Gone says its file is already gone, so it is only forgotten.
	Gone bool
}

// PrunePlan is what a prune removes: disks in the order they go, each
// before its parent, and folders.
type PrunePlan struct {
	Disks   []PruneDisk
	Folders []string
}

// PruneInput is what PlanPrune decides from, as the host reported it.
type PruneInput struct {
	// Root is the machine folder; nothing outside it is removed.
	Root string
	// Media are the registered disks (Host.Media).
	Media []Medium
	// Configs are the settings files of the registered VMs VirtualBox can
	// read.
	Configs []string
	// Folders are the machine folders directly inside Root
	// (Host.MachineFolders).
	Folders []string
	// Unreadable says VirtualBox lists a VM it cannot read, whose folder
	// nothing can name, so no folder is removed.
	Unreadable bool
}

// normPath is path compared without regard to case or to which separator
// VirtualBox wrote.
func normPath(p string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, "/", `\`)), `\`)
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	d, p := normPath(dir), normPath(path)
	return d != "" && (p == d || strings.HasPrefix(p, d+`\`))
}

// PlanPrune decides what a prune removes. A disk goes when it is under
// Root, no registered VM uses it, it is not in a registered VM's folder
// (virt.vbox.vm.detach_disk keeps one there on purpose), and every disk
// descending from it goes too. A folder goes when it holds no registered
// VM's settings and no disk that stays, and only when no VM is unreadable.
func PlanPrune(in PruneInput) PrunePlan {
	vmDirs := make([]string, 0, len(in.Configs))
	for _, c := range in.Configs {
		if i := strings.LastIndexAny(c, `\/`); i > 0 {
			vmDirs = append(vmDirs, c[:i])
		}
	}
	inVMDir := func(p string) bool {
		for _, d := range vmDirs {
			if within(d, p) {
				return true
			}
		}
		return false
	}
	children := map[string][]Medium{}
	for _, m := range in.Media {
		if m.Parent != "" {
			children[strings.ToLower(m.Parent)] = append(children[strings.ToLower(m.Parent)], m)
		}
	}
	// removable is memoized, and a disk being decided counts as staying, so
	// a parent loop VirtualBox would never write ends.
	decided := map[string]bool{}
	deciding := map[string]bool{}
	var removable func(m Medium) bool
	removable = func(m Medium) bool {
		id := strings.ToLower(m.UUID)
		if v, ok := decided[id]; ok {
			return v
		}
		if deciding[id] {
			return false
		}
		deciding[id] = true
		ok := len(m.Users) == 0 && within(in.Root, m.Location) && normPath(m.Location) != normPath(in.Root) && !inVMDir(m.Location)
		for _, c := range children[id] {
			if !removable(c) {
				ok = false
			}
		}
		delete(deciding, id)
		decided[id] = ok
		return ok
	}
	var plan PrunePlan
	added := map[string]bool{}
	// Post-order: a disk's descendants are listed before it.
	var add func(m Medium)
	add = func(m Medium) {
		id := strings.ToLower(m.UUID)
		if added[id] {
			return
		}
		added[id] = true
		for _, c := range children[id] {
			add(c)
		}
		plan.Disks = append(plan.Disks, PruneDisk{UUID: m.UUID, Location: m.Location, Gone: m.State == StateInaccessible})
	}
	staying := []string{}
	for _, m := range in.Media {
		if removable(m) {
			// Only a disk whose parent stays starts a walk, so each chain is
			// listed once, from its top.
			if parent, ok := mediumByUUID(in.Media, m.Parent); !ok || !removable(parent) {
				add(m)
			}
		} else {
			staying = append(staying, m.Location)
		}
	}
	if in.Unreadable {
		return plan
	}
	for _, f := range in.Folders {
		if !within(in.Root, f) || normPath(f) == normPath(in.Root) {
			continue
		}
		keep := false
		for _, c := range in.Configs {
			keep = keep || within(f, c)
		}
		for _, s := range staying {
			keep = keep || within(f, s)
		}
		if !keep {
			plan.Folders = append(plan.Folders, f)
		}
	}
	sort.Strings(plan.Folders)
	return plan
}

// mediumByUUID finds the disk with uuid.
func mediumByUUID(media []Medium, uuid string) (Medium, bool) {
	for _, m := range media {
		if uuid != "" && strings.EqualFold(m.UUID, uuid) {
			return m, true
		}
	}
	return Medium{}, false
}

// MachineFolder returns the account's default machine folder, as list
// systemproperties names it.
func (h Host) MachineFolder(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "list", "systemproperties")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "Default machine folder:"); ok {
			if folder := strings.TrimSpace(v); folder != "" {
				return folder, nil
			}
		}
	}
	return "", errors.New("vboxmanage: list systemproperties names no default machine folder")
}

// MachineFolders returns the folders directly inside root that hold a
// VM's settings file (a .vbox, or the .vbox-prev VirtualBox keeps beside
// it), or nothing at all, as a delete that removed a VM's disks but not
// its folder leaves one, as full paths; a root that is not there has none.
// Only such a folder is ever a prune's to remove, so a folder of anything
// else under the machine folder is never touched.
func (h Host) MachineFolders(ctx context.Context, root string) ([]string, error) {
	if err := CheckPath("folder", root); err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = %s
if (-not (Test-Path -LiteralPath $path -PathType Container)) { exit 3 }
Get-ChildItem -LiteralPath $path -Directory -Force | Where-Object { (Get-ChildItem -LiteralPath $_.FullName -File -Force | Where-Object { $_.Name -like '*.vbox' -or $_.Name -like '*.vbox-prev' } | Select-Object -First 1) -or -not (Get-ChildItem -LiteralPath $_.FullName -Force | Select-Object -First 1) } | ForEach-Object { $_.FullName }
`, tagFolders, winrmexec.QuotePS(root))
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	if out.ExitCode == 3 {
		return nil, nil
	}
	if out.ExitCode != 0 {
		return nil, fmt.Errorf("vboxmanage: listing %s: %s", root, firstErrorLine(out))
	}
	var folders []string
	for _, line := range strings.Split(out.Stdout, "\n") {
		if f := strings.TrimSpace(line); f != "" {
			folders = append(folders, f)
		}
	}
	return folders, nil
}

// RemoveFolder deletes the folder at path and everything in it. A folder
// that is not there is not an error.
func (h Host) RemoveFolder(ctx context.Context, path string) error {
	if err := CheckPath("folder", path); err != nil {
		return err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = %s
if (Test-Path -LiteralPath $path -PathType Container) { Remove-Item -LiteralPath $path -Recurse -Force }
"removed"
`, tagRemoveFolder, winrmexec.QuotePS(path))
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("vboxmanage: removing %s: %s", path, firstErrorLine(out))
	}
	return nil
}

// CloseDisk forgets the disk with uuid, deleting its file too when remove
// is true. VirtualBox refuses a disk a machine uses or one with children.
func (h Host) CloseDisk(ctx context.Context, uuid string, remove bool) error {
	if err := CheckUUID(uuid); err != nil {
		return err
	}
	args := []string{"closemedium", "disk", uuid}
	if remove {
		args = append(args, "--delete")
	}
	_, err := h.run(ctx, args...)
	return err
}

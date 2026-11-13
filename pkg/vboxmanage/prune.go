// Finding what VMs VirtualBox no longer knows left behind under the
// machine folder: disks no registered VM uses, and folders holding no
// registered VM's files. PlanPrune decides from what the host reported;
// the Host methods in prunehost.go read and remove, and hostpath.go holds
// how paths are compared.
package vboxmanage

import (
	"sort"
	"strings"
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
	// Links are the symbolic links, junctions and mount points under Root
	// (Host.Links). What one reaches can be anywhere, so no disk at or under
	// one is removed, and no folder that is one or holds one.
	Links []string
}

// PlanPrune decides what a prune removes. A disk goes when it is under
// Root, no registered VM uses it, it is not in a registered VM's folder
// (virt.vbox.vm.detach_disk keeps one there on purpose), it is not at or
// under a link, and every disk descending from it goes too. A folder goes
// when it holds no registered VM's settings, no disk that stays and no
// link, and only when no VM is unreadable. A path normPath refuses is never
// under Root, so neither is anything VirtualBox names with "..", a UNC
// path or another drive.
func PlanPrune(in PruneInput) PrunePlan {
	vmDirs := make([]string, 0, len(in.Configs))
	for _, c := range in.Configs {
		dir := ""
		if i := strings.LastIndexAny(c, `\/`); i > 0 {
			dir = c[:i]
		}
		// A settings file with no folder that can be named keeps every disk
		// (mayHold), as an unreadable VM keeps every folder.
		vmDirs = append(vmDirs, dir)
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
		ok := len(m.Users) == 0 && within(in.Root, m.Location) && !samePath(m.Location, in.Root) &&
			!anyHolds(vmDirs, m.Location) && !anyHolds(in.Links, m.Location)
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
		if !within(in.Root, f) || samePath(f, in.Root) {
			continue
		}
		// A folder holding a registered VM's settings, a disk that stays, or a
		// link (or that is one, whose removal would reach whatever it names)
		// is kept.
		if !holdsAny(f, in.Configs) && !holdsAny(f, staying) && !holdsAny(f, in.Links) {
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

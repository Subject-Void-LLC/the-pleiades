// The model's answer to list --long hdds: each machine's disks, a linked
// clone's disk a differencing image of the disk its source was cloned
// from, in the form the lab host wrote (pkg/vboxmanage's ParseMedia).
package vboxmanagetest

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// mediumUUID is the model's UUID for the disk at path: the same path
// always gets the same one, so a clone's parent names its source's disk.
func mediumUUID(path string) string {
	sum := uint64(14695981039346656037)
	for _, c := range []byte(strings.ToLower(path)) {
		sum = (sum ^ uint64(c)) * 1099511628211
	}
	return fmt.Sprintf("00000000-0000-4000-a000-%012x", sum&0xffffffffffff)
}

// diskPaths are a machine's hard disks: its slots holding a VDI or VMDK.
func diskPaths(vm *VM) []string {
	var paths []string
	for _, s := range vm.Slots {
		if lower := strings.ToLower(s.Medium); strings.HasSuffix(lower, ".vdi") || strings.HasSuffix(lower, ".vmdk") {
			paths = append(paths, s.Medium)
		}
	}
	return paths
}

// listMedia answers list --long hdds. A machine's disks are attached to
// it; a linked clone's disk descends from its source's first disk, which
// is listed even when the source is inaccessible, as VirtualBox lists a
// parent it knows from the clone's own registry, then with no machine
// using it.
func (h *Host) listMedia() vboxmanage.Output {
	type medium struct {
		parent, location string
		users            []*VM
	}
	var order []string
	media := map[string]*medium{}
	add := func(path, parent string, user *VM) {
		id := mediumUUID(path)
		m, ok := media[id]
		if !ok {
			m = &medium{parent: parent, location: path}
			media[id] = m
			order = append(order, id)
		}
		if parent != "" {
			m.parent = parent
		}
		if user != nil {
			m.users = append(m.users, user)
		}
	}
	for _, vm := range h.vms {
		parent := ""
		if source := h.find(vm.LinkedFrom); vm.LinkedFrom != "" && source != nil {
			if disks := diskPaths(source); len(disks) > 0 {
				parent = mediumUUID(disks[0])
				var user *VM
				if !source.Inaccessible {
					user = source
				}
				add(disks[0], "", user)
			}
		}
		if vm.Inaccessible {
			continue
		}
		for _, path := range diskPaths(vm) {
			add(path, parent, vm)
		}
	}
	var b strings.Builder
	for _, id := range order {
		m := media[id]
		parent, kind := "base", "normal (base)"
		if m.parent != "" {
			parent, kind = m.parent, "normal (differencing)"
		}
		fmt.Fprintf(&b, "UUID:           %s\r\nParent UUID:    %s\r\nState:          created\r\nType:           %s\r\nLocation:       %s\r\nStorage format: VDI\r\n", id, parent, kind, m.location)
		for i, u := range m.users {
			label := "In use by VMs:  "
			if i > 0 {
				label = strings.Repeat(" ", len(label))
			}
			fmt.Fprintf(&b, "%s%s (UUID: %s)\r\n", label, u.Name, u.UUID)
		}
		b.WriteString("\r\n")
	}
	return vboxmanage.Output{Stdout: b.String()}
}

// The model's snapshots: taking, restoring and deleting them, and writing
// the tree as showvminfo numbers it.
package vboxmanagetest

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// snapshot answers snapshot take, restore and delete. The wording of a
// refusal, other than a machine not found, is the model's own.
func (h *Host) snapshot(name, action, what string, rest []string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if action == "take" {
		description := ""
		if len(rest) == 2 && rest[0] == "--description" {
			description = rest[1]
		} else if len(rest) != 0 {
			return errorOutput("the model does not answer snapshot take " + strings.Join(rest, " "))
		}
		uuid := h.newUUID()
		vm.Snapshots = append(vm.Snapshots, Snapshot{Name: what, UUID: uuid, Description: description, Parent: vm.Current})
		vm.Current = uuid
		return vboxmanage.Output{Stdout: "Snapshot taken. UUID: " + uuid + "\r\n", Stderr: progress}
	}
	if len(rest) != 0 {
		return errorOutput("the model does not answer snapshot " + action + " " + strings.Join(rest, " "))
	}
	s := vm.snapshot(what)
	if s == nil {
		return errorOutput(fmt.Sprintf("Could not find a snapshot with UUID {%s}", what),
			"Details: code VBOX_E_OBJECT_NOT_FOUND (0x80bb0001), component MachineWrap, interface IMachine")
	}
	switch action {
	case "restore":
		if !vm.off() {
			return locked(vm.Name)
		}
		vm.Current = s.UUID
		vm.State = vboxmanage.StatePoweroff
		return vboxmanage.Output{Stdout: fmt.Sprintf("Restoring snapshot '%s' (%s)\r\n", s.Name, s.UUID), Stderr: progress}
	case "delete":
		var children int
		for _, c := range vm.Snapshots {
			if c.Parent == s.UUID {
				children++
			}
		}
		if children > 1 {
			return errorOutput(fmt.Sprintf("Snapshot '%s' of the machine '%s' has more than one child snapshot (%d)", s.Name, vm.Name, children),
				"Details: code VBOX_E_INVALID_OBJECT_STATE (0x80bb0007), component SessionMachine, interface IMachine")
		}
		gone := *s
		kept := vm.Snapshots[:0]
		for _, c := range vm.Snapshots {
			if c.UUID == gone.UUID {
				continue
			}
			if c.Parent == gone.UUID {
				c.Parent = gone.Parent
			}
			kept = append(kept, c)
		}
		vm.Snapshots = kept
		if vm.Current == gone.UUID {
			vm.Current = gone.Parent
		}
		return vboxmanage.Output{Stdout: fmt.Sprintf("Deleting snapshot '%s' (%s)\r\n", gone.Name, gone.UUID), Stderr: progress}
	}
	return errorOutput("the model does not answer snapshot " + action)
}

// writeSnapshots writes the snapshot tree under parent, numbered as
// VBoxManage numbers it: "" for the root, "-1" for its first child, "-1-2"
// for that child's second, then the current snapshot's lines.
func (vm *VM) writeSnapshots(b *strings.Builder, parent, path string) {
	number := 0
	for _, s := range vm.Snapshots {
		if s.Parent != parent {
			continue
		}
		p := path
		if parent != "" {
			number++
			p = fmt.Sprintf("%s-%d", path, number)
		}
		fmt.Fprintf(b, "SnapshotName%s=%s\r\n", p, quote(s.Name))
		fmt.Fprintf(b, "SnapshotUUID%s=%s\r\n", p, quote(s.UUID))
		if s.Description != "" {
			fmt.Fprintf(b, "SnapshotDescription%s=%s\r\n", p, quote(s.Description))
		}
		if s.UUID == vm.Current {
			vm.currentNode = "SnapshotName" + p
		}
		vm.writeSnapshots(b, s.UUID, p)
	}
	if parent == "" && vm.Current != "" {
		current := vm.snapshot(vm.Current)
		fmt.Fprintf(b, "CurrentSnapshotName=%s\r\n", quote(current.Name))
		fmt.Fprintf(b, "CurrentSnapshotUUID=%s\r\n", quote(current.UUID))
		fmt.Fprintf(b, "CurrentSnapshotNode=%s\r\n", quote(vm.currentNode))
	}
}

// snapshot returns the snapshot with uuid, or nil.
func (vm *VM) snapshot(uuid string) *Snapshot {
	for i := range vm.Snapshots {
		if strings.EqualFold(vm.Snapshots[i].UUID, uuid) {
			return &vm.Snapshots[i]
		}
	}
	return nil
}

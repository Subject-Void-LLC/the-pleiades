// Sizing a VM: reading a task's size, or its memory and CPUs, and refusing
// a VM the host cannot hold.
package vm

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmsize"
)

// The sizing parameters and the stat a VM's size is reported under.
const (
	paramSize     = "size"
	paramMemoryMB = "memory_mb"
	paramCPUs     = "cpus"

	statSize = "size"
)

// hostReserveMB is the memory a start leaves the host for itself.
const hostReserveMB = 1024

// shape is a VM's memory and CPUs.
type shape struct {
	memoryMB, cpus int
}

// shapeOf is m's shape.
func shapeOf(m vboxmanage.Machine) shape {
	return shape{memoryMB: m.MemoryMB, cpus: m.CPUs}
}

// size is the shape's T-shirt size, or "" when it is none.
func (s shape) size() string {
	if named, ok := vmsize.Of(s.cpus, s.memoryMB); ok {
		return named.Name
	}
	return ""
}

// String names the shape as a message does: its size when it has one.
func (s shape) String() string {
	if named, ok := vmsize.Of(s.cpus, s.memoryMB); ok {
		return named.String()
	}
	cpus := "CPUs"
	if s.cpus == 1 {
		cpus = "CPU"
	}
	return fmt.Sprintf("%d %s and %d MB", s.cpus, cpus, s.memoryMB)
}

// view is the shape as a diff or stat records it.
func (s shape) view() map[string]any {
	return map[string]any{statMemoryMB: s.memoryMB, statCPUs: s.cpus, statSize: s.size()}
}

// readShape reads a task's size, or its memory_mb and cpus, over base:
// either number left out keeps base's. asked reports whether the task
// named any of the three. A size beside either number is refused, since
// the one would silently overrule the other.
func readShape(params map[string]any, base shape) (s shape, asked bool, err error) {
	memoryMB, memorySet, err := sdk.IntParam(params, paramMemoryMB)
	if err != nil {
		return s, false, err
	}
	cpus, cpusSet, err := sdk.IntParam(params, paramCPUs)
	if err != nil {
		return s, false, err
	}
	if raw, present := params[paramSize]; present && raw != nil {
		name, _ := raw.(string)
		if name == "" {
			return s, false, fmt.Errorf("size must be one of the sizes' names, not %v", raw)
		}
		if memorySet || cpusSet {
			return s, false, fmt.Errorf("size names the memory and CPUs itself; give size, or memory_mb and cpus, not both")
		}
		named, err := vmsize.Lookup(name)
		if err != nil {
			return s, false, err
		}
		return shape{memoryMB: named.MemoryMB, cpus: named.CPUs}, true, nil
	}
	s = base
	if memorySet {
		if memoryMB <= 0 {
			return s, false, fmt.Errorf("memory_mb must be positive")
		}
		s.memoryMB = memoryMB
	}
	if cpusSet {
		if cpus <= 0 {
			return s, false, fmt.Errorf("cpus must be positive")
		}
		s.cpus = cpus
	}
	return s, memorySet || cpusSet, nil
}

// fitHost refuses a shape the host could never run: more CPUs than it has
// processors online, or more memory than it has at all.
func fitHost(ctx context.Context, h vboxmanage.Host, s shape) error {
	info, err := h.HostInfo(ctx)
	if err != nil {
		return err
	}
	if s.cpus > info.CPUs {
		return fmt.Errorf("%s is more than the host can run: it has %d processors online", s, info.CPUs)
	}
	if s.memoryMB > info.MemoryMB {
		return fmt.Errorf("%s is more than the host can run: it has %d MB of memory", s, info.MemoryMB)
	}
	return nil
}

// fitsNow refuses to start m when the host's free memory, less what it
// keeps for itself, cannot hold it.
func fitsNow(ctx context.Context, h vboxmanage.Host, m vboxmanage.Machine) error {
	info, err := h.HostInfo(ctx)
	if err != nil {
		return err
	}
	if room := info.AvailableMB - hostReserveMB; m.MemoryMB > room {
		return fmt.Errorf("%q needs %d MB and the host has %d MB free, of which it keeps %d MB for itself; stop another VM, or make this one smaller with virt.vbox.vm.resize", m.Name, m.MemoryMB, info.AvailableMB, hostReserveMB)
	}
	return nil
}

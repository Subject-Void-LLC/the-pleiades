// What the host has to give its machines: its processors and its memory,
// as list hostinfo reports them.
package vboxmanage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// HostInfo is the host's processors and memory.
type HostInfo struct {
	// CPUs is how many logical processors the host has online.
	CPUs int
	// MemoryMB is the host's physical memory, and AvailableMB how much of
	// it was free when it was read, in megabytes.
	MemoryMB, AvailableMB int
}

// hostInfoFields are the lines HostInfo reads, and where each goes.
var hostInfoFields = []struct {
	key  string
	unit string
	set  func(*HostInfo, int)
}{
	{"Processor online count", "", func(i *HostInfo, n int) { i.CPUs = n }},
	{"Memory size", " MByte", func(i *HostInfo, n int) { i.MemoryMB = n }},
	{"Memory available", " MByte", func(i *HostInfo, n int) { i.AvailableMB = n }},
}

// HostInfo reads the host's processors and memory. An answer missing any
// of them is refused rather than read as zero.
func (h Host) HostInfo(ctx context.Context) (HostInfo, error) {
	out, err := h.run(ctx, "list", "hostinfo")
	if err != nil {
		return HostInfo{}, err
	}
	return ParseHostInfo(out.Stdout)
}

// ParseHostInfo reads list hostinfo's answer.
func ParseHostInfo(text string) (HostInfo, error) {
	lines := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ": ")
		if ok {
			lines[key] = value
		}
	}
	var info HostInfo
	for _, f := range hostInfoFields {
		value, ok := lines[f.key]
		if !ok {
			return HostInfo{}, fmt.Errorf("vboxmanage: list hostinfo gave no %q line", f.key)
		}
		n, err := strconv.Atoi(strings.TrimSuffix(value, f.unit))
		if err != nil || n < 0 || (f.unit != "" && !strings.HasSuffix(value, f.unit)) {
			return HostInfo{}, fmt.Errorf("vboxmanage: list hostinfo's %q is %q, not a count%s", f.key, value, f.unit)
		}
		f.set(&info, n)
	}
	if info.CPUs == 0 || info.MemoryMB == 0 {
		return HostInfo{}, fmt.Errorf("vboxmanage: list hostinfo says the host has %d processors and %d MB", info.CPUs, info.MemoryMB)
	}
	if info.AvailableMB > info.MemoryMB {
		return HostInfo{}, fmt.Errorf("vboxmanage: list hostinfo says %d MB of %d MB is available", info.AvailableMB, info.MemoryMB)
	}
	return info, nil
}

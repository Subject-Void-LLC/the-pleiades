// Package vmsize names virtual machine shapes the way a cloud names its
// instance types, as T-shirt sizes: xsmall, small, medium, large and
// xlarge. A size is a CPU count and a memory size together, the same on
// every hypervisor, so a runbook that asks for a medium machine gets the
// same machine whichever Collection makes it.
//
// A method takes a size in place of the two numbers, and reports one for
// a machine whose numbers are exactly a size's. A machine given other
// numbers has no size, which is an answer rather than a failure.
package vmsize

import (
	"fmt"
	"strings"
)

// Size is one named shape.
type Size struct {
	Name     string
	CPUs     int
	MemoryMB int
}

// String is the size as a person reads it: "medium (2 CPUs, 4096 MB)".
func (s Size) String() string {
	cpus := "CPUs"
	if s.CPUs == 1 {
		cpus = "CPU"
	}
	return fmt.Sprintf("%s (%d %s, %d MB)", s.Name, s.CPUs, cpus, s.MemoryMB)
}

// sizes are every size, smallest first. Each doubles the memory of the
// one before it, and from medium on the CPUs too.
var sizes = []Size{
	{Name: "xsmall", CPUs: 1, MemoryMB: 1024},
	{Name: "small", CPUs: 1, MemoryMB: 2048},
	{Name: "medium", CPUs: 2, MemoryMB: 4096},
	{Name: "large", CPUs: 4, MemoryMB: 8192},
	{Name: "xlarge", CPUs: 8, MemoryMB: 16384},
}

// All returns every size, smallest first.
func All() []Size {
	return append([]Size(nil), sizes...)
}

// Names returns every size's name, smallest first.
func Names() []string {
	names := make([]string, len(sizes))
	for i, s := range sizes {
		names[i] = s.Name
	}
	return names
}

// Describe lists every size and what it is, smallest first, as a
// method's documentation names them: "xsmall is 1 CPU and 1024 MB, small
// 1 CPU and 2048 MB, ..., and xlarge 8 CPUs and 16384 MB".
func Describe() string {
	parts := make([]string, len(sizes))
	for i, s := range sizes {
		cpus := "CPUs"
		if s.CPUs == 1 {
			cpus = "CPU"
		}
		parts[i] = fmt.Sprintf("%s %d %s and %d MB", s.Name, s.CPUs, cpus, s.MemoryMB)
	}
	parts[0] = strings.Replace(parts[0], " ", " is ", 1)
	parts[len(parts)-1] = "and " + parts[len(parts)-1]
	return strings.Join(parts, ", ")
}

// Lookup returns the size called name. Any other name is refused with
// the list of those there are; there are no abbreviations.
func Lookup(name string) (Size, error) {
	for _, s := range sizes {
		if s.Name == name {
			return s, nil
		}
	}
	return Size{}, fmt.Errorf("size %q is not one of %s", name, strings.Join(Names(), ", "))
}

// Of returns the size whose CPUs and memory are exactly cpus and
// memoryMB, and false when no size is.
func Of(cpus, memoryMB int) (Size, bool) {
	for _, s := range sizes {
		if s.CPUs == cpus && s.MemoryMB == memoryMB {
			return s, true
		}
	}
	return Size{}, false
}

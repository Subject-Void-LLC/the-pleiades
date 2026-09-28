// Package wincpu reads a Windows host's logical processors as its
// scheduler sees them, from the Windows API GetSystemCpuSetInformation:
// each one's processor group, core, NUMA node, last-level cache and
// efficiency class, and from those, which are a hybrid processor's
// performance cores and the affinity mask that selects them.
//
// The host runs Script and this package reads what it prints; the
// arithmetic is here, where it is tested, not in the script.
package wincpu

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Script is the PowerShell that prints the host's CPU sets, one line each:
//
//	cpuset lp=0 group=0 core=0 llc=0 numa=0 class=1 flags=0
//
// No WMI class reports an efficiency class, so it compiles a call to the
// API with Add-Type. PowerShell in any language mode but FullLanguage
// refuses Add-Type; the script then prints its mode and exits with
// ExitConstrained rather than failing on the compile.
const Script = `# wincpu: cpusets
$ErrorActionPreference = 'Stop'
$mode = $ExecutionContext.SessionState.LanguageMode
if ($mode -ne 'FullLanguage') { "language=$mode"; exit 20 }
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
public static class PleiadesCpuSets {
  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool GetSystemCpuSetInformation(IntPtr info, uint length, out uint returned, IntPtr process, uint flags);
  public static string Read() {
    uint length;
    GetSystemCpuSetInformation(IntPtr.Zero, 0, out length, IntPtr.Zero, 0);
    if (length == 0) throw new Win32Exception(Marshal.GetLastWin32Error());
    IntPtr buffer = Marshal.AllocHGlobal((int)length);
    try {
      if (!GetSystemCpuSetInformation(buffer, length, out length, IntPtr.Zero, 0)) throw new Win32Exception(Marshal.GetLastWin32Error());
      StringBuilder text = new StringBuilder();
      for (int at = 0; at < length; ) {
        int size = Marshal.ReadInt32(buffer, at);
        if (size <= 0) break;
        if (Marshal.ReadInt32(buffer, at + 4) == 0) {
          text.AppendFormat("cpuset lp={0} group={1} core={2} llc={3} numa={4} class={5} flags={6}\n",
            Marshal.ReadByte(buffer, at + 14), (ushort)Marshal.ReadInt16(buffer, at + 12), Marshal.ReadByte(buffer, at + 15),
            Marshal.ReadByte(buffer, at + 16), Marshal.ReadByte(buffer, at + 17), Marshal.ReadByte(buffer, at + 18), Marshal.ReadByte(buffer, at + 19));
        }
        at += size;
      }
      return text.ToString();
    } finally { Marshal.FreeHGlobal(buffer); }
  }
}
'@
[PleiadesCpuSets]::Read()
`

// ExitConstrained is Script's exit code when PowerShell's language mode
// refuses Add-Type. Its output then holds "language=<mode>".
const ExitConstrained = 20

// flagParked is the CPU set flag Windows sets on a parked processor.
const flagParked = 0x1

// Processor is one logical processor.
type Processor struct {
	// Index is its number within its processor group, the bit an
	// affinity mask sets for it.
	Index int
	Group int
	// Core is the group-relative core it is on; two logical processors on
	// one core are its hyperthreads.
	Core           int
	NUMANode       int
	LastLevelCache int
	// EfficiencyClass is 0 on a processor whose cores are all alike; on a
	// hybrid one, a higher class is a faster, less power-efficient core.
	EfficiencyClass int
	// Parked says Windows has parked it to save power.
	Parked bool
}

// Topology is every logical processor a host has, in the order Windows
// lists them.
type Topology struct {
	Processors []Processor
}

// cpusetKeys are the fields a cpuset line carries, each with the largest
// value its type in the API holds.
var cpusetKeys = map[string]int{"lp": 255, "group": 65535, "core": 255, "llc": 255, "numa": 255, "class": 255, "flags": 255}

// Parse reads Script's output. A line that is not a cpuset line is
// ignored; a cpuset line missing a field, holding one out of its range,
// or naming a processor twice is refused, as is output naming none.
func Parse(stdout string) (Topology, error) {
	var t Topology
	seen := map[[2]int]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "cpuset ")
		if !ok {
			continue
		}
		values := map[string]int{}
		for _, field := range strings.Fields(rest) {
			key, text, ok := strings.Cut(field, "=")
			limit, known := cpusetKeys[key]
			if !ok || !known {
				return Topology{}, fmt.Errorf("wincpu: %q in %q is not a CPU set field", field, line)
			}
			n, err := strconv.Atoi(text)
			if err != nil || n < 0 || n > limit {
				return Topology{}, fmt.Errorf("wincpu: %s=%q is not a number from 0 to %d", key, text, limit)
			}
			values[key] = n
		}
		if len(values) != len(cpusetKeys) {
			return Topology{}, fmt.Errorf("wincpu: %q lacks one of the CPU set fields", strings.TrimSpace(line))
		}
		id := [2]int{values["group"], values["lp"]}
		if seen[id] {
			return Topology{}, fmt.Errorf("wincpu: processor %d of group %d is listed twice", id[1], id[0])
		}
		seen[id] = true
		t.Processors = append(t.Processors, Processor{
			Index: values["lp"], Group: values["group"], Core: values["core"], NUMANode: values["numa"],
			LastLevelCache: values["llc"], EfficiencyClass: values["class"], Parked: values["flags"]&flagParked != 0,
		})
	}
	if len(t.Processors) == 0 {
		return Topology{}, fmt.Errorf("wincpu: the host listed no processors")
	}
	return t, nil
}

// topClass is the highest efficiency class any processor has.
func (t Topology) topClass() int {
	top := 0
	for _, p := range t.Processors {
		top = max(top, p.EfficiencyClass)
	}
	return top
}

// Hybrid reports whether the cores are of more than one efficiency class.
func (t Topology) Hybrid() bool {
	for _, p := range t.Processors {
		if p.EfficiencyClass != t.Processors[0].EfficiencyClass {
			return true
		}
	}
	return false
}

// Performance returns the processors of the highest efficiency class:
// every processor, when the host is not hybrid.
func (t Topology) Performance() []Processor {
	return t.class(func(class, top int) bool { return class == top })
}

// Efficiency returns the processors below the highest efficiency class.
func (t Topology) Efficiency() []Processor {
	return t.class(func(class, top int) bool { return class < top })
}

// class returns the processors whose class keep accepts.
func (t Topology) class(keep func(class, top int) bool) []Processor {
	top := t.topClass()
	var out []Processor
	for _, p := range t.Processors {
		if keep(p.EfficiencyClass, top) {
			out = append(out, p)
		}
	}
	return out
}

// Cores returns how many cores the processors are on.
func (t Topology) Cores() int {
	cores := map[[2]int]bool{}
	for _, p := range t.Processors {
		cores[[2]int{p.Group, p.Core}] = true
	}
	return len(cores)
}

// Groups returns the processor groups, in order. A host with more than
// 64 logical processors has more than one.
func (t Topology) Groups() []int {
	seen := map[int]bool{}
	var groups []int
	for _, p := range t.Processors {
		if !seen[p.Group] {
			seen[p.Group] = true
			groups = append(groups, p.Group)
		}
	}
	sort.Ints(groups)
	return groups
}

// PerformanceMask returns the affinity mask selecting the performance
// processors, and false on a host with more than one processor group,
// where one mask cannot name them all.
func (t Topology) PerformanceMask() (uint64, bool) {
	if len(t.Groups()) != 1 {
		return 0, false
	}
	var mask uint64
	for _, p := range t.Performance() {
		if p.Index > 63 {
			return 0, false
		}
		mask |= 1 << p.Index
	}
	return mask, true
}

// Indexes returns the processors' indexes, in order.
func Indexes(processors []Processor) []int {
	out := make([]int, len(processors))
	for i, p := range processors {
		out[i] = p.Index
	}
	return out
}

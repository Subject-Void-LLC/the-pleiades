// The facts facts.gather reads beside its first eight, from the same
// sources (uname, /etc/os-release, /proc/meminfo, /proc/cpuinfo) and one
// more uname call, so a default run describes the device whole (Phase 131's
// R5) without a probe that needs a tool a BusyBox device lacks.
package facts

import (
	"strconv"
	"strings"
)

// gatherNodeNames reads uname -n into the short host name and the full node
// name, both left out when the device printed nothing.
func gatherNodeNames(stdout string) []gatherFact {
	node := strings.TrimSpace(stdout)
	if node == "" {
		return nil
	}
	return []gatherFact{{key: gatherFactHostName, value: gatherShortName(node)}, {key: gatherFactHostNodename, value: node}}
}

// gatherMemory reads total memory and total swap out of /proc/meminfo, each
// in megabytes and each left out when its line is missing or unreadable.
// Swap may be 0, which is an answer: a device with no swap says so. Free
// memory is not gathered: it differs on every read, so as drift data it
// would be nothing but noise.
func gatherMemory(stdout string) []gatherFact {
	facts := gatherMemTotalMB(stdout)
	if kilobytes, err := strconv.ParseInt(gatherFieldAfter(stdout, "SwapTotal:"), 10, 64); err == nil && kilobytes >= 0 {
		facts = append(facts, gatherFact{key: gatherFactSwapMB, value: kilobytes / gatherKilobytesPerMegabyte})
	}
	return facts
}

// gatherProcessors reads the package count, the logical processor count
// (the processor entries) and the cores per package (the first cpu cores
// field) out of /proc/cpuinfo, each left out when the file does not carry
// what it is counted from.
func gatherProcessors(stdout string) []gatherFact {
	facts := gatherProcessorCount(stdout)
	var vcpus int
	cores := -1
	for _, line := range strings.Split(stdout, "\n") {
		label, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(label) {
		case "processor":
			vcpus++
		case "cpu cores":
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && cores < 0 && n > 0 {
				cores = n
			}
		}
	}
	if vcpus > 0 {
		facts = append(facts, gatherFact{key: gatherFactCPUThreads, value: vcpus})
	}
	if cores > 0 {
		facts = append(facts, gatherFact{key: gatherFactCPUCores, value: cores})
	}
	return facts
}

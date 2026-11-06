// Package facts: the min and hardware probes of FreeBSD and macOS (Phase
// 41c). Both answer uname as Linux does; FreeBSD has /etc/os-release, and
// macOS names itself through sw_vers; memory, processors, FreeBSD's swap and
// the boot time come from sysctl rather than /proc, which neither has.
package facts

import (
	"strconv"
	"strings"
)

// bsdHostProbes are platform's (FreeBSD's or Darwin's) min and hardware
// probes.
func bsdHostProbes(platform string) []gatherProbe {
	probes := []gatherProbe{}
	for _, p := range gatherHostProbes() {
		// uname answers alike on all three; the distribution is FreeBSD's
		// own os-release, or sw_vers on a Mac below.
		if p.subset == gatherSubsetMin && (strings.HasPrefix(p.command, "uname") || platform == platformFreeBSD) {
			probes = append(probes, p)
		}
	}
	if platform == platformDarwin {
		probes = append(probes, gatherProbe{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactDistribution, gatherFactOSFamily, gatherFactDistributionVersion, gatherFactDistributionMajor},
			command: "sw_vers",
			parse:   parseSwVers,
		})
	}
	memory := "hw.physmem"
	if platform == platformDarwin {
		memory = "hw.memsize"
	} else {
		// FreeBSD's configured swap. A Mac reports none, as Ansible does
		// (its Darwin hardware facts have no swap): macOS makes and removes
		// swap files as memory is needed, so vm.swapusage's total moves on
		// a Mac nobody changed.
		probes = append(probes, gatherProbe{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactSwapTotalMB},
			command: "sysctl -n vm.swap_total",
			parse:   bytesToMB(gatherFactSwapTotalMB),
		})
	}
	return append(probes,
		gatherProbe{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactMemTotalMB},
			command: "sysctl -n " + memory,
			parse:   bytesToMB(gatherFactMemTotalMB),
		},
		gatherProbe{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactProcessorVCPUs},
			command: "sysctl -n hw.ncpu",
			parse:   wholeNumber(gatherFactProcessorVCPUs),
		},
		gatherProbe{
			subset: gatherSubsetHardware,
			keys:   []string{gatherFactUptimeSeconds},
			// The boot time and the device's own clock, one line each, so
			// the uptime does not lean on this machine's clock.
			command: "sysctl -n kern.boottime; date +%s",
			parse:   parseBoottime,
		},
	)
}

// parseSwVers reads sw_vers (ProductName, ProductVersion), naming the
// distribution MacOSX and its family Darwin as Ansible does, so a converted
// condition reads the same.
func parseSwVers(out string) []gatherFact {
	var facts []gatherFact
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "ProductName":
			if value != "" {
				facts = append(facts, gatherFact{key: gatherFactDistribution, value: "MacOSX"}, osFamilyFact("MacOSX"))
			}
		case "ProductVersion":
			if value != "" {
				major, _, _ := strings.Cut(value, ".")
				facts = append(facts, gatherFact{key: gatherFactDistributionVersion, value: value},
					gatherFact{key: gatherFactDistributionMajor, value: major})
			}
		}
	}
	return facts
}

// bytesToMB reads a byte count as key's megabytes, truncated as Ansible
// truncates.
func bytesToMB(key string) func(string) []gatherFact {
	return func(out string) []gatherFact {
		n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if err != nil || n < 0 {
			return nil
		}
		return []gatherFact{{key: key, value: int(n / (1 << 20))}}
	}
}

// wholeNumber reads a whole number as key's value.
func wholeNumber(key string) func(string) []gatherFact {
	return func(out string) []gatherFact {
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || n < 0 {
			return nil
		}
		return []gatherFact{{key: key, value: n}}
	}
}

// parseBoottime reads kern.boottime ("{ sec = 1793718135, usec = 706058 }
// Tue Nov ...") and the device's date +%s on the next line, as the seconds
// since boot.
func parseBoottime(out string) []gatherFact {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return nil
	}
	_, rest, ok := strings.Cut(lines[0], "sec = ")
	if !ok {
		return nil
	}
	secs, _, _ := strings.Cut(rest, ",")
	boot, err1 := strconv.ParseInt(strings.TrimSpace(secs), 10, 64)
	now, err2 := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64)
	if err1 != nil || err2 != nil || now < boot {
		return nil
	}
	return []gatherFact{{key: gatherFactUptimeSeconds, value: int(now - boot)}}
}

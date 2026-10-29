//go:build darwin

// macOS's memory: hw.memsize for the total, vm_stat for the pages a new
// allocation can have, vm.swapusage for the swap in use.
package headroom

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// read is Read on macOS.
func read() (Memory, error) {
	memsize, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	total, err := strconv.ParseFloat(strings.TrimSpace(string(memsize)), 64)
	if err != nil {
		return Memory{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	stat, err := exec.Command("vm_stat").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("vm_stat: %w", err)
	}
	available, err := parseVMStat(string(stat))
	if err != nil {
		return Memory{}, err
	}
	swap, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("sysctl vm.swapusage: %w", err)
	}
	used, err := parseSwapUsage(string(swap))
	if err != nil {
		return Memory{}, err
	}
	return Memory{TotalMB: total / (1024 * 1024), AvailableMB: available, SwapUsedMB: used}, nil
}

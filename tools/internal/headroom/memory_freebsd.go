//go:build freebsd

// FreeBSD's memory: sysctl for the physical memory and the free and
// inactive pages, swapinfo for the swap in use.
package headroom

import (
	"fmt"
	"os/exec"
)

// read is Read on FreeBSD.
func read() (Memory, error) {
	out, err := exec.Command("sysctl", "-n", "hw.physmem", "hw.pagesize", "vm.stats.vm.v_free_count", "vm.stats.vm.v_inactive_count").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("sysctl: %w", err)
	}
	total, available, err := parseFreeBSD(string(out))
	if err != nil {
		return Memory{}, err
	}
	swap, err := exec.Command("swapinfo", "-k").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("swapinfo: %w", err)
	}
	used, err := parseSwapinfo(string(swap))
	if err != nil {
		return Memory{}, err
	}
	return Memory{TotalMB: total, AvailableMB: available, SwapUsedMB: used}, nil
}

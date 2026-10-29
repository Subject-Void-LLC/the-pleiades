// What the machine had while a run ran, and how the run was held to it:
// the part of a run's record that tools/internal/headroom's policy writes,
// so a run stopped for memory says what it saw, a run killed by the kernel
// leaves the numbers that explain it, and each package's cost is measured
// on every machine rather than guessed on one.
package flakegate

// Headroom is what the machine had while a run ran, and how the run was
// bounded by it (tools/internal/headroom); absent on a record written
// before testgate measured it.
type Headroom struct {
	// AvailableMB is the memory free as the run began, in MB: MemAvailable
	// or its equivalent, or inside a cgroup with a memory limit the smaller
	// of that and what the limit had left.
	AvailableMB float64 `json:"available_mb"`
	// LimitMB is the cgroup memory limit the run ran under, in MB, or 0 with
	// none.
	LimitMB float64 `json:"limit_mb,omitempty"`
	// MinAvailableMB is the least memory free seen while the run ran.
	MinAvailableMB float64 `json:"min_available_mb"`
	// SwapGrowthMB is the most the swap in use grew past where it began.
	SwapGrowthMB float64 `json:"swap_growth_mb"`
	// ParallelAsked is the -p the run was asked for; 0 is go test's own
	// default, the CPU count.
	ParallelAsked int `json:"parallel_asked"`
	// ParallelRan is the -p the run used, lowered from ParallelAsked to what
	// the free memory fit.
	ParallelRan int `json:"parallel_ran"`
	// DiskFreeGB is the space free, in GB, on each file system the run builds
	// and writes on, by path, as the run began.
	DiskFreeGB map[string]float64 `json:"disk_free_gb,omitempty"`
	// PackageMB is the most each package's tests cost the machine while they
	// ran, in MB: the fall in free memory below where the run began, swap
	// growth included. With several packages running at once each is charged
	// the whole fall, so it is a bound on what a package took, not its share;
	// with one at a time it is its own.
	PackageMB map[string]float64 `json:"package_mb,omitempty"`
	// PackageDiskMB is the most each package's run wrote to disk, in MB: the
	// largest fall in free space below where the run began on any one of the
	// file systems it builds and writes on (its build cache, test binaries
	// and temporary files), charged as PackageMB is.
	PackageDiskMB map[string]float64 `json:"package_disk_mb,omitempty"`
	// MinDiskFreeGB is the least space free seen on each file system the run
	// builds and writes on, by path, in GB.
	MinDiskFreeGB map[string]float64 `json:"min_disk_free_gb,omitempty"`
	// Stopped is why the run was stopped before it ended: the memory floor,
	// swap growth, or a signal; empty for a run that ended on its own.
	Stopped string `json:"stopped,omitempty"`
	// Samples is how many times the machine was read while the run ran.
	Samples int `json:"samples"`
}

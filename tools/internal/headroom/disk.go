// The disk check a run makes before it builds anything: each place it
// writes must have MinFreeGB free, or the run is refused with the space
// named and what frees it, rather than failing every package to build with
// no compiler output once the disk is full.
package headroom

import (
	"fmt"
	"sort"
	"strings"
)

// CheckDisk reads the space free on the file system holding each of paths
// (a run's module directory, where go test builds and writes, and its
// temporary directory, where test binaries and go-build work folders go)
// and refuses the run when any has less than MinFreeGB, naming each short
// one and what frees space there. The reading is returned either way, by
// path, for the run's record. A path that cannot be read is refused too: a
// run that cannot see its disk cannot promise it will not fill it.
func CheckDisk(paths ...string) (map[string]float64, error) {
	return checkDisk(DiskFreeGB, MinFreeGB, paths...)
}

// checkDisk is CheckDisk with the reader and the floor given.
func checkDisk(free func(string) (float64, error), minGB float64, paths ...string) (map[string]float64, error) {
	out := map[string]float64{}
	var short []string
	for _, p := range paths {
		if _, seen := out[p]; seen {
			continue
		}
		gb, err := free(p)
		if err != nil {
			return out, fmt.Errorf("reading the space free under %s: %w", p, err)
		}
		out[p] = gb
		if gb < minGB {
			short = append(short, fmt.Sprintf("%.1f GB free under %s", gb, p))
		}
	}
	if len(short) == 0 {
		return out, nil
	}
	sort.Strings(short)
	return out, fmt.Errorf("%s, under the %.0f GB a run needs; free space there first (go clean -cache removes Go's build cache, docker system df says what the container engine holds and docker system prune what of it nothing uses, and a killed run's go-build* folders sit in the temporary directory)",
		strings.Join(short, " and "), minGB)
}

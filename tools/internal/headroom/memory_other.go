//go:build !linux && !darwin && !freebsd && !windows

// A system this repository's tests do not run on: no reading, said so, and
// the run goes on unbounded by memory.
package headroom

import (
	"fmt"
	"runtime"
)

// read is Read where no reader exists.
func read() (Memory, error) {
	return Memory{}, fmt.Errorf("no memory reader for %s", runtime.GOOS)
}

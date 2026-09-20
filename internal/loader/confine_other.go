//go:build unix && !linux

// Package loader: confinement where there is no Landlock.
package loader

import (
	"fmt"
	"os/exec"
	"runtime"
)

// errNoConfinement is why programs cannot be confined on this platform.
var errNoConfinement = fmt.Errorf("%s has no Landlock, which confines each program to what it was handed; "+
	"external Collections are refused rather than run unconfined (they run on Linux, including WSL 2)", runtime.GOOS)

// confinementAvailable always refuses here.
func confinementAvailable() (int, error) {
	return 0, errNoConfinement
}

// protectProcess is never reached here, since Load refuses first.
func protectProcess() error {
	return errNoConfinement
}

// startConfined is never reached here, since Load refuses first. It
// refuses rather than starting anything unconfined.
func startConfined(_ *exec.Cmd, _ []confineRule, _ int) error {
	return errNoConfinement
}

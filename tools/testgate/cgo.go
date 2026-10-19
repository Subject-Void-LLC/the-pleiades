// Whether this run's tests are built with cgo off.
package main

import (
	"os/exec"
	"strings"
)

// goEnvCgo answers go env CGO_ENABLED, as the go test this run starts
// builds with: "0" when it is off, as Go turns it off on a machine with no
// C compiler. A test replaces it.
var goEnvCgo = func() (string, error) {
	out, err := exec.Command("go", "env", "CGO_ENABLED").Output()
	return strings.TrimSpace(string(out)), err
}

// cgoOff reports whether this run's tests are built with cgo off. A go env
// that cannot answer counts as on, which leaves every failure a failure.
func cgoOff() bool {
	v, err := goEnvCgo()
	return err == nil && v == "0"
}

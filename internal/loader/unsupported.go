// Package loader: the refusal on a platform the loader does not run on.
package loader

import "fmt"

// unsupportedMessage is why external Collections are refused on goos, a
// platform with no loader (load_other.go). It names the reason and, on
// Windows, the way to get the feature there today. It is built on every
// platform so its test runs on every platform, since the platforms that
// use it are ones this project's tests rarely run on.
func unsupportedMessage(goos string) string {
	msg := fmt.Sprintf("external collections are not supported on %s: loading them safely needs a confined child process "+
		"(Landlock on Linux) and Unix file ownership and permission checks on their directory, and neither exists here yet", goos)
	if goos == "windows" {
		msg += "; run Pleiades under WSL 2, which is Linux and supports them"
	}
	return msg
}

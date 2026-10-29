// What a data directory setting may be, checked once here for every check
// that writes one into the scripts its machine runs: a POSIX one goes into
// df, mkdir, export, rm -rf and cd lines under double quotes, a Windows one
// into every script's env, so each is held to plain characters before any
// script holds it.
package oscheck

import (
	"fmt"
	"regexp"
)

// posixDataDir is an absolute directory of plain characters, such as /data.
var posixDataDir = regexp.MustCompile(`^/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// windowsDataDir is an absolute drive path of plain characters, such as
// D:\pleiades, with no trailing backslash.
var windowsDataDir = regexp.MustCompile(`^[A-Za-z]:\\[A-Za-z0-9._-]+(\\[A-Za-z0-9._-]+)*$`)

// CheckPOSIXDataDir refuses a POSIX data directory setting (key names it)
// that is not an absolute directory of plain characters; "" is no setting.
func CheckPOSIXDataDir(key, data string) error {
	if data != "" && !posixDataDir.MatchString(data) {
		return fmt.Errorf("%s=%q is not an absolute directory of plain characters, such as /data", key, data)
	}
	return nil
}

// CheckWindowsDataDir refuses a Windows data directory setting (key names
// it) that is not an absolute drive path of plain characters; "" is no
// setting.
func CheckWindowsDataDir(key, data string) error {
	if data != "" && !windowsDataDir.MatchString(data) {
		return fmt.Errorf("%s=%q is not an absolute drive path of plain characters, such as D:\\pleiades", key, data)
	}
	return nil
}

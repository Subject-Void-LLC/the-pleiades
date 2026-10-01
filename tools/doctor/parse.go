// Reading the versions the repository pins, from the files that pin them.
package main

import (
	"go/version"
	"regexp"
	"strings"
)

// requiredToolchain is the toolchain go.mod asks for: its toolchain line
// when it has one, else its go line.
func requiredToolchain(gomod string) string {
	var goLine string
	for _, raw := range strings.Split(gomod, "\n") {
		fields := strings.Fields(raw)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "toolchain":
			return fields[1]
		case "go":
			goLine = "go" + fields[1]
		}
	}
	return goLine
}

// atLeast reports whether toolchain have is at least need. A development
// build of Go reports no comparable version, and is let through rather
// than failed, since doctor cannot tell what it is.
func atLeast(have, need string) bool {
	if !version.IsValid(have) || !version.IsValid(need) {
		return true
	}
	return version.Compare(have, need) >= 0
}

// makefilePin matches a pinned tool version in the Makefile, such as
// "GOSEC_VERSION       ?= v2.28.0".
var makefilePin = regexp.MustCompile(`(?m)^([A-Z]+)_VERSION\s*\?=\s*(\S+)`)

// pinnedTools maps each tool the Makefile pins to its version, keyed by
// the command's name.
func pinnedTools(makefile string) map[string]string {
	pins := map[string]string{}
	for _, m := range makefilePin.FindAllStringSubmatch(makefile, -1) {
		pins[strings.ToLower(m[1])] = m[2]
	}
	return pins
}

// moduleVersion reads the module version out of `go version -m`, the
// line that starts "mod", which is what `make tools` compares too.
func moduleVersion(modinfo string) string {
	for _, raw := range strings.Split(modinfo, "\n") {
		fields := strings.Fields(raw)
		if len(fields) >= 3 && fields[0] == "mod" {
			return fields[2]
		}
	}
	return ""
}

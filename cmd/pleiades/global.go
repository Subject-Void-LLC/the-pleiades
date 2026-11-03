// Package main: the options that come before a command and hold for all of
// them (clispec.Root.Flags): today only --known-hosts.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// knownHostsFlag is the global option naming a known_hosts file.
const knownHostsFlag = "--known-hosts"

// applyGlobalOptions consumes the global options at the front of args and
// returns what is left, the command and its own arguments. --known-hosts
// FILE (or --known-hosts=FILE) sets remoteexec.KnownHostsEnv for this
// process and every process it starts, made absolute so a child working in
// another directory reads the same file; it wins over a value already in
// the environment, since it was written on this command line.
func applyGlobalOptions(args []string) ([]string, error) {
	for len(args) > 0 {
		arg := args[0]
		var value string
		switch {
		case arg == knownHostsFlag:
			if len(args) < 2 || strings.HasPrefix(args[1], "-") {
				return nil, fmt.Errorf("%s needs a file, as in pleiades %s FILE <command>", knownHostsFlag, knownHostsFlag)
			}
			value, args = args[1], args[2:]
		case strings.HasPrefix(arg, knownHostsFlag+"="):
			value, args = strings.TrimPrefix(arg, knownHostsFlag+"="), args[1:]
		default:
			return args, nil
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s needs a file, not an empty value", knownHostsFlag)
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", knownHostsFlag, value, err)
		}
		if err := os.Setenv(remoteexec.KnownHostsEnv, abs); err != nil {
			return nil, fmt.Errorf("%s: %w", knownHostsFlag, err)
		}
	}
	return args, nil
}

package main

import "testing"

// FuzzRunForgeNewCollection calls runForgeNewCollection directly with
// fuzzed arguments, rather than going through run()/runForge, for the same
// reason FuzzRunForgeNewDevice does: cli_fuzz_test.go's generic
// FuzzCommandDispatch harness splices "--dir <tmp>" in right after the
// top-level command name, so a "forge"-prefixed seed never reaches this
// subcommand's real flag parsing. A bad invocation must return an error,
// never panic.
func FuzzRunForgeNewCollection(f *testing.F) {
	f.Add("pkg.apt.install", "AptCapable", "ssh")
	f.Add("install", "", "")
	f.Add("", "", "")
	f.Add("pkg..install", "", "")
	f.Add("../../etc/passwd", "", "")
	f.Add("pkg.apt.install", "NotARealCapability", "")
	f.Add("pkg.type.install", "", "")
	f.Add("pkg.apt.install", "AptCapable,SSHTransportCapable", "ssh,winrm")

	f.Fuzz(func(t *testing.T, name, capabilities, transports string) {
		dir := t.TempDir()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runForgeNewCollection(%q, %q, %q) panicked: %v", name, capabilities, transports, r)
			}
		}()
		_ = runForgeNewCollection([]string{name, "--capabilities", capabilities, "--transports", transports, "--dir", dir})
	})
}

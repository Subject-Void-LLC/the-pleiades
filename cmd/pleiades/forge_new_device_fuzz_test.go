package main

import "testing"

// FuzzRunForgeNewDevice calls runForgeNewDevice directly with fuzzed
// arguments, rather than going through run()/runForge. cli_fuzz_test.go's
// own FuzzCommandDispatch already carries a "forge new-device" seed, but
// its generic harness splices "--dir <tmp>" in right after the top-level
// command name for every seed, so for any "forge"-prefixed input
// runForge's own args[0] becomes the literal string "--dir" -- an
// unknown-subcommand case that never reaches this subcommand's real flag
// parsing. This target exists specifically to close that gap: a bad
// invocation must return an error, never panic.
func FuzzRunForgeNewDevice(f *testing.F) {
	f.Add("juniper", "junos_router", "SSHTransportCapable")
	f.Add("", "junos_router", "")
	f.Add("juniper", "", "")
	f.Add("..", "junos_router", "")
	f.Add("juniper", "../../etc/passwd", "")
	f.Add("juniper", "junos_router", "NotARealCapability")
	f.Add("func", "type", "package")
	f.Add("juniper", "junos_router", "SSHTransportCapable,JunosCapable")

	f.Fuzz(func(t *testing.T, vendor, typeKey, capabilities string) {
		dir := t.TempDir()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runForgeNewDevice(%q, %q, %q) panicked: %v", vendor, typeKey, capabilities, r)
			}
		}()
		_ = runForgeNewDevice([]string{vendor, "--type", typeKey, "--capabilities", capabilities, "--dir", dir})
	})
}

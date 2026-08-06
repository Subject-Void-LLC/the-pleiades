package main

import "testing"

// FuzzRunForgeNewPlugin calls runForgeNewPlugin directly with fuzzed
// arguments, rather than going through run()/runForge, for the same reason
// FuzzRunForgeNewDevice and FuzzRunForgeNewCollection do: cli_fuzz_test.go's
// generic FuzzCommandDispatch harness splices "--dir <tmp>" in right after
// the top-level command name, so a "forge"-prefixed seed never reaches this
// subcommand's real flag parsing.
//
// The property under test is that a bad invocation returns an error and
// never panics. The seeds concentrate on the plugin name, since that single
// string becomes a Go package name, an exported type name, and a filesystem
// directory component all at once, which is exactly the combination
// genutil.ValidateSegment exists to keep safe.
func FuzzRunForgeNewPlugin(f *testing.F) {
	f.Add("catalyst_center", "reads a Catalyst Center", "https://sandboxdnac.cisco.com")
	f.Add("netbox", "reads NetBox", "")
	f.Add("", "", "")
	f.Add("../../etc/passwd", "escape attempt", "")
	f.Add("..", "parent directory", "")
	f.Add("net.catalyst", "a dotted name is not a plugin namespace", "")
	f.Add("package", "a Go keyword", "")
	f.Add("9lives", "a leading digit", "")
	f.Add("NetBox", "uppercase", "")
	f.Add("catalyst_center", "", "missing description")
	f.Add("catalyst_center", "bad endpoint", "not-a-url")
	f.Add("catalyst_center", "scheme-less endpoint", "sandboxdnac.cisco.com")
	f.Add("catalyst\x00center", "a null byte", "")

	f.Fuzz(func(t *testing.T, name, description, endpoint string) {
		dir := t.TempDir()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runForgeNewPlugin(%q, %q, %q) panicked: %v", name, description, endpoint, r)
			}
		}()
		_ = runForgeNewPlugin([]string{name, "--description", description, "--endpoint", endpoint, "--dir", dir})
	})
}

package main

import "testing"

// FuzzCommandDispatch ensures malformed or adversarial argument vectors
// can never panic subcommand dispatch or flag parsing for any of the four
// subcommands. A bad invocation must return a non-zero exit code, not
// crash the process a user just typed a typo into.
func FuzzCommandDispatch(f *testing.F) {
	f.Add("init", "", "", "")
	f.Add("add-host", "", "--type", "")
	f.Add("add-host", "host1", "--type", "linux_server")
	f.Add("add-host", "host1", "--set", "not-a-key-value-pair")
	f.Add("validate", "", "", "")
	f.Add("run", "", "", "")
	f.Add("unknown-command", "", "", "")
	f.Add("", "", "", "")
	f.Add("-h", "", "", "")

	f.Fuzz(func(t *testing.T, cmd, a1, a2, a3 string) {
		dir := t.TempDir()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("run(%q, %q, %q, %q) panicked: %v", cmd, a1, a2, a3, r)
			}
		}()
		// Every subcommand takes --dir; pointing it at a fresh temp
		// directory keeps a malformed fuzz input from ever touching a
		// real file outside the test's own sandbox.
		_ = run([]string{cmd, "--dir", dir, a1, a2, a3})
	})
}

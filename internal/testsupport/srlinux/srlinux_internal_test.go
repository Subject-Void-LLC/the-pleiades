// Package srlinux: reading SR Linux's count of refused logins from the
// statistics sr_cli writes, which the gNMI gates hold a wrong password to.
package srlinux

import "testing"

// TestLoginRejects_ReadsTheServerWideCount reads the server-wide count, not
// the per-RPC one sr_cli writes after it, and zero when SR Linux has written
// no statistics yet.
func TestLoginRejects_ReadsTheServerWideCount(t *testing.T) {
	for in, want := range map[string]int{
		"": 0,
		// As SR Linux 26.7.2 wrote it after one refusal, its counts changed.
		"    access-rejects 3\n    last-access-reject \"2026-11-16T00:36:10.251Z (now)\"\n    access-accepts 20\n    rpc /gnmi.gNMI/Capabilities {\n        access-rejects 1\n    }\n": 3,
		"    statistics {\n        access-accepts 9\n    }\n": 0,
	} {
		got, err := loginRejects(in)
		if err != nil || got != want {
			t.Errorf("loginRejects(%q) = %d, %v, want %d", in, got, err, want)
		}
	}
}

// Package topology_test: a dispatch's identity and its key.
package topology_test

import (
	"regexp"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// TestDispatchIdentity_RoundTrips: the identity reads back to the job and
// device it was made from, a device id holding colons included, and text
// that is not an identity is refused.
func TestDispatchIdentity_RoundTrips(t *testing.T) {
	const job = "01a15e80-5c6c-7634-99f9-52bca31d06fb"
	for _, device := range []string{"rtr1", "router1.example.com", "fe80::1", "a:b:c"} {
		id := topology.DispatchIdentity(job, device)
		gotJob, gotDevice, ok := topology.ParseDispatchIdentity(id)
		if !ok || gotJob != job || gotDevice != device {
			t.Errorf("ParseDispatchIdentity(%q) = %q, %q, %v; want %q, %q", id, gotJob, gotDevice, ok, job, device)
		}
	}
	for _, bad := range []string{"", "no-colon", ":device", "job:"} {
		if _, _, ok := topology.ParseDispatchIdentity(bad); ok {
			t.Errorf("ParseDispatchIdentity(%q) was accepted", bad)
		}
	}
}

// TestDispatchKey_IsOneTokenAndDistinguishesWork: the key is one subject
// token, safe as a KV key and as a recorded lease owner, and two different
// units of work never share one. A job id never holds a colon (the Runner
// refuses one that is not a UUID), so "job-1:x" with "y" is not a pair
// that can occur, and it would share "job-1" with "x:y"'s identity.
func TestDispatchKey_IsOneTokenAndDistinguishesWork(t *testing.T) {
	token := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	seen := map[string]string{}
	for _, pair := range [][2]string{
		{"job-1", "router1.example.com"}, {"job-1", "router1_example_com"}, {"job-1", "fe80::1"},
		{"job-2", "router1.example.com"}, {"job-1", "x:y"},
	} {
		key := topology.DispatchKey(pair[0], pair[1])
		if !token.MatchString(key) {
			t.Errorf("DispatchKey%v = %q, not one subject token", pair, key)
		}
		if other, dup := seen[key]; dup {
			t.Errorf("DispatchKey%v = %q, the same as %s", pair, key, other)
		}
		seen[key] = pair[0] + " " + pair[1]
		if again := topology.DispatchKey(pair[0], pair[1]); again != key {
			t.Errorf("DispatchKey%v is not deterministic: %q then %q", pair, key, again)
		}
	}
}

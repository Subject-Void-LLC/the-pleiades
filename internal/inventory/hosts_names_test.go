// Package inventory_test: no writer of hosts.yaml can add a host named "",
// "." or "..", while a host the file already held keeps being written.
package inventory_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// TestEncodeHosts_RefusesOnlyANewUnaddressableHost holds the backstop to the
// file it writes into: a new host with such a name is refused, one the
// original already held is written again unchanged, and a second one under
// the same name is new.
func TestEncodeHosts_RefusesOnlyANewUnaddressableHost(t *testing.T) {
	legacy := []byte("hosts:\n  - id: a\n    name: .\n    type: linux_server\n")
	ok := inventory.HostSpec{ID: "b", Name: "web-01", Type: "linux_server"}
	dot := inventory.HostSpec{ID: "a", Name: ".", Type: "linux_server"}

	for _, tc := range []struct {
		name     string
		original []byte
		hosts    []inventory.HostSpec
		refused  bool
	}{
		{"a usable name", nil, []inventory.HostSpec{ok}, false},
		{"an empty name into a new file", nil, []inventory.HostSpec{ok, {ID: "c", Name: "", Type: "linux_server"}}, true},
		{"a dot into a new file", nil, []inventory.HostSpec{dot}, true},
		{"two dots beside a held host", legacy, []inventory.HostSpec{dot, {ID: "d", Name: "..", Type: "linux_server"}}, true},
		{"a held dot written again, beside a new host", legacy, []inventory.HostSpec{dot, ok}, false},
		{"a second dot beside the held one", legacy, []inventory.HostSpec{dot, {ID: "e", Name: ".", Type: "linux_server"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inventory.EncodeHosts(tc.original, tc.hosts)
			switch {
			case tc.refused && !errors.Is(err, recordname.ErrUnaddressable):
				t.Fatalf("EncodeHosts = %v, want a refusal matching recordname.ErrUnaddressable", err)
			case !tc.refused && err != nil:
				t.Fatalf("EncodeHosts = %v, want nil", err)
			}
		})
	}

	if _, err := inventory.EncodeHosts([]byte("hosts: [unclosed"), []inventory.HostSpec{dot}); err == nil || errors.Is(err, recordname.ErrUnaddressable) {
		t.Errorf("EncodeHosts over a file that does not parse = %v, want the parse error", err)
	}
}

// TestWriteHosts_LeavesTheFileAsItWasOnARefusal proves a refusal writes
// nothing: the file a command reads next is the file it had.
func TestWriteHosts_LeavesTheFileAsItWasOnARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), inventory.DefaultInventoryFilename)
	if err := inventory.WriteHosts(path, []inventory.HostSpec{{ID: "b", Name: "web-01", Type: "linux_server"}}); err != nil {
		t.Fatalf("WriteHosts: %v", err)
	}
	err := inventory.WriteHosts(path, []inventory.HostSpec{
		{ID: "b", Name: "web-01", Type: "linux_server"},
		{ID: "c", Name: "..", Type: "linux_server"},
	})
	if !errors.Is(err, recordname.ErrUnaddressable) {
		t.Fatalf("WriteHosts with a new host named \"..\" = %v, want a refusal", err)
	}
	hosts, err := inventory.ReadHosts(path)
	if err != nil {
		t.Fatalf("ReadHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web-01" {
		t.Errorf("hosts after the refusal = %v, want only web-01", hosts)
	}
}

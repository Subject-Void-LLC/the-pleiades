// Tests of RemoveHost against real hosts.yaml and sidecar files.
package inventory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveHost(t *testing.T) {
	dir := t.TempDir()
	hosts := filepath.Join(dir, DefaultInventoryFilename)
	specs := []HostSpec{
		{ID: "id-1", Name: "r1", Type: "linux_server", Properties: map[string]any{"host": "192.0.2.1"}},
		{ID: "id-2", Name: "r2", Type: "linux_server", Properties: map[string]any{"host": "192.0.2.2", "console_server": "r1"}},
		{ID: "id-3", Name: "r3", Type: "linux_server", Properties: map[string]any{"host": "192.0.2.3"}},
	}
	if err := WriteHosts(hosts, specs); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, sidecarFileName)
	if err := writeSidecar(sidecar, sidecarDocument{Hosts: []sidecarEntry{{ID: "id-1", Version: 3, State: "active"}, {ID: "id-3", Version: 1, State: "active"}}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveHost(hosts, "r9", false); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("an unknown host: %v", err)
	}
	// r2 names r1, so r1 stays until that is accepted.
	if _, err := RemoveHost(hosts, "r1", false); err == nil || !strings.Contains(err.Error(), "r2 (console_server)") {
		t.Fatalf("a host another names: %v", err)
	}
	removed, err := RemoveHost(hosts, "r1", true)
	if err != nil || removed.ID != "id-1" {
		t.Fatalf("RemoveHost(r1, leave) = %+v, %v", removed, err)
	}
	// A host with no sidecar entry removes cleanly too.
	if _, err := RemoveHost(hosts, "r2", false); err != nil {
		t.Fatalf("RemoveHost(r2): %v", err)
	}
	left, err := ReadHosts(hosts)
	if err != nil || len(left) != 1 || left[0].Name != "r3" {
		t.Fatalf("hosts left: %+v, %v", left, err)
	}
	doc, err := readSidecar(sidecar)
	if err != nil || len(doc.Hosts) != 1 || doc.Hosts[0].ID != "id-3" {
		t.Fatalf("sidecar left: %+v, %v", doc, err)
	}

	// A sidecar that cannot be read refuses the removal before it starts: the
	// error names the state file, and the host is still in hosts.yaml, so the
	// caller has nothing half done to clean up.
	if err := os.WriteFile(sidecar, []byte("hosts: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err = RemoveHost(hosts, "r3", false)
	if err == nil || !strings.Contains(err.Error(), "was not removed") || !strings.Contains(err.Error(), sidecarFileName) {
		t.Fatalf("an unreadable sidecar: %v", err)
	}
	if removed.Name != "" {
		t.Errorf("a refused removal returned the row %+v as removed", removed)
	}
	if still, err := ReadHosts(hosts); err != nil || len(still) != 1 || still[0].Name != "r3" {
		t.Errorf("hosts after a refused removal: %+v, %v; want r3 still there", still, err)
	}
}

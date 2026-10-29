// Tests for the disk check: this machine's own reading, and the refusal
// against a stand-in reader.
package headroom

import (
	"errors"
	"strings"
	"testing"
)

// TestDiskFreeGB reads a real file system: the temporary directory has some
// space, and a path that does not exist is an error.
func TestDiskFreeGB(t *testing.T) {
	gb, err := DiskFreeGB(t.TempDir())
	if err != nil || gb <= 0 {
		t.Errorf("DiskFreeGB(TempDir) = %.1f, %v", gb, err)
	}
	if _, err := DiskFreeGB(t.TempDir() + "/no/such/path"); err == nil {
		t.Error("a missing path read as having space")
	}
}

// TestCheckDisk: every path is read once and reported; one under the floor
// refuses the run, naming each short path, the floor and what frees space;
// one that cannot be read refuses it too.
func TestCheckDisk(t *testing.T) {
	space := map[string]float64{"/src": 749.5, "/tmp": 2.5, "/other": 1}
	reads := 0
	read := func(p string) (float64, error) {
		reads++
		gb, ok := space[p]
		if !ok {
			return 0, errors.New("no such file system")
		}
		return gb, nil
	}
	free, err := checkDisk(read, 4, "/src", "/src")
	if err != nil || free["/src"] != 749.5 || reads != 1 {
		t.Errorf("a path with room: %v, %v after %d read(s)", free, err, reads)
	}
	free, err = checkDisk(read, 4, "/src", "/tmp", "/other")
	if err == nil {
		t.Fatal("paths under the floor were accepted")
	}
	for _, w := range []string{"1.0 GB free under /other and 2.5 GB free under /tmp", "under the 4 GB a run needs", "go clean -cache", "docker system df", "go-build*"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not say %q: %v", w, err)
		}
	}
	if free["/tmp"] != 2.5 || free["/other"] != 1 {
		t.Errorf("the reading was not returned with the refusal: %v", free)
	}
	if _, err := checkDisk(read, 4, "/nowhere"); err == nil || !strings.Contains(err.Error(), "reading the space free under /nowhere") {
		t.Errorf("an unreadable path: %v", err)
	}
}

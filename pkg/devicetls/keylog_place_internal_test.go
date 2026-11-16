// Tests for the held key log directory's own check, from inside the package:
// a path that no longer names the directory held is refused rather than
// checked in its place.
package devicetls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeptOut_RefusesAPathThatIsNotTheDirectoryHeld holds one directory open
// and names another, as a path changed between the directory's opening and
// its check would: the check refuses rather than walk up a directory the file
// would not be made in.
func TestKeptOut_RefusesAPathThatIsNotTheDirectoryHeld(t *testing.T) {
	held, named := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(held)
	if err != nil {
		t.Fatal(err)
	}
	d := &keyLogDir{abs: filepath.Join(named, "run.keylog"), base: "run.keylog", root: root}
	defer d.close()
	if err := d.keptOut(named, nil); err == nil || !strings.Contains(err.Error(), "changed while the TLS key log was being checked") {
		t.Errorf("keptOut naming a directory other than the one held: %v", err)
	}
	if err := d.keptOut(held, nil); err != nil {
		t.Errorf("keptOut naming the directory held: %v", err)
	}
}

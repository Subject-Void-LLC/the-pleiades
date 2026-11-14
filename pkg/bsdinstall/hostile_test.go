// The Schema/Injection Hardening table for installerconfig: the one value a
// caller sets, a distribution set's name, reaches the script's preamble, a
// shell assignment, so every hostile value is refused before a script is
// written; the rest of the script is fixed text.
package bsdinstall

import (
	"strings"
	"testing"
	"time"
)

func TestScript_HostileDistributionsAreRefused(t *testing.T) {
	for _, v := range []string{"'", `"`, "`id`", "$(id)", ";id", "base.txz\nreboot", "base.txz\x00", "base\uFFFE.txz", "..", "../base.txz",
		`\\server\share\base.txz`, `C:\base.txz`, "-base.txz", "base.txz base.txz", "$HOME.txz", "BASE.TXZ", ""} {
		i := Install{Distributions: []string{"kernel.txz", v}}
		if script, err := i.Script(); err == nil || script != nil {
			t.Errorf("distribution %q: script written (%v)", v, err)
		}
		if iso, err := i.ISO(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)); err == nil || iso != nil {
			t.Errorf("distribution %q: image written (%v)", v, err)
		}
	}
	// A name the rule takes is carried as itself, inside the one quoted
	// assignment, and nowhere else.
	script, err := Install{Distributions: []string{"kernel.txz", "lib32.txz"}}.Script()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "DISTRIBUTIONS=\"kernel.txz lib32.txz\"\n") || strings.Count(string(script), "lib32.txz") != 1 {
		t.Errorf("the preamble:\n%s", script)
	}
}

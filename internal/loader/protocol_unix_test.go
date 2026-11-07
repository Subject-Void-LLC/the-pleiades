//go:build unix

// Package loader: a program agreed at a deprecated protocol warns once.
package loader

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// TestValidateCandidates_ADeprecatedProtocolWarnsOncePerProgram proves a
// program agreed at a deprecated version loads with exactly one warning that
// names the release removing it, and one at a supported version with none.
func TestValidateCandidates_ADeprecatedProtocolWarnsOncePerProgram(t *testing.T) {
	old := candidate{path: "/c/old", desc: external.Description{Protocol: 1},
		protocol: external.Protocol{Version: 1, Status: external.ProtocolDeprecated, RemovedIn: "0.6.0"}}
	current := candidate{path: "/c/new", desc: external.Description{Protocol: 1},
		protocol: external.Protocol{Version: 1, Status: external.ProtocolSupported}}
	warnings, problems := validateCandidates([]candidate{old, current}, "0.4.0", reservedNamespaces())
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "program /c/old speaks protocol 1, which is deprecated and removed in 0.6.0") {
		t.Errorf("warnings %q, want one naming /c/old and 0.6.0", warnings)
	}
}

// TestProgramDigest_HashesWithoutRunning proves ProgramDigest returns the
// digest Inspect reports for the same file, refuses a name that is not a
// program's file name, and refuses a directory Load would refuse.
func TestProgramDigest_HashesWithoutRunning(t *testing.T) {
	requireConfinement(t)
	dir := programDir(t)
	writeProgram(t, dir, "prog", script(describeJSON(t, 0, external.DescribedMethod{Name: "acme.digest.run", Manifest: implemented(false)}), "exit 0"))
	got, err := ProgramDigest(dir, "prog")
	if err != nil {
		t.Fatalf("ProgramDigest: %v", err)
	}
	want, _, err := Inspect(t.Context(), dir, "prog", testOptions())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got != want {
		t.Errorf("ProgramDigest = %s, Inspect = %s", got, want)
	}
	for _, name := range []string{"", "../prog", ".hidden", "missing"} {
		if _, err := ProgramDigest(dir, name); err == nil {
			t.Errorf("ProgramDigest(%q) succeeded", name)
		}
	}
	if _, err := ProgramDigest(filepath.Join(dir, "no-such-dir"), "prog"); err == nil {
		t.Error("a directory that does not exist was read")
	}
}

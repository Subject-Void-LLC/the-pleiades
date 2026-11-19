// Package hostfacts_test: the shared package facts' rules, held here because the
// gatherer and the methods that read back after a change must agree on them
// exactly, or the next gather reads as drift.
package hostfacts_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
)

func TestParseDpkg_KeepsOnlyInstalledRows(t *testing.T) {
	out := "ii\tcurl\t7.88.1-10\tamd64\nrc\told\t1.0\tamd64\nhi\theld\t2.0\tall\nii\tlibc6:amd64\t2.36\tamd64\nbroken line\n"
	got := hostfacts.ParseDpkg(out)
	want := []hostfacts.Package{
		{Name: "curl", Version: "7.88.1-10", Arch: "amd64", Source: hostfacts.SourceDpkg},
		{Name: "held", Version: "2.0", Arch: "all", Source: hostfacts.SourceDpkg},
		{Name: "libc6:amd64", Version: "2.36", Arch: "amd64", Source: hostfacts.SourceDpkg},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseDpkg = %+v, want %+v", got, want)
	}
}

func TestParseRPM_WritesTheEpochAndDropsKeys(t *testing.T) {
	out := "bash\t(none)\t5.2-1.el9\tx86_64\nvim\t2\t9.0-1\tx86_64\ngpg-pubkey\t(none)\tabc-def\t(none)\nnoarch\t0\t1.0-1\t(none)\n"
	got := hostfacts.ParseRPM(out)
	want := []hostfacts.Package{
		{Name: "bash", Version: "5.2-1.el9", Arch: "x86_64", Source: hostfacts.SourceRPM},
		{Name: "vim", Version: "2:9.0-1", Arch: "x86_64", Source: hostfacts.SourceRPM},
		{Name: "noarch", Version: "1.0-1", Arch: "", Source: hostfacts.SourceRPM},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRPM = %+v, want %+v", got, want)
	}
}

func TestPackageValue_LeavesOutWhatTheDatabaseDoesNotGive(t *testing.T) {
	got := hostfacts.PackageValue(hostfacts.Package{Name: "x", Source: hostfacts.SourceRPM})
	if !reflect.DeepEqual(got, map[string]any{"source": hostfacts.SourceRPM}) {
		t.Fatalf("PackageValue = %v, want the source alone", got)
	}
}

func TestItemKey_EncodesWhatCannotStandInAKey(t *testing.T) {
	for name, want := range map[string]string{
		"curl":        "package.curl",
		"a/b":         "package.a%2Fb",
		"a b":         "package.a%20b",
		"100%":        "package.100%25",
		"\xff":        "package.%FF",
		"libc6:amd64": "package.libc6:amd64",
	} {
		if got := hostfacts.ItemKey(hostfacts.PackageFamily, name); got != want {
			t.Errorf("ItemKey(%q) = %q, want %q", name, got, want)
		}
	}
}

//go:build unix

// Package loader: tests that the loader, and never a program, sets a method's
// provider.
package loader

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// TestLoad_TheLoaderSetsTheProvider proves every method the loader
// registers carries the program's path and the digest it was loaded with,
// and that a program cannot say otherwise: its description arrives with
// extra fields claiming to be built in and without a provider, and they
// change nothing. The engine keeps a third party's check away from a
// simulate-locked device on the strength of this field.
func TestLoad_TheLoaderSetsTheProvider(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.provider.run"
	out := describeJSON(t, 0, external.DescribedMethod{Name: fqcn, Manifest: implemented(true)})
	out = strings.Replace(out, `"name":`, `"provider":null,"builtin":true,"Provider":{"Program":""},"name":`, 1)
	if !strings.Contains(out, `"builtin":true`) {
		t.Fatalf("the description was not rewritten, so this test would prove nothing: %s", out)
	}
	path := writeProgram(t, dir, "provider", script(out, `printf '{}' >&3`))

	d := loadOne(t, dir, fqcn, testOptions())
	if d.Provider == nil {
		t.Fatal("the loader registered an external method with no provider")
	}
	if d.Provider.Program != path {
		t.Errorf("Provider.Program = %q, want %q", d.Provider.Program, path)
	}
	digest, err := inspectProgram(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Provider.Digest != digest {
		t.Errorf("Provider.Digest = %q, want the digest the program was loaded with, %q", d.Provider.Digest, digest)
	}
}

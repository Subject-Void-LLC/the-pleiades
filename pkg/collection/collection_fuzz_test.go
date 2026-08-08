package collection_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// FuzzRegister drives Register with arbitrary name strings, including
// empty namespaces, bare names, and duplicate registration, proving it
// never panics regardless of what a caller registers under -- only
// MustRegister panics, and only deliberately, on an error Register itself
// already returned cleanly.
//
// The shared package-level registry persists across every call this fuzz
// target makes for the life of one test binary, so a well-formed name the
// mutation engine happens to generate twice is a legitimate duplicate on
// its second occurrence, not a bug -- Register rejecting it and Lookup
// still finding the first registration are both correct. A malformed name
// (no namespace, empty namespace, or empty method), by contrast, can never
// succeed on any occurrence, so it must always be rejected and never
// found. wellFormed below (computed the same way Register itself splits
// the name) is what tells these two cases apart.
func FuzzRegister(f *testing.F) {
	f.Add("")
	f.Add(".")
	f.Add("..")
	f.Add("install")
	f.Add(".install")
	f.Add("pkg.")
	f.Add("pkg.install")
	f.Add("pkg.apt.install")
	f.Add("\x00\xff.\x00\xff")

	f.Fuzz(func(t *testing.T, name string) {
		namespace, method, ok := strings.Cut(name, ".")
		wellFormed := ok && namespace != "" && method != ""

		err := collection.Register(collection.Descriptor{Name: name})

		if !wellFormed {
			if err == nil {
				t.Fatalf("Register(%q): malformed name was accepted", name)
			}
			if _, found := collection.Lookup(name); found {
				t.Fatalf("Register(%q) was rejected but the name is registered anyway", name)
			}
			return
		}

		if err == nil {
			// Freshly registered by this iteration: a second call now
			// must fail as a genuine same-iteration duplicate.
			if err2 := collection.Register(collection.Descriptor{Name: name}); err2 == nil {
				t.Fatalf("second Register(%q) succeeded; want a duplicate error", name)
			}
		}
		if _, found := collection.Lookup(name); !found {
			t.Fatalf("Lookup(%q): well-formed name not found after Register (err=%v)", name, err)
		}
	})
}

// FuzzRegisterRequiredCapability drives Register with arbitrary required
// capability names, proving an unknown one is always rejected and a known
// one is always accepted, never a panic either way.
func FuzzRegisterRequiredCapability(f *testing.F) {
	f.Add("SSHTransportCapable")
	f.Add("CiscoIOSCapable")
	f.Add("LinuxCapable")
	f.Add("")
	f.Add("NotARealCapability")

	var seq atomic.Int64
	f.Fuzz(func(t *testing.T, capName string) {
		d := collection.Descriptor{
			Name: fmt.Sprintf("fuzzcap.method%d", seq.Add(1)),
			Manifest: collection.Manifest{
				RequiredCapabilities: []capability.Name{capability.Name(capName)},
			},
		}

		_, known := capability.Lookup(capability.Name(capName))
		err := collection.Register(d)

		if known && err != nil {
			t.Fatalf("Register with a known capability %q unexpectedly failed: %v", capName, err)
		}
		if !known && err == nil {
			t.Fatalf("Register with unknown capability %q unexpectedly succeeded", capName)
		}
	})
}

//go:build unix

// Package loader: the parity between the loader's validation and
// collection.Register.
package loader

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// FuzzRegistrationParity is Phase 45's parity check between the loader's
// two passes: any method pass one (validateMethod) accepts, collection.
// Register accepts too, built exactly as pass two builds it
// (descriptorFor). If Register ever gains a rule pass one does not mirror,
// a directory could load halfway, and this fails with the method that
// shows it.
//
// The other direction does not hold, by design: pass one also refuses what
// only a third party is held to (a reserved namespace, a declared stub, an
// unsafe name or description, a malformed engine version), none of which
// Register knows about.
func FuzzRegistrationParity(f *testing.F) {
	for _, seed := range []struct {
		name, status, notes, capability, transport, engine, noCheck, param string
		reversible, supportsCheck                                          bool
	}{
		{"acme.motd.read", "implemented", "", string(capability.NameSSHTransport), "ssh", "", "", "", true, true},
		{"acme.motd.read", "implemented", "reads only", "", "", ">=1.0.0", "", "", false, false},
		{"acme.motd.read", "implemented", "", "", "", "", "", "", false, false},
		{"acme.motd.read", "declared", "", "", "", "", "", "", true, true},
		{"acme.motd.read", "implemented", "x", "NoSuchCapable", "", "", "", "", false, false},
		{"acme", "implemented", "x", "", "", "", "", "", false, false},
		{"fuzzparity.taken.run", "implemented", "x", "", "", "", "", "", false, false},
		{"pleiades.motd.read", "implemented", "x", "", "", "", "", "", false, true},
		{"acme.motd.read", "implemented", "x", "", "", "", "a reason it cannot be checked", "", false, true},
		{"acme.motd.read", "implemented", "x", "", "", "", "a reason it cannot be checked", "", false, false},
		{"acme.motd.read", "implemented", "x", "", "", "", "", collection.TargetParam, false, false},
		{"acme.motd.read", "implemented", "x", "", "", "", "", "path", false, false},
	} {
		f.Add(seed.name, seed.status, seed.notes, seed.capability, seed.transport, seed.engine, seed.noCheck, seed.param, seed.reversible, seed.supportsCheck)
	}
	f.Fuzz(func(t *testing.T, name, status, notes, capName, transport, engine, noCheck, param string, reversible, supportsCheck bool) {
		defer collection.SnapshotForTest()()
		taken := collection.Descriptor{
			Name:     "fuzzparity.taken.run",
			Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"}},
			Invoke:   (&program{}).method("fuzzparity.taken.run", collection.ModeExecute),
		}
		if err := collection.Register(taken); err != nil {
			t.Fatalf("registering the fixture: %v", err)
		}

		m := external.DescribedMethod{Name: name, Manifest: collection.Manifest{
			Status:        collection.Status(status),
			Reversibility: collection.Reversibility{Reversible: reversible, Notes: notes},
			SupportsCheck: supportsCheck,
			NoCheckReason: noCheck,
			EngineVersion: engine,
		}}
		if capName != "" {
			m.Manifest.RequiredCapabilities = []capability.Name{capability.Name(capName)}
		}
		if transport != "" {
			m.Manifest.SupportedTransports = []string{transport}
		}
		if param != "" {
			m.Manifest.Doc.Params = []collection.Param{{Name: param, Type: "string", Description: "fuzzed"}}
		}
		if _, err := validateMethod(m, "dev", reservedNamespaces()); err != nil {
			return
		}
		c := candidate{path: "/fuzz/program", digest: "sha256:fuzz"}
		if err := collection.Register(descriptorFor(&program{path: c.path}, c, m)); err != nil {
			t.Fatalf("pass one accepted %q (%+v) and Register refused it: %v", name, m.Manifest, err)
		}
	})
}

// TestRegister_ARuleOnlyRegisterHasNamesWhatStaysRegistered covers the
// failure the parity fuzz exists to prevent, should it ever happen: when
// Register refuses a method pass one accepted, the error names the
// program, the refused method, and every method registered before it,
// since those stay registered and an operator has to know which.
func TestRegister_ARuleOnlyRegisterHasNamesWhatStaysRegistered(t *testing.T) {
	requireConfinement(t)
	dir := programDir(t)
	manifest := implemented(false)
	out := describeJSON(t, 0,
		external.DescribedMethod{Name: "loadertest.injected.one", Manifest: manifest},
		external.DescribedMethod{Name: "loadertest.injected.two", Manifest: manifest},
		external.DescribedMethod{Name: "loadertest.injected.three", Manifest: manifest},
	)
	writeProgram(t, dir, "injected", script(out, "true"))

	t.Cleanup(collection.SnapshotForTest())
	injected := errors.New("a rule only Register has")
	real := registerMethod
	t.Cleanup(func() { registerMethod = real })
	registerMethod = func(d collection.Descriptor) error {
		if d.Name == "loadertest.injected.three" {
			return injected
		}
		return real(d)
	}

	_, err := Load(t.Context(), dir, testOptions())
	if !errors.Is(err, injected) {
		t.Fatalf("Load = %v, want the injected refusal", err)
	}
	for _, want := range []string{`registering "loadertest.injected.three" failed after validation passed`, "2 method(s) registered before it stay registered (loadertest.injected.one, loadertest.injected.two)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	for _, name := range []string{"loadertest.injected.one", "loadertest.injected.two"} {
		if _, ok := collection.Lookup(name); !ok {
			t.Errorf("%s is not registered, so the error's list is wrong", name)
		}
	}
}

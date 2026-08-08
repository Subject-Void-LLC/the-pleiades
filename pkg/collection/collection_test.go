package collection_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

func TestRegisterAndLookup(t *testing.T) {
	d := collection.Descriptor{
		Name:     "test.register_and_lookup",
		Manifest: collection.Manifest{Status: collection.StatusDeclared},
	}

	if err := collection.Register(d); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	got, ok := collection.Lookup(d.Name)
	if !ok {
		t.Fatalf("Lookup(%q): not found", d.Name)
	}
	if got.Name != d.Name || got.Manifest.Status != collection.StatusDeclared {
		t.Errorf("Lookup(%q) = %+v; want %+v", d.Name, got, d)
	}
}

func TestLookup_Miss(t *testing.T) {
	if _, ok := collection.Lookup("test.never_registered"); ok {
		t.Error("Lookup on an unregistered name: found, want not found")
	}
}

// TestRegister_RejectsBareName is this phase's Release Gate: a bare
// method name is rejected with a message naming the actual requirement,
// not a citation into an internal document a user never receives.
func TestRegister_RejectsBareName(t *testing.T) {
	err := collection.Register(collection.Descriptor{Name: "install"})
	if err == nil {
		t.Fatal("Register(bare name): expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "not namespaced") {
		t.Errorf("error %q does not explain the namespacing requirement", err.Error())
	}

	if _, ok := collection.Lookup("install"); ok {
		t.Error("bare name was registered despite the rejection")
	}
}

func TestRegister_RejectsEmptyNamespace(t *testing.T) {
	if err := collection.Register(collection.Descriptor{Name: ".install"}); err == nil {
		t.Fatal("Register(\".install\"): expected an error, got nil")
	}
}

func TestRegister_RejectsEmptyMethod(t *testing.T) {
	if err := collection.Register(collection.Descriptor{Name: "pkg."}); err == nil {
		t.Fatal("Register(\"pkg.\"): expected an error, got nil")
	}
}

func TestRegister_RejectsUnknownCapability(t *testing.T) {
	d := collection.Descriptor{
		Name: "test.unknown_capability",
		Manifest: collection.Manifest{
			RequiredCapabilities: []capability.Name{"NotARealCapability"},
		},
	}

	err := collection.Register(d)
	if err == nil {
		t.Fatal("Register with an unknown required capability: expected an error, got nil")
	}

	if _, ok := collection.Lookup(d.Name); ok {
		t.Error("descriptor with an unknown capability was registered despite the rejection")
	}
}

// TestRegister_RejectsImplementedWithoutInvoke is the guardrail
// collection.go's Register documents: a Manifest claiming
// StatusImplemented with a nil Invoke would otherwise panic the first time
// a runbook dispatched to it, so Register refuses it at registration time
// instead.
func TestRegister_RejectsImplementedWithoutInvoke(t *testing.T) {
	d := collection.Descriptor{
		Name:     "test.implemented_without_invoke",
		Manifest: collection.Manifest{Status: collection.StatusImplemented},
		Invoke:   nil,
	}

	err := collection.Register(d)
	if err == nil {
		t.Fatal("Register with StatusImplemented and a nil Invoke: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), string(collection.StatusImplemented)) {
		t.Errorf("error %q does not name the status that requires an implementation", err.Error())
	}

	if _, ok := collection.Lookup(d.Name); ok {
		t.Error("descriptor claiming StatusImplemented with no Invoke was registered despite the rejection")
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	d := collection.Descriptor{Name: "test.duplicate", Manifest: collection.Manifest{Status: collection.StatusDeclared}}

	if err := collection.Register(d); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}

	err := collection.Register(d)
	if err == nil {
		t.Fatal("second Register on the same name: expected an error, got nil")
	}

	got, ok := collection.Lookup(d.Name)
	if !ok || got.Manifest.Status != collection.StatusDeclared {
		t.Errorf("Lookup after a rejected duplicate = %+v, %v; the first registration must survive", got, ok)
	}
}

func TestMustRegister_PanicsOnDuplicate(t *testing.T) {
	d := collection.Descriptor{Name: "test.must_register_duplicate"}
	collection.MustRegister(d)

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister on a duplicate name did not panic")
		}
	}()
	collection.MustRegister(d)
}

func TestMustRegister_SucceedsOnFreshName(t *testing.T) {
	d := collection.Descriptor{Name: "test.must_register_fresh"}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MustRegister on a fresh, valid name panicked: %v", r)
		}
	}()
	collection.MustRegister(d)

	if _, ok := collection.Lookup(d.Name); !ok {
		t.Error("Lookup after MustRegister: not found")
	}
}

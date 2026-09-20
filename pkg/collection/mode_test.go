// Package collection_test: tests of Mode, MethodFor and the check-support
// rules.
package collection_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// invokeBody and checkBody are two distinguishable Method values, so a
// test can tell which one MethodFor handed back by calling it.
func invokeBody(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
	return collection.Result{Changed: true}, nil
}

func checkBody(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
	return collection.Result{Changed: false}, nil
}

// withCheck returns a registrable, implemented descriptor, with check
// support set as asked.
func withCheck(name string, supportsCheck bool, check collection.Method) collection.Descriptor {
	return collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true},
			SupportsCheck: supportsCheck,
		},
		Invoke: invokeBody,
		Check:  check,
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    collection.Mode
		wantErr bool
	}{
		{in: "", want: collection.ModeExecute},
		{in: "execute", want: collection.ModeExecute},
		{in: "check", want: collection.ModeCheck},
		// The one that matters most: a misspelling must not run for real.
		{in: "chekc", wantErr: true},
		{in: "Check", wantErr: true},
		{in: "simulate", wantErr: true},
		{in: "plan", wantErr: true},
	}
	for _, tc := range cases {
		got, err := collection.ParseMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestRegister_CheckSupportMustAgree(t *testing.T) {
	cases := []struct {
		name    string
		d       collection.Descriptor
		wantErr string
	}{
		{
			name: "declared without a function",
			d:    withCheck("test.check_no_func", true, nil),
			// The message names what is missing, not just that something is.
			wantErr: "no Check function",
		},
		{
			name:    "a function nobody declared",
			d:       withCheck("test.check_undeclared", false, checkBody),
			wantErr: "does not declare check support",
		},
		{
			name: "a declared stub claiming check support",
			d: collection.Descriptor{
				Name:     "test.check_stub",
				Manifest: collection.Manifest{Status: collection.StatusDeclared, SupportsCheck: true},
				Check:    checkBody,
			},
			wantErr: "not implemented",
		},
		{
			name: "a call-level answer on a method with no check",
			d: func() collection.Descriptor {
				d := withCheck("test.check_call_undeclared", false, nil)
				d.CheckCall = func(map[string]any) error { return nil }
				return d
			}(),
			wantErr: "carries a CheckCall function",
		},
		{
			name: "a check with a reason it cannot be checked",
			d: func() collection.Descriptor {
				d := withCheck("test.check_and_reason", true, checkBody)
				d.Manifest.NoCheckReason = "it cannot"
				return d
			}(),
			wantErr: "also a reason it cannot be checked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			err := collection.Register(tc.d)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Register = %v, want an error containing %q", err, tc.wantErr)
			}
			if _, ok := collection.Lookup(tc.d.Name); ok {
				t.Error("the contradictory descriptor was registered despite the refusal")
			}
		})
	}
}

func TestRegister_AcceptsBothCoherentShapes(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	partly := withCheck("test.check_partly", true, checkBody)
	partly.CheckCall = func(map[string]any) error { return collection.CannotCheck("fixture") }
	for _, d := range []collection.Descriptor{
		withCheck("test.check_supported", true, checkBody),
		withCheck("test.check_unsupported", false, nil),
		partly,
	} {
		if err := collection.Register(d); err != nil {
			t.Errorf("Register(%s): %v", d.Name, err)
		}
	}
}

func TestMethodFor(t *testing.T) {
	ctx := context.Background()

	supported := withCheck("test.method_for_supported", true, checkBody)
	unsupported := withCheck("test.method_for_unsupported", false, nil)

	// Execute reaches Invoke, which this fixture made report a change.
	m, err := supported.MethodFor(collection.ModeExecute)
	if err != nil {
		t.Fatalf("MethodFor(execute): %v", err)
	}
	if res, _ := m(ctx, nil, nil, nil); !res.Changed {
		t.Error("MethodFor(execute) did not return Invoke")
	}

	// Check reaches Check, which reports no change. Handing back Invoke
	// here is the failure this function exists to rule out.
	m, err = supported.MethodFor(collection.ModeCheck)
	if err != nil {
		t.Fatalf("MethodFor(check): %v", err)
	}
	if res, _ := m(ctx, nil, nil, nil); res.Changed {
		t.Error("MethodFor(check) returned Invoke, which would change the device during a dry run")
	}

	if _, err := unsupported.MethodFor(collection.ModeCheck); err == nil || !strings.Contains(err.Error(), "does not support check mode") {
		t.Errorf("MethodFor(check) on an unsupported method = %v, want a refusal naming it", err)
	}

	// SupportsCheck without a function, as a descriptor that bypassed
	// Register would carry, must still refuse rather than return nil.
	bypassed := withCheck("test.method_for_bypassed", true, nil)
	if m, err := bypassed.MethodFor(collection.ModeCheck); err == nil || m != nil {
		t.Errorf("MethodFor(check) with no Check function = %v, %v; want a refusal", m, err)
	}

	noInvoke := collection.Descriptor{Name: "test.method_for_no_invoke"}
	if _, err := noInvoke.MethodFor(collection.ModeExecute); err == nil {
		t.Error("MethodFor(execute) with no Invoke returned no error")
	}

	if _, err := supported.MethodFor(collection.Mode("plan")); err == nil {
		t.Error("MethodFor accepted a mode outside the closed set")
	}
}

// TestNoCheckAnswer covers the answer a method with no check gives: its
// own reason when it has one, and the bare fact otherwise.
func TestNoCheckAnswer(t *testing.T) {
	bare := withCheck("test.no_check_bare", false, nil)
	reasoned := withCheck("test.no_check_reasoned", false, nil)
	reasoned.Manifest.NoCheckReason = "what it changes is the device's to decide"
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(reasoned); err != nil {
		t.Fatalf("a method with no check and a reason why was refused: %v", err)
	}
	if got := bare.NoCheckAnswer(); got != "it does not declare check support" {
		t.Errorf("bare answer = %q", got)
	}
	if got := reasoned.NoCheckAnswer(); got != reasoned.Manifest.NoCheckReason {
		t.Errorf("reasoned answer = %q, want the method's own reason", got)
	}
}

// TestCannotCheck_IsFoundThroughWrapping pins the contract every reader of
// CannotCheck relies on: the engine, the external SDK's child and the
// native adapter all find it with errors.As, however far a method wrapped
// it, and the reason the method gave is what an operator reads.
func TestCannotCheck_IsFoundThroughWrapping(t *testing.T) {
	const reason = "the archive's source is not there yet"
	err := fmt.Errorf("archive.unarchive: %w", collection.CannotCheck(reason))

	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Fatalf("errors.As did not find a CannotCheckError in %v", err)
	}
	if cannot.Reason != reason {
		t.Errorf("Reason = %q, want %q", cannot.Reason, reason)
	}
	if want := "archive.unarchive: this call cannot be checked: " + reason; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if errors.As(fmt.Errorf("an ordinary failure"), &cannot) {
		t.Error("an ordinary error was taken for a CannotCheckError")
	}
}

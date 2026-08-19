package aws_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// TestAws_Registered proves the plugin is reachable through the
// shared registry. It does not prove the real binary can reach it: that
// needs a blank import in internal/inventory/plugins/builtins.go, which
// internal/archtest is what actually checks.
func TestAws_Registered(t *testing.T) {
	desc, ok := syncplugin.Lookup(aws.Name)
	if !ok {
		t.Fatalf("plugin %q is not registered", aws.Name)
	}
	if desc.New == nil {
		t.Fatal("registered descriptor has no constructor")
	}
	if desc.New() == nil {
		t.Fatal("constructor returned nil")
	}
	if desc.Description == "" {
		t.Error("registered descriptor has no description")
	}
}

// TestAws_NotImplemented proves every method refuses out loud
// while this plugin is still declared. Delete the cases as you implement
// them; a case that starts failing is telling you a method became real, not
// that the test is wrong.
func TestAws_NotImplemented(t *testing.T) {
	desc, ok := syncplugin.Lookup(aws.Name)
	if !ok {
		t.Fatalf("plugin %q is not registered", aws.Name)
	}
	if desc.Implemented() {
		t.Skip("plugin reports StatusImplemented; these declared-stage assertions no longer apply")
	}

	ctx := context.Background()

	tests := []struct {
		name string
		call func(p syncplugin.Plugin) error
	}{
		{
			name: "Connect",
			call: func(p syncplugin.Plugin) error {
				return p.Connect(ctx, syncplugin.Config{Name: aws.Name})
			},
		},
		{
			name: "Discover",
			call: func(p syncplugin.Plugin) error {
				_, err := p.Discover(ctx)
				return err
			},
		},
		{
			name: "Sync",
			call: func(p syncplugin.Plugin) error {
				_, err := p.Sync(ctx, nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(desc.New()); err == nil {
				t.Fatalf("%s returned nil; a declared plugin must refuse, never succeed", tt.name)
			}
		})
	}
}

// TestAws_ClassifyQuarantines proves Classify is the one method
// that must not return an error even while unimplemented. Section 6g
// requires an unplaceable device to stay visible for manual review, and
// syncplugin.Reconcile stops the whole sync on a Classify error.
func TestAws_ClassifyQuarantines(t *testing.T) {
	desc, ok := syncplugin.Lookup(aws.Name)
	if !ok {
		t.Fatalf("plugin %q is not registered", aws.Name)
	}
	if desc.Implemented() {
		t.Skip("plugin reports StatusImplemented; these declared-stage assertions no longer apply")
	}

	cls, err := desc.New().Classify(context.Background(), record.Record{Name: "any-device"})
	if err != nil {
		t.Fatalf("Classify must not error while declared, got %v", err)
	}
	if !cls.Quarantined() {
		t.Error("a declared plugin must quarantine every device rather than guess a type")
	}
	if cls.Reason == "" {
		t.Error("a quarantined classification must explain why")
	}
}

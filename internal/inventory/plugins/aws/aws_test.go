package aws_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws"
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
	if desc.New(syncplugin.Deps{}) == nil {
		t.Fatal("constructor returned nil")
	}
	if desc.Description == "" {
		t.Error("registered descriptor has no description")
	}
}

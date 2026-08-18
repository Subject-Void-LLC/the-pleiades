package pkg

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// This file is an internal test (package pkg, not pkg_test) specifically
// to reach dispatch's two refusal branches that the public Install,
// Remove and Upgrade functions can never reach on their own: they always
// call dispatch with a hardcoded verb ("install", "remove", "upgrade"),
// and every concrete method under both real managerNamespace entries
// (pkg.apt.*, pkg.dnf.*) is implemented today, so "not registered" and
// "declared but not implemented" are both genuinely unreachable through
// the public API. dispatch itself still needs to answer them correctly,
// for the day a verb is added to the generic layer before its concrete
// siblings exist.

type dispatchTestDevice struct {
	*inventorytest.Stub
	manager string
}

func (d *dispatchTestDevice) SSHHost() string            { return "127.0.0.1" }
func (d *dispatchTestDevice) SSHPort() int               { return 22 }
func (d *dispatchTestDevice) PackageManagerName() string { return d.manager }

func TestDispatch_TargetNotRegistered(t *testing.T) {
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}, manager: "apt"}
	_, err := dispatch(context.Background(), nil, dev, nil, "reinstall")
	if err == nil || !strings.Contains(err.Error(), "pkg.apt.reinstall is not registered") {
		t.Errorf("err = %v, want it to say pkg.apt.reinstall is not registered", err)
	}
}

func TestDispatch_TargetDeclaredButNotImplemented(t *testing.T) {
	const fake = "pkg.apt.faketest_dispatch_coverage"
	if err := collection.Register(collection.Descriptor{
		Name: fake,
		Manifest: collection.Manifest{
			RequiredCapabilities: []capability.Name{capability.NameApt},
			Status:               collection.StatusDeclared,
		},
	}); err != nil {
		t.Fatalf("registering the test fixture: %v", err)
	}

	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}, manager: "apt"}
	_, err := dispatch(context.Background(), nil, dev, nil, "faketest_dispatch_coverage")
	if err == nil || !strings.Contains(err.Error(), "declared but not implemented yet") {
		t.Errorf("err = %v, want it to say declared but not implemented", err)
	}
}

// Tests for a Linux server's file transfer root, in both directions.
package linux_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// fileTransferServer builds a linux_server whose only property is
// file_transfer_root set to value.
func fileTransferServer(value inventory.PropertyValue) (inventory.InventoryItem, error) {
	return linux.NewServer(record.Record{
		ID: "s1", Name: "files1", Type: "linux_server",
		Properties: map[string]inventory.PropertyValue{linux.FileTransferRootProperty: value},
	})
}

// TestFileTransferRoot_DeclaredOnlyWhenSet covers both directions of
// the data half: a server nobody gave a root does not claim the
// capability, and one that has a root does, with the structural half
// returning exactly that root.
func TestFileTransferRoot_DeclaredOnlyWhenSet(t *testing.T) {
	bare, err := linux.NewServer(record.Record{ID: "s0", Name: "bare", Type: "linux_server"})
	if err != nil {
		t.Fatal(err)
	}
	if bare.HasCapability(capability.NameFileTransfer) {
		t.Error("a linux_server with no file_transfer_root claims FileTransferCapable")
	}
	if got := bare.(capability.FileTransferCapable).FileTransferRoot(); got != "" {
		t.Errorf("FileTransferRoot() with no property = %q, want the empty string", got)
	}

	for _, root := range []string{"/srv/xfer", "/", "/data/firmware images"} {
		item, err := fileTransferServer(root)
		if err != nil {
			t.Fatalf("NewServer(file_transfer_root %q) error = %v", root, err)
		}
		if !item.HasCapability(capability.NameFileTransfer) {
			t.Errorf("a linux_server with file_transfer_root %q does not claim FileTransferCapable", root)
		}
		ft, ok := item.(capability.FileTransferCapable)
		if !ok || ft.FileTransferRoot() != root {
			t.Errorf("FileTransferRoot() = %q, want %q", ft.FileTransferRoot(), root)
		}
		if _, err := filexfer.Resolve(ft.FileTransferRoot(), "image.bin"); err != nil {
			t.Errorf("the root %q the device reports does not resolve: %v", root, err)
		}
	}
}

// TestFileTransferRoot_InvalidValueIsRefusedAtLoad moves a bad root's
// failure from the first transfer to the moment inventory loads, and
// names the device and the property.
func TestFileTransferRoot_InvalidValueIsRefusedAtLoad(t *testing.T) {
	for _, bad := range []inventory.PropertyValue{
		"", "srv/xfer", "/srv/xfer/", "/srv/../etc", "/srv\x00x", `C:\xfer`, 42, true,
	} {
		_, err := fileTransferServer(bad)
		if err == nil {
			t.Errorf("NewServer(file_transfer_root %#v) error = nil, want a refusal", bad)
			continue
		}
		if !strings.Contains(err.Error(), "files1") || !strings.Contains(err.Error(), linux.FileTransferRootProperty) {
			t.Errorf("NewServer(file_transfer_root %#v) error = %q, want it to name the device and the property", bad, err)
		}
	}
	_, err := fileTransferServer("relative")
	var pathErr *filexfer.PathError
	if !errors.As(err, &pathErr) || pathErr.Refusal != filexfer.RefusedRelativeRoot {
		t.Errorf("a relative root's error = %v, want it to carry the resolver's own refusal", err)
	}
}

// TestFileTransferRoot_ClassifiedWithoutPropertyFailsClosed covers the
// one way to hold the capability with no root: classification, which can
// only add. The accessor then reports no root, and a transfer is
// refused by the resolver rather than landing anywhere.
func TestFileTransferRoot_ClassifiedWithoutPropertyFailsClosed(t *testing.T) {
	item, err := linux.NewServer(record.Record{
		ID: "s2", Name: "classified", Type: "linux_server",
		Capabilities: []capability.Name{capability.NameFileTransfer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasCapability(capability.NameFileTransfer) {
		t.Fatal("a classified linux_server does not claim FileTransferCapable")
	}
	root := item.(capability.FileTransferCapable).FileTransferRoot()
	_, err = filexfer.Resolve(root, "f")
	var pathErr *filexfer.PathError
	if !errors.As(err, &pathErr) || pathErr.Part != filexfer.PartRoot || pathErr.Refusal != filexfer.RefusedEmpty {
		t.Fatalf("Resolve with a classified server's empty root error = %v, want an empty-root refusal", err)
	}
}

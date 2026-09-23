// A Linux server's file transfer root: the accessor that satisfies
// capability.FileTransferCapable, and the data half that decides when the
// server declares it.
package linux

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// FileTransferRootProperty is the inventory property naming the one
// directory file transfers to and from a Linux server are confined to.
const FileTransferRootProperty = "file_transfer_root"

// FileTransferRoot returns the directory every file transfer to or from
// this server is confined to, satisfying capability.FileTransferCapable.
//
// It has no default, deliberately, the same choice WorkingDirectory
// makes. A default of "/" would confine nothing, and the root's whole
// value is being an operator's explicit decision about where a runbook
// may write; an operator who does want the whole filesystem can say "/"
// on purpose. An unset property returns the empty string, which
// filexfer.Resolve refuses by name, so a transfer against a server
// nobody configured fails closed rather than landing somewhere a default
// chose.
func (l *Server) FileTransferRoot() string {
	root, _ := l.Properties().String(FileTransferRootProperty)
	return root
}

// fileTransferBaseline returns baseline with capability.NameFileTransfer
// appended when the record sets file_transfer_root, and refuses a value
// that is set but unusable.
//
// This is the DATA half of FileTransferCapable, the shape
// cisco.Router's netconfBaseline established (FAILURE_PATTERNS 204):
// FileTransferRoot proves the structural half, and a server declares the
// capability only when an operator has actually chosen a root. Declaring
// it on every Linux server instead would let a method requiring it pass
// plan-time validation against a server with no root and fail at run
// time, which is the check this capability exists to move left.
//
// A value that is present but unusable (not a string, relative, not in
// its simplest form, holding a control character or a backslash) is
// refused here, when inventory loads, rather than by the first transfer.
// A server can still reach the capability without the property through
// classification, which can only add; FileTransferRoot then returns the
// empty string and every transfer is refused by filexfer.Resolve.
func fileTransferBaseline(rec record.Record, baseline []capability.Name) ([]capability.Name, error) {
	if _, present := rec.Properties[FileTransferRootProperty]; !present {
		return baseline, nil
	}
	root, ok := inventory.NewProperties(rec.Properties).String(FileTransferRootProperty)
	if !ok {
		return nil, fmt.Errorf("linux_server %q: property %s must be a string", rec.Name, FileTransferRootProperty)
	}
	if err := filexfer.ValidateRoot(root); err != nil {
		return nil, fmt.Errorf("linux_server %q: property %s: %w", rec.Name, FileTransferRootProperty, err)
	}
	return append(baseline, capability.NameFileTransfer), nil
}

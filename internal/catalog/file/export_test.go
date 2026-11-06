// Package file: what the external tests reach inside the package.
package file

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// SetRemovableKnown replaces how file.remove's check asks whether a path may
// be removed, and returns what restores it: a test gives the answer a
// device's shell gives where it cannot tell, which this machine cannot be.
func SetRemovableKnown(f func(context.Context, *remoteexec.Conn, string) (bool, bool, error)) func() {
	old := removableKnown
	removableKnown = f
	return func() { removableKnown = old }
}

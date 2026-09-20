//go:build !unix

// Package loader: Load on a platform external Collections do not run on
// yet.
package loader

import (
	"context"
	"errors"
	"runtime"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// errUnsupported is every refusal on this platform (unsupportedMessage).
var errUnsupported = errors.New(unsupportedMessage(runtime.GOOS))

// Load refuses on this platform. The Unix loader relies on file
// ownership and permission bits to decide who may put a program in the
// directory, on confining each program it starts, and on handing the
// program a response pipe as file descriptor 3; none has an equivalent
// written here. Refusing loudly beats loading programs without the checks
// that make loading them safe. It is only called when
// PLEIADES_COLLECTIONS_DIR is set, so a user who never asked for external
// Collections sees nothing.
func Load(_ context.Context, _ string, _ Options) (*Set, error) {
	return nil, errUnsupported
}

// ReadApprovals refuses on this platform, as Load does.
func ReadApprovals(_ string) ([]Approval, error) {
	return nil, errUnsupported
}

// Approve refuses on this platform, as Load does.
func Approve(_ string, _ Approval) error {
	return errUnsupported
}

// Revoke refuses on this platform, as Load does.
func Revoke(_, _, _ string) (int, error) {
	return 0, errUnsupported
}

// Inspect refuses on this platform, as Load does.
func Inspect(_ context.Context, _, _ string, _ Options) (string, external.Description, error) {
	return "", external.Description{}, errUnsupported
}

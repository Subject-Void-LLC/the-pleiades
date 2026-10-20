// Package engine: whether a task's failure is its device never answering.
package engine

import "errors"

// unreachedError is a transport's failure that knows whether the device
// never answered (pkg/remoteexec.DialError, pkg/winrmexec.NotStartedError).
type unreachedError interface {
	error
	Unreached() bool
}

// Unreached reports whether err, anywhere in its chain, is a transport's
// failure saying the device never answered: nothing was sent, so nothing
// ran. A refused login is an answer, and is not.
func Unreached(err error) bool {
	var u unreachedError
	return errors.As(err, &u) && u.Unreached()
}

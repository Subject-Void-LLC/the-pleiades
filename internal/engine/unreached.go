// Package engine: whether a task's failure is its device never answering,
// or its device (or this side) refusing the login.
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

// refusedError is a transport's failure that knows whether it was a
// refusal (pkg/remoteexec.DialError, pkg/winrmexec.NotStartedError,
// pkg/telnetexec.ErrLoginRefused, and wire.ChildResponse.Failure for one
// that crossed a process boundary).
type refusedError interface {
	error
	Refused() bool
}

// Refused reports whether err, anywhere in its chain, is a transport's
// refusal: the device turned down the login, or this side turned down its
// host key or certificate. The answer is the same however often it is
// asked, and each time with a password is one more failed login toward the
// account's lockout, so a task does not run again for its until, or for
// its loop's next item, once one of its runs ends this way.
func Refused(err error) bool {
	var r refusedError
	return errors.As(err, &r) && r.Refused()
}

// Package awscloud: the error an AWS SDK call's failure is reported as, with
// AWS's own words bounded and quoted and the SDK's error kept underneath.
package awscloud

import "github.com/Subject-Void-LLC/the-pleiades/pkg/farside"

// sdkError is an AWS SDK call's failure as this package reports it: what
// the call was doing, then the SDK's own text, bounded and quoted through
// pkg/farside. The SDK's text carries AWS's parsed error message, which is
// the far side's words, so it is held to farside.MaxBytes and a control
// byte in it is shown escaped rather than sent to a terminal.
//
// Unwrap returns the SDK's error untouched, so errors.As still finds a
// smithy.APIError (errorCode and AccessDenied read its code that way) and
// errors.Is still finds context.Canceled or context.DeadlineExceeded inside
// a request the caller gave up on.
type sdkError struct {
	// op says what the call was doing, such as `listing bucket "b"`.
	op string
	// err is the SDK's error, never rewritten.
	err error
}

// Error reads "awscloud: <op>: <the SDK's text, quoted and bounded>".
func (e *sdkError) Error() string {
	return "awscloud: " + e.op + ": " + farside.Text(e.err.Error())
}

// Unwrap returns the SDK's own error, so errors.As and errors.Is see
// through this one.
func (e *sdkError) Unwrap() error {
	return e.err
}

// wrapSDK returns err as an sdkError for op. Each caller has already
// checked err is not nil.
func wrapSDK(op string, err error) error {
	return &sdkError{op: op, err: err}
}

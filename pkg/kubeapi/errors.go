// What a cluster's refusals mean, said in an operator's terms.
package kubeapi

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrCredentialRefused marks an answer that the credential itself was
// refused (401), which no retry can change: like a refused SSH login, it is
// neither retried nor counted as the cluster being unreachable.
var ErrCredentialRefused = errors.New("the API server refused this credential")

// ErrForbidden marks an answer that the credential is valid but may not do
// what was asked (403), which no retry changes either.
var ErrForbidden = errors.New("the API server forbids this credential the request")

// maxServerErrorText bounds how much of an error's text, which carries
// whatever the server answered (client-go quotes a whole undecodable
// body), an explained error keeps.
const maxServerErrorText = 512

// Explain wraps err with what a 401 or 403 means, keeping the server's own
// message beneath, bounded and with anything a terminal would act on
// escaped (serverError); err itself stays reachable for errors.Is and
// apierrors' checks.
func Explain(err error) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsUnauthorized(err):
		return fmt.Errorf("%w (401: it may be expired, revoked or for another cluster): %w", ErrCredentialRefused, bound(err))
	case apierrors.IsForbidden(err):
		return fmt.Errorf("%w (403): %w", ErrForbidden, bound(err))
	}
	return bound(err)
}

// serverError is an error whose text is a bounded, escaped copy of the
// error it wraps.
type serverError struct {
	err  error
	text string
}

func (e *serverError) Error() string { return e.text }
func (e *serverError) Unwrap() error { return e.err }

// bound returns err with its text cut to maxServerErrorText bytes and every
// rune that is not printable escaped, so a server cannot write control
// sequences into a log or a terminal, nor make an error of any size.
func bound(err error) error {
	raw := err.Error()
	var b strings.Builder
	for i, r := range raw {
		if b.Len() >= maxServerErrorText {
			fmt.Fprintf(&b, "... (%d more bytes)", len(raw)-i)
			break
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	if b.String() == raw {
		return err
	}
	return &serverError{err: err, text: b.String()}
}

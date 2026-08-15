// The adapter between the local credential store and the UI's sign-in
// port.
//
// It exists so internal/ui/web never imports internal/localauth. That
// package renders pages, and the one verb it needs from a password store is
// "does this pair prove a subject"; handing it the whole store would give a
// handler the ability to set passwords, unlock accounts and read lockout
// state, none of which a login page has any business doing.
//
// Adapting concrete implementations to the narrow interfaces their
// consumers declare is what a composition root is for, which is also why
// internal/archtest exempts cmd/ from the localauth consumer allowlist
// rather than listing this file in it.
package main

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// passwordAuthenticator adapts localauth.Store to uiweb.PasswordAuthenticator.
type passwordAuthenticator struct {
	store localauth.Store
}

// Authenticate proves an email and password pair and returns the subject.
//
// It discards the localauth.Account the store returns and keeps only the
// subject, which is the whole point of the adapter: the account carries
// lockout state and a must-change flag that the login handler has no
// decision to make about yet. When 79c adds a "you must change your
// password" interstitial, this return type widens deliberately rather than
// the handler having quietly had the data all along.
//
// The error is passed through unwrapped. localauth.Authenticate already
// answers identically for a wrong password, an unknown address and a locked
// account, so there is nothing here to flatten and wrapping it would only
// add a distinguishing message to something whose entire design is that it
// does not distinguish.
func (a passwordAuthenticator) Authenticate(ctx context.Context, email, password string) (string, error) {
	account, err := a.store.Authenticate(ctx, email, password)
	if err != nil {
		return "", err
	}
	return account.Subject, nil
}

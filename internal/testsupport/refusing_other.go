//go:build !unix

// Off Unix: no unlistened bind, so RefusingAddr skips.
package testsupport

import "errors"

// errNoUnlistenedBind is what bindUnlistened answers off Unix, where
// RefusingAddr skips rather than run socket calls nothing here verifies.
var errNoUnlistenedBind = errors.New("binding a port without listening on it needs Unix socket calls, which this platform lacks")

// bindUnlistened always answers errNoUnlistenedBind off Unix.
func bindUnlistened() (int, func() error, error) {
	return 0, nil, errNoUnlistenedBind
}

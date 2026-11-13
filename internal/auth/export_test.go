// This file exposes test-only seams to package auth_test. It is compiled
// only with the tests, so nothing here is part of the package's API.
package auth

import "time"

// WithClockForTest replaces the clock a JWKS provider measures key age and
// its fetch cooldown on, so a test can move past a 12 hour staleness bound
// or a one minute cooldown without waiting for either.
func WithClockForTest(now func() time.Time) JWKSOption {
	return func(p *jwksKeyProvider) { p.now = now }
}

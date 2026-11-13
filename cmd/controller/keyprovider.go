// This file chooses how the controller checks an API token's signature:
// against an identity provider's published keys, or against one static
// secret.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// errBothTokenSources is the refusal for a controller given both ways of
// checking a token.
//
// Each one alone decides who may sign a token this controller accepts, so
// setting both is two answers to one question, and ranking them hid the
// second (FAILURE_PATTERNS 1229). `controller setup` writes JWT_SECRET into
// the compose .env file and the Helm Secret unless it is given --jwks-url, so
// the likely way to get here is a deployment set up with a secret and then
// pointed at an identity provider by hand. The message says which to remove
// for each intention, and the command that does it, because leaving the
// secret in place means removing JWKS_URL on some later restart brings back a
// key that signs any token.
var errBothTokenSources = errors.New("JWKS_URL and JWT_SECRET are both set, and each one chooses how this controller checks API tokens, so set exactly one. " +
	"To check tokens against your identity provider, remove JWT_SECRET: `controller setup --jwks-url <url> --force` removes it from the compose .env file and writes JWKS_URL, " +
	"and the Helm chart reads none once controller.jwksUrl is set. A JWT_SECRET left in place signs any token again the moment JWKS_URL is removed. " +
	"To keep the static secret, remove JWKS_URL")

// loadKeyProvider builds the auth.KeyProvider this process verifies tokens
// against.
//
// JWKS_URL selects the Federated Identity path (auth.NewJWKSKeyProvider),
// which takes an https URL only. JWT_SECRET backs a static,
// development-only provider (auth.NewStaticKeyProvider), which fails closed
// on a short or empty secret rather than letting a forgeable key serve
// traffic. Exactly one must be set: both is a startup error rather than a
// precedence rule, as DB_DSN with DB_PATH is.
//
// PLAN.md Section 32.1 says a shared symmetric secret "must be rejected at
// startup in any other mode" than local development. No deploy-mode
// config surface (dev vs. production) exists anywhere in this codebase yet,
// so that stronger enforcement is not built here: an operator can choose
// JWKS today by setting JWKS_URL alone, but nothing yet forces the choice.
// Stated plainly as a deferred gap, not silently skipped.
func loadKeyProvider() (auth.KeyProvider, error) {
	jwksURL, jwtSecret := os.Getenv("JWKS_URL"), os.Getenv("JWT_SECRET")
	switch {
	case jwksURL != "" && jwtSecret != "":
		return nil, errBothTokenSources
	case jwksURL != "":
		return auth.NewJWKSKeyProvider(jwksURL)
	case jwtSecret != "":
		return auth.NewStaticKeyProvider([]byte(jwtSecret))
	default:
		return nil, fmt.Errorf("JWT_SECRET is required when JWKS_URL is not set")
	}
}

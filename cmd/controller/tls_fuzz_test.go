package main

import (
	"strings"
	"testing"
)

// FuzzResolveTLSNeverServesPlainHTTPByAccident is the fuzz target for the
// property this whole phase rests on.
//
// THE PROPERTY. resolveTLS has exactly one outcome that serves plain HTTP,
// tlsModeUpstream, and it may only be reached when an operator explicitly
// said an ingress already terminated TLS. No combination of environment
// strings may reach it any other way. That matters more than it sounds:
// the session cookie carries Secure and the __Host- prefix
// unconditionally, and a browser silently refuses such a cookie on a
// plain-HTTP origin that is not loopback, so a controller that fell into
// plain HTTP by accident would render "Those credentials were not
// accepted" to an operator who typed the right password. The failure
// blames the user for the deployment's mistake, which is why it has to be
// unreachable rather than merely unlikely.
//
// WHY FUZZING RATHER THAN A TABLE. There is already a table test for the
// five documented cases. What a table cannot cover is the input space of
// the truthiness parser: upstreamTerminated lowercases and trims, accepts
// four spellings of yes and four of no, and rejects everything else. Every
// bug in that shape is a string somebody did not think to write down. A
// fuzzer writes them down.
//
// The three variables are fuzzed together rather than separately because
// the interesting cases are the interactions: a cert path with an upstream
// claim, one half of the pair with a truthy claim, whitespace that makes
// an empty value look set.
func FuzzResolveTLSNeverServesPlainHTTPByAccident(f *testing.F) {
	// The documented table, as seeds, so the corpus starts from the cases
	// that are supposed to work rather than from noise alone.
	f.Add("/tls/cert.pem", "/tls/key.pem", "")    // serve the operator's pair
	f.Add("", "", "1")                            // upstream terminated
	f.Add("/tls/cert.pem", "/tls/key.pem", "1")   // both arrangements: error
	f.Add("/tls/cert.pem", "", "")                // half a pair: error
	f.Add("", "/tls/key.pem", "")                 // the other half: error
	f.Add("", "", "")                             // nothing set: self-provision
	f.Add("", "", "TRUE")                         // case folding
	f.Add("", "", "  on  ")                       // trimming
	f.Add("", "", "maybe")                        // not a truth value: error
	f.Add("", "", "2")                            // not a truth value: error
	f.Add(" ", " ", "")                           // whitespace is not emptiness
	f.Add("", "", "\x00")                         // a NUL is not a yes
	f.Add("", "", "1\n")                          // a trailing newline, as a file would give
	f.Add("/tls/cert.pem", "/tls/key.pem", "off") // explicit no, with a pair
	f.Add("", "", strings.Repeat("1", 1<<10))     // an oversized value
	f.Add("\x80\x81", "\x82", "\xff")             // invalid UTF-8 throughout

	f.Fuzz(func(t *testing.T, certFile, keyFile, upstream string) {
		// A NUL cannot occur in a real environment variable: it is the
		// terminator the C environment is built on, and os.Setenv refuses
		// one outright. Fuzzing over it would be fuzzing an input the
		// operating system can never deliver, and the only thing it can
		// prove is that t.Setenv rejects it, which is not a fact about
		// this code. Everything else, including invalid UTF-8, is fair
		// game and stays in the corpus.
		for _, v := range []string{certFile, keyFile, upstream} {
			if strings.ContainsRune(v, 0) {
				return
			}
		}

		t.Setenv("TLS_CERT_FILE", certFile)
		t.Setenv("TLS_KEY_FILE", keyFile)
		t.Setenv(upstreamVar, upstream)

		settings, err := resolveTLS()
		if err != nil {
			// A refusal is always a safe answer: the process does not start,
			// so it serves nothing at all.
			return
		}

		// Every accepted outcome must be one of the three modes. A zero or
		// unknown mode reaching a caller would be decided by whatever
		// ServesTLS happens to return for it.
		switch settings.Mode {
		case tlsModeServe, tlsModeSelfProvisioned, tlsModeUpstream:
		default:
			t.Fatalf("resolveTLS accepted cert=%q key=%q %s=%q and returned mode %v, which is not one of the three documented modes",
				certFile, keyFile, upstreamVar, upstream, settings.Mode)
		}

		if settings.ServesTLS() {
			// Serving TLS is the safe direction; the only requirement is
			// that it names something to serve.
			if settings.Mode == tlsModeServe && (settings.CertFile == "" || settings.KeyFile == "") {
				t.Fatalf("resolveTLS chose to serve an operator-supplied pair from cert=%q key=%q but returned CertFile=%q KeyFile=%q; "+
					"a serve mode with no material is a listener that cannot start",
					certFile, keyFile, settings.CertFile, settings.KeyFile)
			}
			return
		}

		// From here down the process would serve PLAIN HTTP. Two things must
		// both be true, and neither is negotiable.

		// One: the operator asked for it, in one of the spellings the
		// documented parser accepts. Anything else reaching this line means
		// a value nobody wrote as a yes was read as one.
		switch strings.ToLower(strings.TrimSpace(upstream)) {
		case "1", "true", "yes", "on":
		default:
			t.Fatalf("resolveTLS chose PLAIN HTTP with %s=%q, which is not one of the four accepted yes values; "+
				"the one mode that turns off transport encryption must be reachable only by an explicit request",
				upstreamVar, upstream)
		}

		// Two: no certificate was configured. An operator who named a
		// certificate AND claimed upstream termination has written down two
		// different intentions, and resolveTLS is documented to refuse that
		// rather than silently preferring one. Reaching plain HTTP here
		// would silently discard a certificate somebody deliberately
		// supplied.
		if certFile != "" || keyFile != "" {
			t.Fatalf("resolveTLS chose PLAIN HTTP while cert=%q key=%q were set; the both-arrangements-at-once case is documented as a startup error, not a precedence rule",
				certFile, keyFile)
		}
	})
}

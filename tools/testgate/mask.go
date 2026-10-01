// Keeping secrets out of what testgate prints and writes to a job summary.
package main

import (
	"io"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// testOutput is where testgate echoes what tests print. run replaces it
// with a writer that masks through the shared ruleset; it starts as the
// plain stream only so a caller that never ran run still has somewhere to
// write.
var testOutput io.Writer = os.Stdout

// maskEnvironment teaches the shared ruleset the value of every
// environment variable whose name it calls secret, so the test output
// testgate prints and the job summary it writes mask that value wherever a
// test printed it. In CI that is LOCALSTACK_AUTH_TOKEN, which the container
// jobs hand their tests; the ruleset also masks by shape (a PEM block, a
// JWT, a URL carrying a password), whatever the source. GitHub masks the
// secrets it was given in logs and summaries too, but only those, and a
// secret never belongs in a new place on the strength of somebody else's
// masking.
func maskEnvironment() {
	literals := redact.Shared().Literals()
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if ok && len(value) >= redact.MinLiteralLength && redact.SecretName(name) {
			literals.Add(value)
		}
	}
	testOutput = redact.Shared().Writer(os.Stdout)
}

// scrub masks text through the shared ruleset before testgate prints it or
// writes it to the job summary.
func scrub(text string) string {
	return redact.Text(nil, text)
}

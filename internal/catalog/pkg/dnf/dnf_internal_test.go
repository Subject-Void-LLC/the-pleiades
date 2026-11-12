// Internal tests of the guard every dnf command keeps for itself: dnf gets
// no "--" before its operands (dnf5 refuses one), so runDnf, hasUpdate and
// requirePackage each refuse an operand dnf would read as an option or a
// pattern before anything is sent, whatever a caller skipped
// (FAILURE_PATTERNS 1174).
package dnf

import (
	"context"
	"strings"
	"testing"
)

// TestCommands_RefuseAnOperandDnfWouldMisread: the connection is nil, so a
// command that got past its own check would panic sending, rather than
// return the refusal.
func TestCommands_RefuseAnOperandDnfWouldMisread(t *testing.T) {
	ctx := context.Background()
	for _, word := range []string{"--setopt=tsflags=noscripts", "-x", "nginx-*"} {
		_, runErr := runDnf(ctx, nil, []string{"install", "-y"}, "curl", word)
		_, updateErr := hasUpdate(ctx, nil, word)
		packageErr := requirePackage(ctx, nil, word)
		for label, err := range map[string]error{"runDnf": runErr, "hasUpdate": updateErr, "requirePackage": packageErr} {
			if err == nil || !strings.Contains(err.Error(), "dnf: package is refused") {
				t.Errorf("%s(%q) = %v, want the operand refused before sending", label, word, err)
			}
		}
	}
}

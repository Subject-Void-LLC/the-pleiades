// Tests of how a device's refusal reaches a net.ios.* error: in its own
// words, masked, quoted and bounded (Phase 131's R1).
package ios

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// TestDeviceRefusal_KeepsTheWordsAndBoundsThem: the device's "%" line is
// what is quoted; a reply with none is quoted whole, never raw; a control
// byte is shown escaped; a long reply is held to farside's bound; and a
// secret the reply echoes is masked before the bound cuts anything.
func TestDeviceRefusal_KeepsTheWordsAndBoundsThem(t *testing.T) {
	if got := deviceRefusal("dir flash:\r\n% Invalid input detected at '^' marker.\r\nR1#"); got != `"% Invalid input detected at '^' marker."` {
		t.Errorf("a marked refusal = %s", got)
	}
	long := deviceRefusal("no marker here \x1b[2J" + strings.Repeat("x", 4000))
	if !strings.Contains(long, `no marker here \x1b[2J`) || strings.ContainsRune(long, 0x1b) || len(long) > farside.MaxBytes+5 {
		t.Errorf("an unmarked, hostile reply = %d bytes: %.120s", len(long), long)
	}
	if got := deviceRefusal("% Invalid input: username u secret 0 hunter2"); strings.Contains(got, "hunter2") {
		t.Errorf("an echoed secret reached the error: %s", got)
	}
	if got := deviceRefusal(""); got != farside.NoOutput {
		t.Errorf("an empty reply = %s, want %s", got, farside.NoOutput)
	}
}

// Tests that every --tls-keylog example in the command tree runs as shown:
// the flag refuses a key log inside the project, and a relative name is read
// from the directory the command runs in, which is the project, so an
// example names an absolute path or one under the home directory.
package clispec

import (
	"strings"
	"testing"
)

// TestExamples_KeyLogGoesOutsideTheProject holds each example passing
// --tls-keylog to a path that is not relative to the project, and asks that
// run and adhoc each show one.
func TestExamples_KeyLogGoesOutsideTheProject(t *testing.T) {
	shown := map[string]bool{}
	walk(Root, func(c Command) {
		for _, ex := range c.Examples {
			words := strings.Fields(ex)
			for i, w := range words {
				if w != "--tls-keylog" {
					continue
				}
				shown[c.Name] = true
				if i+1 == len(words) {
					t.Errorf("%q: --tls-keylog names no file", ex)
					continue
				}
				if p := words[i+1]; !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") {
					t.Errorf("%q names the key log %s, relative to the project the command runs in, which the flag refuses: name a path outside it", ex, p)
				}
			}
		}
	})
	for _, name := range []string{"run", "adhoc"} {
		if !shown[name] {
			t.Errorf("%s shows no --tls-keylog example", name)
		}
	}
	if !strings.Contains(tlsKeyLogDoc, "inside the project") || !strings.Contains(tlsKeyLogDoc, "Windows") {
		t.Errorf("the flag's documentation does not say a key log inside the project, or one on Windows, is refused: %q", tlsKeyLogDoc)
	}
}

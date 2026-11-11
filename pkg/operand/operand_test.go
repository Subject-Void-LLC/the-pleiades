// Tests of the operand rule, and a fuzz target holding it to its contract:
// it never panics, it refuses exactly the words a program would read as an
// option or a pattern, and its message never carries the value.
package operand_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// TestProblem covers the names every method takes, the two kinds of word
// refused, and the edges of each: a dash inside or at the end of a name is
// a name, and so are the characters real package names carry.
func TestProblem(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       string // a substring of the problem; "" means none
	}{
		{"a package", "curl", ""},
		{"a package with a version character set", "libstdc++6", ""},
		{"g++, which ends in a plus", "g++", ""},
		{"an architecture qualifier", "libc6:amd64", ""},
		{"a unit with its suffix", "nginx.service", ""},
		{"a template instance", "getty@tty1.service", ""},
		{"an escaped unit name", `dev-disk-by\x2duuid-1.device`, ""},
		{"a Windows service", "Spooler", ""},
		{"a dash inside", "openssh-server", ""},
		{"a dash at the end is the method's own concern", "diffutils-", ""},
		{"empty is the required check's concern", "", ""},
		{"a short option", "-f", `begins with "-"`},
		{"an apt configuration option", "-oDPkg::Pre-Invoke::=touch /tmp/pwned", `begins with "-"`},
		{"a long option", "--autoremove", `begins with "-"`},
		{"a lone dash", "-", `begins with "-"`},
		{"the end of options", "--", `begins with "-"`},
		{"a star", "lib*", "pattern"},
		{"a question mark", "ssh?", "pattern"},
		{"a bracket", "ss[hd]", "pattern"},
		{"a closing bracket alone", "a]", "pattern"},
		{"a star anywhere", "nginx*.service", "pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := operand.Problem(tc.text)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("Problem(%q) = %q, want none", tc.text, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("Problem(%q) = %q, want it to say %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestCheck names the parameter in the error and returns nil for a name.
func TestCheck(t *testing.T) {
	if err := operand.Check("name", "curl"); err != nil {
		t.Fatalf("Check(curl) = %v", err)
	}
	err := operand.Check("name", "--autoremove")
	if err == nil || !strings.HasPrefix(err.Error(), "name is refused: ") {
		t.Fatalf("Check(--autoremove) = %v, want it to name the parameter", err)
	}
	if strings.Contains(err.Error(), "autoremove") {
		t.Errorf("the error carries the value: %v", err)
	}
}

// FuzzProblem holds Problem to its contract over any text: no panic, a
// refusal exactly when the text begins with a dash or holds a pattern
// character, and a message that is one of the two fixed sentences, so it
// can never carry any part of the value.
func FuzzProblem(f *testing.F) {
	for _, seed := range []string{
		"curl", "g++", "nginx.service", "", "-", "--", "-f", "--autoremove",
		"-oDPkg::Pre-Invoke::=touch /tmp/pwned", "--reference=/etc/shadow",
		"lib*", "s*", "ss[hd]", "a?b", "diffutils-", "\x00", "-\n", " -x",
	} {
		f.Add(seed)
	}
	option, pattern := operand.Problem("-"), operand.Problem("*")
	f.Fuzz(func(t *testing.T, text string) {
		got := operand.Problem(text)
		want := strings.HasPrefix(text, "-") || strings.ContainsAny(text, "*?[]")
		if (got != "") != want {
			t.Fatalf("Problem(%q) = %q, want a refusal %v", text, got, want)
		}
		if got != "" && got != option && got != pattern {
			t.Fatalf("Problem(%q) = %q, which is neither fixed sentence", text, got)
		}
	})
}

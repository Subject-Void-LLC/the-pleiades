// Tests of the line encoding. That every login shell hands sh the same
// command is proven against real shells by the release gates run with
// PLEIADES_TEST_LOGIN_SHELL (internal/testsupport) and by
// internal/transport/ssh's TestEveryLoginShellHandsShTheSameCommand.
package shline

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFor_KeepsNothingAShellReadsInsideItsArgument(t *testing.T) {
	command := "x='it'\\''s' \"$HOME\" `id` \\\\ ! \n\t{ a; } 2>/dev/null | cat; printf '%s\\n' \x01\xff"
	line := For(command)
	body := strings.TrimSuffix(strings.TrimPrefix(line, shPrefix), shSuffix)
	for _, c := range []string{"'", "\"", "$", "`", "!", "\n", "\t", "\\\\", "\\'", "{", ";", "|", ">", "&"} {
		if strings.Contains(body, c) {
			t.Errorf("the encoded argument holds %q: %q", c, body)
		}
	}
	if got, ok := From(line); !ok || got != command {
		t.Fatalf("From(For(x)) = %q, %v", got, ok)
	}
}

func TestFrom_RefusesALineForDidNotMake(t *testing.T) {
	for _, line := range []string{"", "ls -l", shPrefix, shPrefix + "abc", shPrefix + `\0` + shSuffix, shPrefix + `\1101` + shSuffix,
		shPrefix + `\0181` + shSuffix, shPrefix + `\0777` + shSuffix, "x" + shPrefix + shSuffix} {
		if got, ok := From(line); ok {
			t.Errorf("From(%q) = %q, true", line, got)
		}
	}
}

// TestFor_ShRunsTheCommandAsWritten runs the line through this machine's sh,
// as sshd runs it through a login shell that is sh.
func TestFor_ShRunsTheCommandAsWritten(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh here")
	}
	script := "set -e\nx='it'\\''s \"q\" $HOME `b` \\\\ !'\nif [ -n \"$x\" ]; then printf '%s\\n' \"$x\"; fi\nread line; echo \"in:$line\""
	cmd := exec.Command(sh, "-c", For(script))
	cmd.Stdin = strings.NewReader("data\n")
	cmd.Env = []string{"HOME=/h", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if want := "it's \"q\" $HOME `b` \\\\ !\nin:data\n"; err != nil || string(out) != want {
		t.Fatalf("got %q (%v), want %q", out, err, want)
	}
}

func FuzzFor(f *testing.F) {
	for _, s := range []string{"", "a", "'\\'\"$\n", "\x00\xff", strings.Repeat("\\0101", 3)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, command string) {
		line := For(command)
		got, ok := From(line)
		if !ok || got != command {
			t.Fatalf("From(For(%q)) = %q, %v", command, got, ok)
		}
		body := strings.TrimSuffix(strings.TrimPrefix(line, shPrefix), shSuffix)
		if strings.ContainsAny(body, "'\"$`!\n\r") {
			t.Fatalf("the encoded argument of %q holds a byte a shell reads: %q", command, body)
		}
	})
}

// TestFor_EveryByteReachesShAsItself sends each byte from 1 to 255 inside a
// quoted printf argument through this machine's sh, as sshd runs a line
// through a login shell that is sh, and reads it back unchanged: run on Linux,
// macOS and a FreeBSD lab machine alike, each measures its own sh's printf.
func TestFor_EveryByteReachesShAsItself(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh here")
	}
	var all strings.Builder
	for b := 1; b < 256; b++ {
		if b != '\'' {
			all.WriteByte(byte(b))
		}
	}
	want := all.String()
	cmd := exec.Command(sh, "-c", For("printf '%s' '"+want+"'; printf '%s' \"'\""))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	out, err := cmd.Output()
	if err != nil || string(out) != want+"'" {
		t.Fatalf("got %q (%v)", out, err)
	}
}

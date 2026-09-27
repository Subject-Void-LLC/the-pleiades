// Tests for add-credential --password-stdin through the real binary: the
// password arrives on a pipe, never on the command line, and is the
// password the vault then holds.
package main_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

func TestCLI_AddCredentialPasswordFromStdin(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// A Windows-written line ends in a carriage return, which is not part
	// of the password.
	const secret = "piped-Passw0rd with spaces"
	out, err := runPleiadesWithStdin(t, dir, secret+"\r\n", "add-credential", "win01", "--username", "Administrator", "--password-stdin")
	if err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the output shows the password:\n%s", out)
	}
	stored, err := credential.NewLazyFileStore(dir).Lookup(context.Background(), "win01")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Username != "Administrator" || stored.Password != secret {
		t.Errorf("stored %s, want Administrator with the piped password", stored)
	}

	for name, c := range map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"both password forms": {secret, []string{"--username", "u", "--password", "x", "--password-stdin"}, "mutually exclusive"},
		"with a key":          {secret, []string{"--username", "u", "--password-stdin", "--key", "/dev/null"}, "mutually exclusive"},
		"with generate":       {secret, []string{"--username", "u", "--password-stdin", "--generate"}, "cannot be combined"},
		"nothing piped":       {"", []string{"--username", "u", "--password-stdin"}, "no secret on standard input"},
		"an empty line":       {"\n", []string{"--username", "u", "--password-stdin"}, "empty secret"},
	} {
		out, err := runPleiadesWithStdin(t, dir, c.stdin, append([]string{"add-credential", "win02"}, c.args...)...)
		if err == nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q:\n%s", name, err, c.want, out)
		}
	}
	if _, err := credential.NewLazyFileStore(dir).Lookup(context.Background(), "win02"); err == nil {
		t.Error("a refused add-credential stored a credential")
	}
}

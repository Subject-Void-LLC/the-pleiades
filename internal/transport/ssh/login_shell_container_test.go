// Package ssh: the ssh_exec transport against a real sshd whose account logs
// in to each of ash, bash, zsh, tcsh and fish.
package ssh

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// loginShellScript is POSIX shell using what the login shells read
// differently: quotes closed around an escaped one, a backslash pair, a
// dollar sign, backquotes and an exclamation mark in quotes, if/for, braces,
// $(...), ${x}, arithmetic, 2>/dev/null, a here-document and a newline in a
// quoted string.
const loginShellScript = `set -e
x='it'\''s a "test" with $HOME, ` + "`backquotes`" + `, \\ and !'
if [ -n "$x" ]; then printf '%s\n' "$x"; fi
for i in 1 2; do echo "n=$i"; done
{ echo braces; } | cut -c1-6
v=$(echo sub); echo "$v ${v}x $((1+2))"
ls /nonexistent 2>/dev/null || echo "no such dir"
echo 'two
lines'
cat <<EOF
heredoc $v
EOF`

// TestEveryLoginShellHandsShTheSameCommand runs one POSIX script through the
// real ssh_exec transport and a real sshd whose account's login shell is, in
// turn, ash, bash, zsh, tcsh and fish, and holds every answer to the first:
// sh reads the command whichever shell sshd hands it to (pkg/remoteexec/shline).
// The same script sent as written (Target.LoginShell, for a device whose exec
// channel is not a POSIX shell) does not survive fish, which is what every
// command met before.
func TestEveryLoginShellHandsShTheSameCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container")
	}
	ctx := context.Background()
	srv, err := testsupport.StartSSHD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Terminate(context.Background()) })
	target := transport.Target{Endpoint: transport.NetworkEndpoint{Host: srv.Host, Port: srv.Port}}
	cred := credential.Credential{Username: testsupport.SSHDUser, Password: testsupport.SSHDPassword}
	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 10 * time.Second})

	want := ""
	for _, shell := range []string{"ash", "bash", "zsh", "tcsh", "fish"} {
		if err := testsupport.UseLoginShell(ctx, srv.Container, shell, testsupport.SSHDUser); err != nil {
			t.Fatal(err)
		}
		res, err := tr.Exec(ctx, target, cred, loginShellScript)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%s: exit %d, %v: %s", shell, res.ExitCode, err, res.Stderr)
		}
		if want == "" {
			want = res.Stdout
			if want == "" {
				t.Fatal("the script printed nothing")
			}
		} else if res.Stdout != want {
			t.Errorf("under a %s login:\n%s\nunder ash:\n%s", shell, res.Stdout, want)
		}
	}

	raw := target
	raw.LoginShell = true
	res, err := tr.Exec(ctx, raw, cred, loginShellScript)
	if err == nil && res.ExitCode == 0 && res.Stdout == want {
		t.Error("the script sent as written survived a fish login, so this test could not tell sh from the login shell")
	}
}

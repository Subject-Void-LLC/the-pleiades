// Tests that a project synced over SSH with an RSA key signs the git server's
// login with SHA-2 and never with ssh-rsa (SHA-1), which nothing allows for a
// git server, through go-git's real SSH transport against an in-process SSH
// server that runs the real git-upload-pack for each request. The server
// reports the signature it verified, so what was signed is read from its side.
package project_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestGitSyncer_AnRSAKeyNeverSignsSHA1 proves an RSA project key syncs from a
// git server that advertises SHA-2, signing with rsa-sha2-512, and is refused,
// with no key login made, by one that takes the key only with ssh-rsa: the
// refusal names no property, since none exists for a git server.
func TestGitSyncer_AnRSAKeyNeverSignsSHA1(t *testing.T) {
	need.POSIXShell(t)
	if _, err := exec.LookPath("git-upload-pack"); err != nil {
		t.Skipf("needs git-upload-pack on PATH, which the test's SSH server runs to serve the repository: %v", err)
	}
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	keyPEM, pub, err := remoteexectest.RSAUserKey()
	if err != nil {
		t.Fatal(err)
	}

	for name, algorithms := range map[string][]string{
		"a server advertising SHA-2":                nil,
		"a server taking the key only with ssh-rsa": {ssh.KeyAlgoRSA},
	} {
		t.Run(name, func(t *testing.T) {
			srv, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: pub, PublicKeyAuthAlgorithms: algorithms, RefusePasswords: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			known := filepath.Join(t.TempDir(), "known_hosts")
			if err := os.WriteFile(known, []byte(knownhosts.Line([]string{srv.Addr()}, srv.HostKey)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// go-git verifies the server's host key against this file.
			t.Setenv("SSH_KNOWN_HOSTS", known)

			auth := &stubAuth{auth: project.Auth{Username: srv.Username, PrivateKey: keyPEM}}
			syncer := project.NewGitSyncer(t.TempDir(), auth, project.SourcePolicy{})
			p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: "ssh://" + srv.Username + "@" + srv.Addr() + origin, CredentialID: 1}
			got, err := syncer.Sync(t.Context(), p, nil)
			if err != nil {
				t.Fatalf("Sync() = %v, want the outcome in the Result", err)
			}

			if algorithms == nil {
				if got.Status != project.SyncSucceeded {
					t.Fatalf("Sync() = %+v, want a clone over SSH", got)
				}
				signed := srv.KeySignatures()
				if len(signed) == 0 || slices.ContainsFunc(signed, func(a string) bool { return a != ssh.KeyAlgoRSASHA512 }) {
					t.Errorf("the git server verified %v, want only rsa-sha2-512", signed)
				}
				return
			}
			if got.Status != project.SyncFailed || !strings.Contains(got.Err, "did not advertise a SHA-2 signature for this RSA key") || !strings.Contains(got.Err, "no device record governs this login") {
				t.Fatalf("Sync() = %+v, want a refusal of the ssh-rsa signature naming no property", got)
			}
			t.Logf("the refusal a project's page shows: %s", got.Err)
			if strings.Contains(got.Err, "ssh_allow_legacy_user_key_signatures") {
				t.Errorf("the refusal names a device property no git server has: %s", got.Err)
			}
			if srv.KeyLogins() != 0 {
				t.Errorf("the git server verified %d key logins, want none", srv.KeyLogins())
			}
		})
	}
}

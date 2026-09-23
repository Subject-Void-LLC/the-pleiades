// Tests for reading a mesh credential from the path an operator names.
//
// The branch that matters most is the empty one: an unset variable has to
// mean "dial exactly as before", because that is what lets a deployment
// turn authentication on without every existing deployment changing at
// the same moment.
package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func TestCredentialsFromEnv(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "runner.creds")
	body := mintTestCredential(t)
	if err := os.WriteFile(good, body, 0o400); err != nil {
		t.Fatalf("writing a credential: %v", err)
	}

	truncated := filepath.Join(dir, "truncated.creds")
	if err := os.WriteFile(truncated, []byte("-----BEGIN NATS USER JWT-----\ntoken.x\n------END NATS USER JWT------\n"), 0o400); err != nil {
		t.Fatalf("writing a truncated credential: %v", err)
	}

	empty := filepath.Join(dir, "empty.creds")
	if err := os.WriteFile(empty, nil, 0o400); err != nil {
		t.Fatalf("writing an empty credential: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantNil bool
		wantErr string
	}{
		{
			// The load-bearing case. Unset means no option at all, which
			// is byte-identically the unauthenticated dial every
			// deployment does today.
			name:    "unset means no credential and no error",
			path:    "",
			wantNil: true,
		},
		{
			name: "a well formed credential is returned",
			path: good,
		},
		{
			name:    "a missing file names the variable and the path",
			path:    filepath.Join(dir, "absent.creds"),
			wantErr: "NATS_CREDS_FILE",
		},
		{
			// A directory here is a mounted-the-wrong-thing mistake,
			// which is common with Kubernetes secrets: mounting a Secret
			// gives you a directory of keys, not a file.
			name:    "a directory is refused rather than read",
			path:    dir,
			wantErr: "not a regular file",
		},
		{
			// The half-a-credential case, which is the likeliest way to
			// corrupt one: a copy that lost its tail, or a JWT pasted
			// without its key.
			name:    "a jwt with no seed is refused at startup",
			path:    truncated,
			wantErr: "user key",
		},
		{
			name:    "an empty file is refused",
			path:    empty,
			wantErr: "user key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := topology.CredentialsFromEnv(tt.path)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CredentialsFromEnv(%q) returned no error, want one naming %q", tt.path, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to name %q", err, tt.wantErr)
				}
				if got != nil {
					t.Error("an error was returned alongside a credential")
				}
				return
			}

			if err != nil {
				t.Fatalf("CredentialsFromEnv(%q) = %v, want no error", tt.path, err)
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("got %d bytes, want nil so the caller adds no dial option at all", len(got))
				}
				return
			}
			if string(got) != string(body) {
				t.Error("the credential was altered on the way through")
			}
		})
	}
}

// TestCredentialsFromEnvNeverQuotesTheCredential is the rule that holds
// however this function is changed.
//
// Every error here names a file an operator chose, and several of them
// have just read that file's bytes. A message that quotes them puts a
// private seed into a startup log, which is the one place it must never
// reach, and a log is exactly what somebody pastes into an issue.
func TestCredentialsFromEnvNeverQuotesTheCredential(t *testing.T) {
	dir := t.TempDir()
	body := mintTestCredential(t)

	// The seed's own characters are corrupted rather than its armor,
	// because the armor turns out not to be load bearing: nkeys locates a
	// seed by its shape, so misspelling the BEGIN line leaves a perfectly
	// readable credential. Breaking the base32 body is what actually
	// produces the error path, and it leaves the JWT half intact, which
	// is the material this test then looks for in the message.
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "SU") {
			lines[i] = line[:len(line)-4] + "AAAA"
		}
	}
	mangled := []byte(strings.Join(lines, "\n"))

	path := filepath.Join(dir, "mangled.creds")
	if err := os.WriteFile(path, mangled, 0o400); err != nil {
		t.Fatalf("writing: %v", err)
	}

	_, err := topology.CredentialsFromEnv(path)
	if err == nil {
		t.Fatal("a credential with no readable seed was accepted")
	}

	// The seed itself, which is the line that must not appear.
	for _, line := range strings.Split(string(body), "\n") {
		if len(line) < 40 || strings.HasPrefix(line, "-----") {
			continue
		}
		if strings.Contains(err.Error(), line) {
			t.Fatalf("the error quotes credential material back: %v", err)
		}
	}
}

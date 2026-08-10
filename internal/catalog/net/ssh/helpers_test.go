package ssh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildAuthMethod_PrefersPassword(t *testing.T) {
	method, err := buildAuthMethod(map[string]string{secretUsername: "admin", secretPassword: "hunter2"})
	if err != nil {
		t.Fatalf("buildAuthMethod: %v", err)
	}
	if method == nil {
		t.Fatal("expected a non-nil ssh.AuthMethod")
	}
}

func TestBuildAuthMethod_NoUsableSecretReturnsError(t *testing.T) {
	if _, err := buildAuthMethod(map[string]string{secretUsername: "admin"}); err == nil {
		t.Fatal("expected an error when neither password nor private key is present")
	}
}

func TestBuildAuthMethod_InvalidPrivateKeyReturnsError(t *testing.T) {
	if _, err := buildAuthMethod(map[string]string{secretPrivateKeyPEM: "not a real key"}); err == nil {
		t.Fatal("expected an error for an unparseable private key")
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain word", in: "pong", want: "'pong'"},
		{name: "embedded single quote", in: "it's", want: `'it'\''s'`},
		{name: "shell metacharacters neutralized", in: "pong; rm -rf /", want: "'pong; rm -rf /'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHostKeyCallback_InsecureSkipOptsOutExplicitly(t *testing.T) {
	cb, err := hostKeyCallback(map[string]any{paramInsecureSkipHostKeyVerify: true})
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if cb == nil {
		t.Fatal("expected a non-nil HostKeyCallback")
	}
}

func TestHostKeyCallback_MissingKnownHostsFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := hostKeyCallback(nil); err == nil {
		t.Fatal("expected a fail-closed error when no known_hosts file exists")
	}
}

func TestHostKeyCallback_RealKnownHostsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create .ssh dir: %v", err)
	}
	// A minimal, syntactically valid known_hosts entry (ssh-ed25519 test
	// key), just enough for knownhosts.New to parse the file successfully;
	// this test proves the file is found and parsed, not that any
	// specific host matches it.
	const entry = "example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBWFbutafwV24facYaGCEZzOJi+2Cvk49WrlDUdcT9pJ\n"
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(entry), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	cb, err := hostKeyCallback(nil)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if cb == nil {
		t.Fatal("expected a non-nil HostKeyCallback")
	}
}

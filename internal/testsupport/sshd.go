// Starting a real OpenSSH server for a test, in one place.
//
// # Why this exists beside StartNATS rather than as its model
//
// StartNATS starts one broker per test and ties its lifetime to tb. The
// packages that need a real sshd share ONE container across every test
// in the package instead, because startup is several seconds and nothing
// they do needs a pristine server (internal/transport/ssh's
// requireSSHContainer set that precedent). So StartSSHD takes a context
// and returns an error, for a caller that starts it inside a sync.Once
// and terminates it from TestMain, which a testing.TB-bound starter
// cannot serve.
//
// It exists at all so the file-transfer adapters (pkg/sftpxfer and
// pkg/scpxfer) run their release gates against the same server, started
// the same way, rather than two private copies of the same thirty lines
// drifting apart: the lesson internal/testsupport was created for.
package testsupport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

// The shared sshd container's fixed test account. This is a throwaway
// container started fresh for each test run, never a real device, so a
// literal password here is not a secret leak (the same reasoning
// internal/transport/ssh's container tests record for theirs).
const (
	// SSHDUser is the account the container's sshd accepts.
	SSHDUser = "testuser"
	// SSHDPassword is SSHDUser's password.
	SSHDPassword = "testpass123"
	// SSHDInnerPort is the port sshd listens on inside the container,
	// which is also what a hop through the container reaches.
	SSHDInnerPort = 2222
	// SSHDHome is SSHDUser's home directory in this image.
	SSHDHome = "/config"
)

// SSHD is a running OpenSSH server and the ways a test reaches it.
type SSHD struct {
	// Container is the running container.
	Container testcontainers.Container
	// Host and Port are the address a test dials from outside.
	Host string
	Port int
}

// StartSSHD starts the pinned SSHDImage with password authentication
// for SSHDUser and TCP forwarding enabled, so the container can also
// serve as its own bastion for a one-hop chain. The caller must call
// Terminate.
//
// The image serves the sftp subsystem (Subsystem sftp internal-sftp in
// its generated sshd_config) and ships /usr/bin/scp, both confirmed
// against the pinned image before any file-transfer code was written,
// per LESSONS_LEARNED #171: a fixture missing the subsystem under test
// proves nothing about it.
func StartSSHD(ctx context.Context) (*SSHD, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        SSHDImage,
			ExposedPorts: []string{fmt.Sprintf("%d/tcp", SSHDInnerPort)},
			Env: map[string]string{
				"PUID":            "1000",
				"PGID":            "1000",
				"PASSWORD_ACCESS": "true",
				"USER_NAME":       SSHDUser,
				"USER_PASSWORD":   SSHDPassword,
			},
			// The image's init script adds an Include for this directory
			// only when it exists, and sshd takes the first occurrence of
			// a keyword, so this "yes" wins over the later default "no".
			// internal/transport/ssh's requireSSHContainer records how
			// that was confirmed.
			Files: []testcontainers.ContainerFile{{
				Reader:            strings.NewReader("AllowTcpForwarding yes\n"),
				ContainerFilePath: "/config/sshd/sshd_config.d/allow-tcp-forwarding.conf",
				FileMode:          0o644,
			}},
			// The image's own final startup line, logged once sshd is
			// listening inside the container, then its banner through the
			// mapped port, which is the path every caller dials.
			WaitingFor: wait.ForAll(
				wait.ForLog("done.").WithStartupTimeout(SSHDStartupTimeout),
				SSHGreeting(fmt.Sprintf("%d/tcp", SSHDInnerPort)),
			),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return nil, fmt.Errorf("starting the sshd container: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.Background())
		return nil, fmt.Errorf("sshd container host: %w", err)
	}
	mapped, err := c.MappedPort(ctx, fmt.Sprintf("%d/tcp", SSHDInnerPort))
	if err != nil {
		_ = c.Terminate(context.Background())
		return nil, fmt.Errorf("sshd container port: %w", err)
	}
	return &SSHD{Container: c, Host: host, Port: int(mapped.Num())}, nil
}

// Terminate stops and removes the container.
func (s *SSHD) Terminate(ctx context.Context) error {
	return s.Container.Terminate(ctx)
}

// RootExec runs script through /bin/sh inside the container as root,
// bypassing sshd entirely, and returns its combined output. It is how a
// test lays out a trap (a symlink, a FIFO, a file another account owns)
// and inspects the aftermath through a path independent of the code
// under test. A non-zero exit is an error carrying the output.
func (s *SSHD) RootExec(ctx context.Context, script string) (string, error) {
	code, reader, err := s.Container.Exec(ctx, []string{"/bin/sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("exec in the sshd container: %w", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("reading exec output: %w", err)
	}
	if code != 0 {
		return string(out), fmt.Errorf("exec %q exited %d: %s", script, code, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// KnownHosts writes a known_hosts file into dir naming every one of the
// container's real host keys under every address a test dials it by:
// the mapped address from outside, and 127.0.0.1 on the inner port,
// which is what a hop through the container presents as the target's
// name. It returns the file's path, for remoteexec.Options.KnownHostsPath,
// so a release gate verifies host keys for real rather than skipping the
// check.
//
// Every key type is listed, not only ed25519, because the client and
// server negotiate which host key algorithm is used, and a known_hosts
// file naming a host under one key type is a MISMATCH (not an unknown
// host) when the server presents another. The first version of this
// listed only ed25519 and every connection was refused, fail closed,
// exactly as the verification is meant to behave.
func (s *SSHD) KnownHosts(ctx context.Context, dir string) (string, error) {
	pub, err := s.RootExec(ctx, "cat /config/ssh_host_keys/ssh_host_*_key.pub")
	if err != nil {
		return "", err
	}
	var keys []string
	for line := range strings.SplitSeq(strings.TrimSpace(pub), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", fmt.Errorf("unexpected host key file content %q", line)
		}
		keys = append(keys, fields[0]+" "+fields[1])
	}

	var lines strings.Builder
	for _, key := range keys {
		for _, host := range []string{s.Host, "127.0.0.1", "localhost"} {
			fmt.Fprintf(&lines, "[%s]:%d %s\n", host, s.Port, key)
		}
		fmt.Fprintf(&lines, "[127.0.0.1]:%d %s\n", SSHDInnerPort, key)
	}

	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		return "", fmt.Errorf("writing known_hosts: %w", err)
	}
	return path, nil
}

// InstallClientKey generates a throwaway ed25519 key pair in dir,
// authorizes its public half for SSHDUser inside the container, and
// returns the private key's path. It exists so a benchmark can drive
// the OpenSSH command-line clients (scp, sftp) against the same server
// as the code under test, for an honest comparison: those clients
// cannot be given a password non-interactively.
func (s *SSHD) InstallClientKey(ctx context.Context, dir string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating a client key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "pleiades benchmark")
	if err != nil {
		return "", fmt.Errorf("encoding the client key: %w", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", fmt.Errorf("writing the client key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return "", fmt.Errorf("deriving the public key: %w", err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	script := fmt.Sprintf("mkdir -p %[1]s/.ssh && printf '%%s\\n' '%[2]s' >> %[1]s/.ssh/authorized_keys && "+
		"chown -R 1000:1000 %[1]s/.ssh && chmod 700 %[1]s/.ssh && chmod 600 %[1]s/.ssh/authorized_keys",
		SSHDHome, authorized)
	if _, err := s.RootExec(ctx, script); err != nil {
		return "", err
	}
	return keyPath, nil
}

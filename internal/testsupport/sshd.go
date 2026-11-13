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
	"time"

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
	return StartSSHDOn(ctx)
}

// StartSSHDOn is StartSSHD with the container also joined to networks, so
// it can serve as a bastion to a test container reachable only there (a
// Kubernetes API server known by its alias on that network).
func StartSSHDOn(ctx context.Context, networks ...string) (*SSHD, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        SSHDImage,
			Networks:     networks,
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
	if err := SetLoginShell(ctx, c, SSHDUser); err != nil {
		_ = c.Terminate(context.Background())
		return nil, err
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

// EnvLoginShell names the login shell the sshd containers give their test
// account (zsh, tcsh, fish, bash), so a test reaching a device over SSH can be
// run again under a shell that is not the image's POSIX one: the Alpine sshd
// containers (StartSSHD and the release gates' own), the Docker and Podman
// engine hosts, and cmd/pleiades' Debian devices take it. An Ansible gate's
// container does not, Ansible choosing its own shell. Unset,
// the image's own shell stays. A method that sends its command for the login
// shell to parse, rather than for sh, fails under one of them.
const EnvLoginShell = "PLEIADES_TEST_LOGIN_SHELL"

// loginShellTimeout bounds installing a login shell and switching to it.
const loginShellTimeout = 3 * time.Minute

// SetLoginShell makes the shell EnvLoginShell names each of users' login
// shell in c, as root and before any test connects (UseLoginShell). It does
// nothing when the variable is unset.
func SetLoginShell(ctx context.Context, c testcontainers.Container, users ...string) error {
	shell := os.Getenv(EnvLoginShell)
	if shell == "" {
		return nil
	}
	return UseLoginShell(ctx, c, shell, users...)
}

// UseLoginShell installs shell (zsh, tcsh, fish, bash, or ash, Alpine's own)
// in c through whichever of apk, apt-get and dnf the image has, and makes it
// each of users' login shell. sshd reads it at each login, so a connection
// opened after this call gets it.
func UseLoginShell(ctx context.Context, c testcontainers.Container, shell string, users ...string) error {
	if shell == "" || strings.Trim(shell, "abcdefghijklmnopqrstuvwxyz") != "" {
		return fmt.Errorf("%q is not a shell's name", shell)
	}
	// Installers write warnings (debconf, a mirror's notice) even when quiet,
	// so each account's shell is read back on a line of its own, marked.
	script := "command -v " + shell + " >/dev/null || " +
		"if command -v apk >/dev/null; then apk add -q " + shell + "; " +
		"elif command -v apt-get >/dev/null; then apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq " + shell + "; " +
		// fish is in EPEL, not a RHEL-family system's own repositories.
		"elif command -v dnf >/dev/null; then dnf install -y -q " + shell + " || { dnf install -y -q epel-release && dnf install -y -q " + shell + "; }; " +
		"fi >/dev/null 2>&1; path=$(command -v " + shell + ") || { echo 'no " + shell + "'; exit 1; }"
	for _, user := range users {
		script += "; sed -i -E \"s#^(" + user + ":([^:]*:){5})[^:]*\\$#\\1$path#\" /etc/passwd && " +
			"printf 'shell:%s\\n' \"$(getent passwd " + user + " | cut -d: -f7)\""
	}
	// Bounded: installing the shell reaches the package mirror, and a stalled
	// one must fail the test, not hold it until the package's timeout.
	ctx, cancel := context.WithTimeout(ctx, loginShellTimeout)
	defer cancel()
	code, reader, err := c.Exec(ctx, []string{"/bin/sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		return fmt.Errorf("setting the login shell to %s: %w", shell, err)
	}
	out, _ := io.ReadAll(reader)
	var got []string
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "shell:"); ok {
			got = append(got, v)
		}
	}
	if code != 0 || len(got) != len(users) {
		return fmt.Errorf("setting the login shell to %s: exit %d, %d of %d accounts: %s", shell, code, len(got), len(users), out)
	}
	for _, path := range got {
		if !strings.HasSuffix(path, "/"+shell) {
			return fmt.Errorf("setting the login shell to %s: an account's is %s", shell, path)
		}
	}
	return nil
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
// Every key type is listed, not only ed25519. The first version of this
// listed only ed25519 and every connection was refused as a mismatch,
// because the client then offered Go's default order and the server
// answered with its ECDSA key. pkg/remoteexec now offers the types
// known_hosts holds for the host first (hostkeyalgos.go), so one type
// would do; TestSSHContainer_APinnedHostKeyOfAnyOneTypeVerifies proves
// that, and every type stays here so a fixture never depends on it.
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

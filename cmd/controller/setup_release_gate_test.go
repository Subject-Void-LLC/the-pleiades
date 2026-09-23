//go:build linux

// The setup command's release gate at the binary level: the built controller,
// real SQLite databases, and real pseudo terminals.
package main_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/activityentry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/encryptionkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// These tests run the built controller binary's setup command the way an
// operator runs it, with no test seam between them and the code: a real
// process, a real SQLite database, and for the interactive paths a real
// pseudo terminal. They are the setup command's release gate at the binary
// level; the compose and Helm halves are in tests/e2e.

const setupTestPassword = "a-long-enough-admin-password"

// readKeyFile returns the key and JWT secret setup wrote in dir.
func readKeyFile(t *testing.T, dir string) (key []byte, jwt string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	f, err := setup.ParseEnvFile(data)
	if err != nil {
		t.Fatalf("parsing .env: %v", err)
	}
	raw, _ := f.Get(setup.VarMasterKey)
	key, err = crypto.DecodeKey(raw, ".env")
	if err != nil {
		t.Fatalf("the written key does not decode: %v", err)
	}
	jwt, _ = f.Get(setup.VarJWTSecret)
	return key, jwt
}

// openUnder opens the database as the controller would, under key.
func openUnder(t *testing.T, dbPath string, key []byte) *ent.Client {
	t.Helper()
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: "sqlite://" + dbPath})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	return client
}

// assertKeyRecorded checks the registry and the activity trail name the key.
func assertKeyRecorded(t *testing.T, client *ent.Client, key []byte, possession encryptionkey.Possession) {
	t.Helper()
	ctx := context.Background()
	row, err := client.EncryptionKey.Query().Where(encryptionkey.FingerprintEQ(crypto.Fingerprint(key))).Only(ctx)
	if err != nil {
		t.Fatalf("the registry has no row for the key setup wrote: %v", err)
	}
	if row.Origin != encryptionkey.OriginSetup || row.Possession != possession {
		t.Fatalf("registry row = %+v, want origin setup and possession %s", row, possession)
	}
	entry, err := client.ActivityEntry.Query().Where(activityentry.ObjectKindEQ("encryption key")).Only(ctx)
	if err != nil {
		t.Fatalf("the activity trail has no encryption key entry: %v", err)
	}
	if entry.Actor != "controller-setup" || entry.ObjectID != row.ID || strings.Contains(entry.ObjectName, crypto.EncodeKey(key)) {
		t.Fatalf("activity entry = %+v", entry)
	}
}

// TestSetupReleaseGate_NonInteractiveReachesAWorkingSignIn runs setup the
// way automation and the compose gate do, then proves its three outcomes:
// the key file, the activity trail, and an administrator who can sign in.
// A second identical run must refuse and name the file.
func TestSetupReleaseGate_NonInteractiveReachesAWorkingSignIn(t *testing.T) {
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "controller.db")
	args := []string{"setup", "--target", "compose", "--dir", dir, "--non-interactive",
		"--admin-email", "admin@example.com", "--password-stdin"}

	cmd := exec.Command(binPath, args...)
	cmd.Env = setupEnv(dbPath)
	cmd.Stdin = strings.NewReader(setupTestPassword + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("setup failed: %v\n%s", err, out)
	}

	key, jwt := readKeyFile(t, dir)
	for name, secret := range map[string]string{"key": crypto.EncodeKey(key), "JWT secret": jwt, "password": setupTestPassword} {
		if strings.Contains(string(out), secret) {
			t.Fatalf("setup printed the %s:\n%s", name, out)
		}
	}

	client := openUnder(t, dbPath, key)
	assertKeyRecorded(t, client, key, encryptionkey.PossessionNotChecked)
	store := localauth.NewEntStore(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := store.Authenticate(context.Background(), "admin@example.com", setupTestPassword); err != nil {
		t.Fatalf("the administrator setup created cannot sign in: %v", err)
	}

	again := exec.Command(binPath, args...)
	again.Env = setupEnv(dbPath)
	again.Stdin = strings.NewReader(setupTestPassword + "\n")
	out, err = again.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("a re-run exited with %v, want 1:\n%s", err, out)
	}
	if !strings.Contains(string(out), filepath.Join(dir, ".env")) || !strings.Contains(string(out), "irreversible") {
		t.Fatalf("the re-run refusal does not name the file it would destroy:\n%s", out)
	}
}

// ptySession is a setup process on a pseudo terminal.
type ptySession struct {
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex
	buf    bytes.Buffer
	exited chan error
}

// startPTY starts the binary with args on a new pseudo terminal.
func startPTY(t *testing.T, dbPath string, args ...string) *ptySession {
	t.Helper()
	return startPTYWith(t, setupEnv(dbPath), args...)
}

// startPTYWith is startPTY with the child's environment given whole.
func startPTYWith(t *testing.T, env []string, args ...string) *ptySession {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	f, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pseudo terminal available here: %v", err)
	}
	s := &ptySession{cmd: cmd, pty: f, exited: make(chan error, 1)}
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := f.Read(chunk)
			s.mu.Lock()
			s.buf.Write(chunk[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { s.exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = f.Close() })
	return s
}

// shown returns everything the terminal has shown.
func (s *ptySession) shown() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitFor waits until the terminal shows want.
func (s *ptySession) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(s.shown(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it showed:\n%q", want, s.shown())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// echoOff reports whether the terminal currently has echo off.
func (s *ptySession) echoOff() bool {
	termios, err := unix.IoctlGetTermios(int(s.pty.Fd()), unix.TCGETS)
	return err == nil && termios.Lflag&unix.ECHO == 0
}

// typeLine types a line, waiting for echo to be off first when hidden.
func (s *ptySession) typeLine(t *testing.T, line string, hidden bool) {
	t.Helper()
	if hidden {
		deadline := time.Now().Add(10 * time.Second)
		for !s.echoOff() {
			if time.Now().After(deadline) {
				t.Fatal("echo was never turned off for a hidden answer")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if _, err := s.pty.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("typing: %v", err)
	}
}

// wait returns the process's exit code.
func (s *ptySession) wait(t *testing.T) int {
	t.Helper()
	select {
	case err := <-s.exited:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("waiting for setup: %v", err)
		}
		return 0
	case <-time.After(30 * time.Second):
		t.Fatalf("setup did not exit; the terminal showed:\n%q", s.shown())
		return -1
	}
}

// displayedKey is how the possession screen lays out the key.
var displayedKey = regexp.MustCompile(`\n {4}([A-Za-z0-9+/]{43}=)\r?\n`)

// TestSetupReleaseGate_InteractiveShowsTheKeyOnceAndTakesItBack drives the
// whole interactive path: the outage question, the key on the alternate
// screen, the possession check, and the administrator's hidden password.
func TestSetupReleaseGate_InteractiveShowsTheKeyOnceAndTakesItBack(t *testing.T) {
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "controller.db")
	s := startPTY(t, dbPath, "setup", "--dir", dir)

	s.waitFor(t, "longest link outage")
	s.typeLine(t, "20m", false)
	s.waitFor(t, "Press Enter when you have stored the key")
	match := displayedKey.FindStringSubmatch(s.shown())
	if match == nil {
		t.Fatalf("no key on the possession screen:\n%q", s.shown())
	}
	shownKey := match[1]
	s.typeLine(t, "", false)
	s.waitFor(t, "Type or paste the key you stored")
	s.typeLine(t, shownKey, true)
	s.waitFor(t, "matches, byte for byte")
	s.waitFor(t, "Email address for the first administrator")
	s.typeLine(t, "admin@example.com", false)
	s.waitFor(t, "New password for")
	s.typeLine(t, setupTestPassword, true)
	s.waitFor(t, "Repeat password")
	s.typeLine(t, setupTestPassword, true)
	if code := s.wait(t); code != 0 {
		t.Fatalf("setup exited %d:\n%s", code, s.shown())
	}

	key, _ := readKeyFile(t, dir)
	if crypto.EncodeKey(key) != shownKey {
		t.Fatal("the key written is not the key shown")
	}
	out := s.shown()
	enter, leave := strings.Index(out, "\x1b[?1049h"), strings.Index(out, "\x1b[?1049l")
	if strings.Count(out, shownKey) != 1 || strings.Index(out, shownKey) < enter || strings.Index(out, shownKey) > leave {
		t.Fatal("the key reached the terminal outside the alternate screen, or more than once")
	}
	if strings.Contains(out, setupTestPassword) {
		t.Fatal("the administrator's password was echoed")
	}
	assertKeyRecorded(t, openUnder(t, dbPath, key), key, encryptionkey.PossessionChecked)
}

// TestSetupReleaseGate_ReplacingAKeyTakesTheExactPhrase covers the named
// flag at a terminal: a wrong phrase changes nothing, and the exact phrase
// replaces a key that protects no data.
func TestSetupReleaseGate_ReplacingAKeyTakesTheExactPhrase(t *testing.T) {
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "controller.db")
	first := exec.Command(binPath, "setup", "--dir", dir, "--non-interactive")
	first.Env = setupEnv(dbPath)
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("first setup failed: %v\n%s", err, out)
	}
	oldKey, _ := readKeyFile(t, dir)
	before, _ := os.ReadFile(filepath.Join(dir, ".env"))

	wrong := startPTY(t, dbPath, "setup", "--dir", dir, "--destroy-existing-encryption-key")
	wrong.waitFor(t, "Type exactly")
	wrong.typeLine(t, "destroy 0000-0000", false)
	if code := wrong.wait(t); code != 1 {
		t.Fatalf("a wrong phrase exited %d, want 1", code)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".env")); !bytes.Equal(before, after) {
		t.Fatal("a wrong phrase changed the file")
	}

	right := startPTY(t, dbPath, "setup", "--dir", dir, "--destroy-existing-encryption-key")
	right.waitFor(t, "Type exactly")
	phrase := regexp.MustCompile(`Type exactly "(destroy [0-9a-f]{4}-[0-9a-f]{4})"`).FindStringSubmatch(right.shown())
	if phrase == nil {
		t.Fatalf("no phrase in the prompt:\n%q", right.shown())
	}
	right.typeLine(t, phrase[1], false)
	right.waitFor(t, "Press Enter when you have stored the key")
	shownKey := displayedKey.FindStringSubmatch(right.shown())[1]
	right.typeLine(t, "", false)
	right.typeLine(t, shownKey, true)
	right.waitFor(t, "Email address for the first administrator")
	right.typeLine(t, "", false)
	if code := right.wait(t); code != 0 {
		t.Fatalf("the replacement exited %d:\n%s", code, right.shown())
	}
	newKey, _ := readKeyFile(t, dir)
	if bytes.Equal(newKey, oldKey) {
		t.Fatal("the key was not replaced")
	}
}

// TestSetupReleaseGate_AnInterruptRestoresEchoAndWritesNothing interrupts
// setup while it is reading the key back with echo off, which is the moment
// an interrupt without the guard would leave the operator's shell silent.
func TestSetupReleaseGate_AnInterruptRestoresEchoAndWritesNothing(t *testing.T) {
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "controller.db")
	s := startPTY(t, dbPath, "setup", "--dir", dir, "--max-outage", "30m")

	s.waitFor(t, "Press Enter when you have stored the key")
	s.typeLine(t, "", false)
	s.waitFor(t, "Type or paste the key you stored")
	deadline := time.Now().Add(10 * time.Second)
	for !s.echoOff() {
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.pty.Write([]byte{0x03}); err != nil {
		t.Fatalf("sending Ctrl-C: %v", err)
	}
	if code := s.wait(t); code != 130 {
		t.Fatalf("an interrupted setup exited %d, want 130:\n%q", code, s.shown())
	}
	if s.echoOff() {
		t.Fatal("setup exited on an interrupt and left echo off")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatal("an interrupted setup wrote .env")
	}
	if !strings.Contains(s.shown(), "wrote nothing") {
		t.Fatalf("the interrupt message does not say nothing was written:\n%q", s.shown())
	}
}

// TestController_RefusesToStartWithoutAKeyAndNamesSetup proves the server's
// refusal points at the one remedy that ships in every image, names no
// repository make target, and fires before the database is touched.
func TestController_RefusesToStartWithoutAKeyAndNamesSetup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", "controller.db")
	cmd := exec.Command(binPath)
	// The server provisions its own certificate before it reaches the key
	// check, relative to its working directory, so both are pointed away from
	// the source tree, as every other test here that starts the server does.
	cmd.Dir = t.TempDir()
	cmd.Env = append(setupEnv(dbPath), "JWT_SECRET="+strings.Repeat("j", 40), "NATS_URL=nats://127.0.0.1:1",
		"PLEIADES_TLS_AUTOCERT_DIR="+filepath.Join(t.TempDir(), "tls"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the controller started with no key:\n%s", out)
	}
	if !strings.Contains(string(out), "controller setup") {
		t.Fatalf("the refusal does not name the setup command:\n%s", out)
	}
	if strings.Contains(string(out), "break-glass") || strings.Contains(string(out), "make ") {
		t.Fatalf("the refusal names a repository make target, which is not in the image:\n%s", out)
	}
	if _, err := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(err) {
		t.Fatal("the controller opened the database before refusing for want of a key")
	}
}

// TestSetupReleaseGate_CtrlCOnTheKeyScreenAsksFirst drives the case that
// ended a real `make up`: Ctrl+C pressed while the key is on the screen,
// which in most terminals is stop rather than copy. The first one must
// explain and keep the key there; a second one stops with the interrupt
// status, writes nothing, and leaves the terminal as it found it.
func TestSetupReleaseGate_CtrlCOnTheKeyScreenAsksFirst(t *testing.T) {
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "controller.db")
	s := startPTY(t, dbPath, "setup", "--dir", dir, "--max-outage", "30m")

	s.waitFor(t, "Press Enter when you have stored the key")
	deadline := time.Now().Add(10 * time.Second)
	for !s.echoOff() {
		if time.Now().After(deadline) {
			t.Fatal("the key screen never put the terminal in raw mode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.pty.Write([]byte{0x03}); err != nil {
		t.Fatalf("sending Ctrl+C: %v", err)
	}
	s.waitFor(t, "Nothing has stopped")
	if _, err := s.pty.Write([]byte{0x03}); err != nil {
		t.Fatalf("sending the second Ctrl+C: %v", err)
	}
	if code := s.wait(t); code != 130 {
		t.Fatalf("setup stopped with %d after two Ctrl+C, want 130:\n%q", code, s.shown())
	}
	if !strings.Contains(s.shown(), "setup was interrupted, and wrote nothing") {
		t.Fatalf("the stop does not say nothing was written:\n%q", s.shown())
	}
	if s.echoOff() {
		t.Fatal("setup stopped and left the terminal without echo")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatal("setup wrote .env after being stopped")
	}
}

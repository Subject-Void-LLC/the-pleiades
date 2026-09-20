//go:build unix

// Package loader's test helpers: building a directory of fake external
// Collection programs out of /bin/sh scripts, and the context and device a
// proxy is called with.
//
// A shell script is the right fixture for most of these tests because the
// point is misbehavior: a program that exits without answering, never
// exits, floods its output, or writes half a response. A program built on
// pkg/external never does any of that, which is why real_program_test.go
// covers the well-behaved half with one.
package loader

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// testPassword is the credential every proxy test hands the program. It is
// distinctive so an assertion that it never leaked cannot pass by
// accident against a short common string.
const testPassword = "loader-test-password-7c1f0a"

// testOptions are fast bounds for tests: every timeout and grace period is
// short enough that a hung fixture fails the test in well under a second
// of real waiting, instead of the minutes the production defaults allow.
func testOptions() Options {
	return Options{
		EngineVersion:   "dev",
		DescribeTimeout: 5 * time.Second,
		InvokeTimeout:   5 * time.Second,
		waitDelay:       200 * time.Millisecond,
		graceAfterExit:  200 * time.Millisecond,
		// Debug level, so the capture-and-log path runs in every test, into
		// a sink that keeps a passing run's output quiet.
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// programDir returns a fresh directory Load will accept: owned by this
// user and writable by nobody else.
func programDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	return dir
}

// implemented is an implemented manifest that passes every rule, with
// check support as asked.
func implemented(supportsCheck bool) collection.Manifest {
	return collection.Manifest{
		Status:        collection.StatusImplemented,
		Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing on any device"},
		SupportsCheck: supportsCheck,
	}
}

// describeJSON is the describe output for methods, at this build's
// protocol unless protocol is non-zero.
func describeJSON(t testing.TB, protocol int, methods ...external.DescribedMethod) string {
	t.Helper()
	if protocol == 0 {
		protocol = external.ProtocolVersion
	}
	data, err := json.Marshal(external.Description{Protocol: protocol, Methods: methods})
	if err != nil {
		t.Fatalf("encoding a description: %v", err)
	}
	return string(data)
}

// script is the text of a fake external Collection: describeOut printed
// verbatim for "describe", and invokeBody run for "invoke". The describe
// text travels in a quoted heredoc, so the shell expands nothing in it.
func script(describeOut, invokeBody string) string {
	return "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"describe)\ncat <<'PLEIADES_DESCRIBE_END'\n" + describeOut + "\nPLEIADES_DESCRIBE_END\n;;\n" +
		"invoke)\n" + invokeBody + "\n;;\n" +
		"*) exit 2 ;;\n" +
		"esac\n"
}

// writeProgram writes text as an executable program named name in dir,
// approves the build it wrote exactly as `pleiades collection approve`
// would (approveProgram), and returns its path. Tests about approval
// itself write with writeUnapproved instead.
func writeProgram(t testing.TB, dir, name, text string) string {
	t.Helper()
	path := writeUnapproved(t, dir, name, text)
	approveProgram(t, path)
	return path
}

// writeUnapproved writes text as an executable program named name in dir,
// approving nothing, and returns its path.
func writeUnapproved(t testing.TB, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatalf("writing %s: %v", path, err)
	}
	// WriteFile's mode is filtered by the umask; this fixes it exactly.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// approveProgram adds the current build of the program at path to its
// directory's approval list.
func approveProgram(t testing.TB, path string) {
	t.Helper()
	digest, err := inspectProgram(path)
	if err != nil {
		t.Fatalf("approving %s: %v", path, err)
	}
	if err := Approve(filepath.Dir(path), Approval{
		Program:    filepath.Base(path),
		Digest:     digest,
		ApprovedBy: "loader-test",
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("approving %s: %v", path, err)
	}
}

// oneMethodProgram writes a program describing the single method fqcn and
// running invokeBody for it, and returns the program's path.
func oneMethodProgram(t testing.TB, dir, fqcn string, supportsCheck bool, invokeBody string) string {
	t.Helper()
	out := describeJSON(t, 0, external.DescribedMethod{Name: fqcn, Manifest: implemented(supportsCheck)})
	return writeProgram(t, dir, strings.ReplaceAll(fqcn, ".", "_"), script(out, invokeBody))
}

// loadOne loads dir and returns the registered descriptor for fqcn, failing
// the test if either step fails. It restores the registry when the test
// ends.
func loadOne(t testing.TB, dir, fqcn string, opts Options) collection.Descriptor {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	if _, err := Load(t.Context(), dir, opts); err != nil {
		t.Fatalf("Load: %v", err)
	}
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s was not registered", fqcn)
	}
	return d
}

// recordingContext is the sdk.RunbookContext a proxy is called with: one
// fixed credential, and every stat it is handed kept for assertions.
type recordingContext struct {
	secrets map[string]string
	stats   map[string]any

	// refuse, when set, makes SetStat fail, to drive the branch where the
	// program answered and recording its answer did not work.
	refuse error
}

// newRecordingContext holds testPassword as the device's credential.
func newRecordingContext() *recordingContext {
	return &recordingContext{
		secrets: map[string]string{"username": "operator", "password": testPassword},
		stats:   map[string]any{},
	}
}

// InjectSecrets returns the credential.
func (c *recordingContext) InjectSecrets() map[string]string { return c.secrets }

// SetStat records a stat, or refuses when the test asked it to.
func (c *recordingContext) SetStat(key string, value any) error {
	if c.refuse != nil {
		return c.refuse
	}
	c.stats[key] = value
	return nil
}

// EmitFact is recorded the same way.
func (c *recordingContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// sshDevice is a device reachable over SSH, which is the only kind whose
// address a request carries.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

// SSHHost implements capability.SSHTransportCapable.
func (d *sshDevice) SSHHost() string { return d.host }

// SSHPort implements capability.SSHTransportCapable.
func (d *sshDevice) SSHPort() int { return d.port }

// newSSHDevice returns a device named web1 at 192.0.2.10:2222.
func newSSHDevice() *sshDevice {
	return &sshDevice{
		Stub: &inventorytest.Stub{StubID: "dev-1", StubName: "web1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: "192.0.2.10",
		port: 2222,
	}
}

// readFile returns path's contents, failing the test when it cannot.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a path under this test's own t.TempDir
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

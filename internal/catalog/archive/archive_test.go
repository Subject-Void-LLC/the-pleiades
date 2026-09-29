package archive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	archivemod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/archive"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with real tar, rm and mkdir -- creating and extracting real
// archives under t.TempDir(), never a fake script. Nothing about tar's
// exit codes, its auto-detection of compression, or the bytes it
// produces is stubbed; RULE 0 is what this whole namespace's subject
// matter (archive contents) demands, the same way file.line's own tests
// run real cat/mktemp/mv rather than asserting against the method's own
// report.

// ---------- harness ----------

type ctxStub struct {
	secrets   map[string]string
	stats     map[string]any
	failOnKey string
}

func (c *ctxStub) InjectSecrets() map[string]string { return c.secrets }
func (c *ctxStub) SetStat(key string, value any) error {
	if c.failOnKey != "" && key == c.failOnKey {
		return fmt.Errorf("ctxStub: injected failure recording %q", key)
	}
	c.stats[key] = value
	return nil
}
func (c *ctxStub) EmitFact(key string, value any) error { return c.SetStat(key, value) }

type target struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *target) SSHHost() string { return d.host }
func (d *target) SSHPort() int    { return d.port }

func noSSHDevice() inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NamePOSIXFileSystem}}
}

type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessBudgeted(t, -1)
}

func newHarnessBudgeted(t *testing.T, budget int) *harness {
	t.Helper()
	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return &harness{
		rc: &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}},
		device: &target{
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NamePOSIXFileSystem}},
			host: srv.Host, port: srv.Port,
		},
	}
}

func (h *harness) params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func inverseOf(rc *ctxStub) (fqcn string, params map[string]any, ok bool) {
	raw, present := rc.stats[sdk.StatInverse]
	if !present {
		return "", nil, false
	}
	record := raw.(map[string]any)
	fqcn, _ = record["fqcn"].(string)
	params, _ = record["params"].(map[string]any)
	return fqcn, params, true
}

func lookup(t *testing.T, fqcn string) collection.Descriptor {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	return desc
}

// writeFile writes content at path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeScript writes an executable shell script named name in dir. Used
// only by the two tests that need to control which stream a failure
// writes to (archive.go's failureDetail has no other way to reach its
// stdout-only and silent branches, since a real tar failure almost
// always explains itself on stderr); everything else in this file runs
// real tar.
func writeScript(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake %s: %v", name, err)
	}
}

// buildTarGz builds a real gzip-compressed tar archive at path holding
// one entry named name with content.
func buildTarGz(t *testing.T, path, name, content string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatalf("writing %s: %v", path, err)
	}
}

// readTarGzEntries opens a real gzip tar archive and returns its entry
// names and the content of each regular file entry, proving the archive
// this method wrote is genuinely readable, not merely present.
func readTarGzEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- path built by this test
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	entries := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("tar read: %v", err)
			}
			entries[hdr.Name] = string(data)
		}
	}
	return entries
}

// ---------- registration ----------

func TestRegistration(t *testing.T) {
	for _, fqcn := range []string{"archive.create", "archive.extract"} {
		desc := lookup(t, fqcn)
		if desc.Manifest.Status != collection.StatusImplemented {
			t.Errorf("%s: Status = %v, want StatusImplemented", fqcn, desc.Manifest.Status)
		}
		if desc.Invoke == nil {
			t.Errorf("%s: Invoke is nil", fqcn)
		}
		if desc.Manifest.Doc.Summary == "" {
			t.Errorf("%s: Doc.Summary is empty", fqcn)
		}
	}
	if !lookup(t, "archive.create").Manifest.Reversibility.Reversible {
		t.Error("archive.create: expected Reversible: true")
	}
	if lookup(t, "archive.extract").Manifest.Reversibility.Reversible {
		t.Error("archive.extract: expected Reversible: false")
	}
}

// ---------- Create ----------

func TestCreate_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := archivemod.Create(context.Background(), rc, noSSHDevice(), map[string]any{
		"path": "/tmp/x.tar.gz", "src": []any{"/tmp/a"},
	})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestCreate_MissingRequiredParams(t *testing.T) {
	h := newHarness(t)
	for _, params := range []map[string]any{
		{"src": []any{"/tmp/a"}},
		{"path": "/tmp/x.tar.gz"},
		{"path": "/tmp/x.tar.gz", "src": []any{}},
	} {
		if _, err := archivemod.Create(context.Background(), h.rc, h.device, params); err == nil {
			t.Errorf("params %v: expected a required-param error", params)
		}
	}
}

func TestCreate_SrcNotAList(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := archivemod.Create(context.Background(), rc, noSSHDevice(), map[string]any{
		"path": "/tmp/x.tar.gz", "src": "not-a-list",
	})
	if err == nil {
		t.Fatal("expected an error for a non-list src")
	}
}

func TestCreate_RemoveNotABool(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{dir}, "remove": "yes",
	})); err == nil {
		t.Fatal("expected an error for a non-bool remove")
	}
}

func TestCreate_TarFailsWithStdoutOnly(t *testing.T) {
	fakeBin := t.TempDir()
	writeScript(t, fakeBin, "tar", "#!/bin/sh\nprintf 'fake stdout failure\\n'\nexit 1\n")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := newHarness(t)
	dir := t.TempDir()
	_, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{dir},
	}))
	if err == nil || !strings.Contains(err.Error(), "fake stdout failure") {
		t.Fatalf("Create error = %v, want the fake tar's stdout-only failure surfaced", err)
	}
}

func TestCreate_TarFailsSilently(t *testing.T) {
	fakeBin := t.TempDir()
	writeScript(t, fakeBin, "tar", "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := newHarness(t)
	dir := t.TempDir()
	_, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{dir},
	}))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("Create error = %v, want it to mention 'no output'", err)
	}
}

func TestCreate_InvalidFormatRefusesBeforeConnecting(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := archivemod.Create(context.Background(), rc, noSSHDevice(), map[string]any{
		"path": "/tmp/x.zip", "src": []any{"/tmp/a"}, "format": "zip",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Create error = %v, want a format refusal (reached before any connection was attempted)", err)
	}
}

func TestCreate_AbsentCreatesRealTarGz(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "hello from the archive test")
	path := filepath.Join(dir, "out.tar.gz")

	h := newHarness(t)
	result, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": path, "src": []any{src},
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	entries := readTarGzEntries(t, path)
	found := false
	for name, content := range entries {
		if filepath.Base(name) == "src.txt" {
			found = true
			if content != "hello from the archive test" {
				t.Errorf("entry %s content = %q", name, content)
			}
		}
	}
	if !found {
		t.Fatalf("archive entries = %v, want src.txt present", entries)
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "file.remove" || params["path"] != path {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
}

func TestCreate_PlainTarFormat(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "plain tar content")
	path := filepath.Join(dir, "out.tar")

	h := newHarness(t)
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": path, "src": []any{src}, "format": "tar",
	})); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path built by this test
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		t.Fatal("expected a plain (uncompressed) tar, found gzip magic bytes")
	}
	tr := tar.NewReader(bytes.NewReader(data))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("reading the plain tar: %v", err)
	}
	if filepath.Base(hdr.Name) != "src.txt" {
		t.Fatalf("first entry = %q, want src.txt", hdr.Name)
	}
}

func TestCreate_AlreadyPresentIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "should never be read")
	path := filepath.Join(dir, "out.tar.gz")
	// Deliberately NOT a real archive: if Create ran tar anyway, this
	// would be replaced with real gzip bytes and the byte-for-byte
	// comparison below would fail.
	writeFile(t, path, "not a real archive")

	h := newHarness(t)
	result, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": path, "src": []any{src},
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path built by this test
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(data) != "not a real archive" {
		t.Fatalf("path content changed to %q, want it untouched", data)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a no-op")
	}
}

func TestCreate_RemoveDeletesSources(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "delete me after archiving")
	path := filepath.Join(dir, "out.tar.gz")

	h := newHarness(t)
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": path, "src": []any{src}, "remove": true,
	})); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", src, err)
	}
	entries := readTarGzEntries(t, path)
	if len(entries) == 0 {
		t.Fatal("expected the archive to hold the removed source's content")
	}
	// Removing the archive does not bring back the sources remove=true
	// deleted, so this undo, unlike a plain create's, is partial.
	if record, _ := h.rc.stats[sdk.StatInverse].(map[string]any); record[sdk.InversePartialKey] != true {
		t.Errorf("the undo of an archive that removed its sources is not marked partial: %v", record)
	}
}

func TestCreate_TarFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.tar.gz")
	h := newHarness(t)
	_, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": path, "src": []any{filepath.Join(dir, "does-not-exist")},
	}))
	if err == nil {
		t.Fatal("expected a real tar failure for a nonexistent source")
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded on failure")
	}
}

func TestCreate_ConnectionDiesStattingPath(t *testing.T) {
	h := newHarnessBudgeted(t, 0)
	dir := t.TempDir()
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{dir},
	})); err == nil {
		t.Fatal("expected a connection failure statting path")
	}
}

func TestCreate_ConnectionDiesRunningTar(t *testing.T) {
	h := newHarnessBudgeted(t, 1)
	dir := t.TempDir()
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{dir},
	})); err == nil {
		t.Fatal("expected a connection failure running tar")
	}
}

func TestCreate_ConnectionDiesRemovingSources(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "content")
	h := newHarnessBudgeted(t, 2)
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{src}, "remove": true,
	})); err == nil {
		t.Fatal("expected a connection failure removing sources")
	}
}

func TestCreate_ConnectionDiesStattingAfter(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "content")
	h := newHarnessBudgeted(t, 2)
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{src},
	})); err == nil {
		t.Fatal("expected a connection failure statting path after creation")
	}
}

func TestCreate_RecordStatFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "content")
	h := newHarness(t)
	h.rc.failOnKey = "path"
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{src},
	})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestCreate_RecordDiffFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "content")
	h := newHarness(t)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{src},
	})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

func TestCreate_RecordInverseFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "content")
	h := newHarness(t)
	h.rc.failOnKey = sdk.StatInverse
	if _, err := archivemod.Create(context.Background(), h.rc, h.device, h.params(map[string]any{
		"path": filepath.Join(dir, "out.tar.gz"), "src": []any{src},
	})); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}

// ---------- Extract ----------

func TestExtract_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := archivemod.Extract(context.Background(), rc, noSSHDevice(), map[string]any{
		"src": "/tmp/x.tar.gz", "dest": "/tmp/out",
	})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestExtract_MissingRequiredParams(t *testing.T) {
	h := newHarness(t)
	for _, params := range []map[string]any{
		{"dest": "/tmp/out"},
		{"src": "/tmp/x.tar.gz"},
	} {
		if _, err := archivemod.Extract(context.Background(), h.rc, h.device, params); err == nil {
			t.Errorf("params %v: expected a required-param error", params)
		}
	}
}

func TestExtract_RemoveNotABool(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"), "remove": "yes",
	})); err == nil {
		t.Fatal("expected an error for a non-bool remove")
	}
}

func TestExtract_ExtractsRealArchive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "extracted content")
	dest := filepath.Join(dir, "out")

	h := newHarness(t)
	result, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": dest,
	}))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	data, err := os.ReadFile(filepath.Join(dest, "payload.txt")) // #nosec G304 -- path built by this test
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(data) != "extracted content" {
		t.Fatalf("extracted content = %q", data)
	}
}

func TestExtract_ExtractsIntoExistingDirWithoutDisturbingOtherFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "new.txt", "new content")
	dest := filepath.Join(dir, "out")
	writeFile(t, filepath.Join(dest, "existing.txt"), "already there")

	h := newHarness(t)
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": dest,
	})); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "existing.txt")); err != nil || string(data) != "already there" { // #nosec G304 -- test fixture
		t.Fatalf("existing.txt = %q, err = %v, want it undisturbed", data, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "new.txt")); err != nil {
		t.Fatalf("expected new.txt to be extracted: %v", err)
	}
}

func TestExtract_CreatesAlreadyPresentSkips(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "should not be extracted")
	dest := filepath.Join(dir, "out")
	marker := filepath.Join(dir, "marker")
	writeFile(t, marker, "already done")

	h := newHarness(t)
	result, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": dest, "creates": marker,
	}))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected dest to remain absent, stat err = %v", err)
	}
}

func TestExtract_NoCreatesAlwaysExtracts(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	dest := filepath.Join(dir, "out")

	h := newHarness(t)
	for i := 0; i < 2; i++ {
		result, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
			"src": src, "dest": dest,
		}))
		if err != nil {
			t.Fatalf("Extract run %d: %v", i, err)
		}
		if !result.Changed {
			t.Fatalf("Extract run %d: expected Changed = true (no creates was given)", i)
		}
	}
}

func TestExtract_RemoveDeletesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	dest := filepath.Join(dir, "out")

	h := newHarness(t)
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": dest, "remove": true,
	})); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", src, err)
	}
}

func TestExtract_TarFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "not-an-archive.tar.gz")
	writeFile(t, src, "definitely not a tar file")
	dest := filepath.Join(dir, "out")

	h := newHarness(t)
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": dest,
	})); err == nil {
		t.Fatal("expected a real tar failure for a corrupt archive")
	}
}

func TestExtract_ConnectionDiesCheckingCreates(t *testing.T) {
	h := newHarnessBudgeted(t, 0)
	dir := t.TempDir()
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"), "creates": filepath.Join(dir, "marker"),
	})); err == nil {
		t.Fatal("expected a connection failure checking creates")
	}
}

func TestExtract_ConnectionDiesStattingDestWhenCreatesFound(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	writeFile(t, marker, "done")
	h := newHarnessBudgeted(t, 1)
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"), "creates": marker,
	})); err == nil {
		t.Fatal("expected a connection failure statting dest after finding creates")
	}
}

func TestExtract_ConnectionDiesStattingDest(t *testing.T) {
	h := newHarnessBudgeted(t, 0)
	dir := t.TempDir()
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected a connection failure statting dest")
	}
}

func TestExtract_ConnectionDiesMakingDirectory(t *testing.T) {
	h := newHarnessBudgeted(t, 1)
	dir := t.TempDir()
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected a connection failure creating dest")
	}
}

func TestExtract_ConnectionDiesRunningTar(t *testing.T) {
	h := newHarnessBudgeted(t, 2)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected a connection failure running tar")
	}
}

func TestExtract_ConnectionDiesRemovingSource(t *testing.T) {
	h := newHarnessBudgeted(t, 3)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": filepath.Join(dir, "out"), "remove": true,
	})); err == nil {
		t.Fatal("expected a connection failure removing src")
	}
}

func TestExtract_ConnectionDiesStattingAfter(t *testing.T) {
	h := newHarnessBudgeted(t, 3)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected a connection failure statting dest after extraction")
	}
}

func TestExtract_RecordStatFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	h := newHarness(t)
	h.rc.failOnKey = "dest"
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestExtract_RecordDiffFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "payload.txt", "content")
	h := newHarness(t)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": src, "dest": filepath.Join(dir, "out"),
	})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

func TestExtract_RecordStatFailsWhenSkipped(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	writeFile(t, marker, "done")
	h := newHarness(t)
	h.rc.failOnKey = "dest"
	if _, err := archivemod.Extract(context.Background(), h.rc, h.device, h.params(map[string]any{
		"src": filepath.Join(dir, "in.tar.gz"), "dest": filepath.Join(dir, "out"), "creates": marker,
	})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface even on the skip path")
	}
}

// Tests that pleiades.builtin.project.write reads its parameters, refuses
// a context with no project, and writes through the run's own project
// writer; the real writer's containment is internal/engine's to test, and
// the whole path through the binary is cmd/pleiades's.
package project

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recorder keeps the stats a call sets.
type recorder struct{ stats map[string]any }

func (r *recorder) InjectSecrets() map[string]string { return nil }
func (r *recorder) SetStat(k string, v any) error    { r.stats[k] = v; return nil }
func (r *recorder) EmitFact(string, any) error       { return nil }

// writerContext is a recorder that answers sdk.ProjectFileWriter, keeping
// what it was asked to write.
type writerContext struct {
	recorder
	name    string
	data    []byte
	perm    fs.FileMode
	write   bool
	changed bool
	err     error
}

func (w *writerContext) WriteProjectFile(name string, data []byte, perm fs.FileMode, write bool) (bool, error) {
	w.name, w.data, w.perm, w.write = name, data, perm, write
	return w.changed, w.err
}

// TestWrite_PassesThePathBytesAndModeThrough: a run writes, a check does
// not, the default mode is 0600, a quoted mode is read as octal, an empty
// content is an empty file, and the stats name the file and its digest.
func TestWrite_PassesThePathBytesAndModeThrough(t *testing.T) {
	ctx := context.Background()
	w := &writerContext{recorder: recorder{stats: map[string]any{}}, changed: true}
	res, err := Write(ctx, w, nil, map[string]any{"path": "configs/r3.conf", "content": "hostname r3\n"})
	if err != nil || !res.Changed {
		t.Fatalf("Write = %v, %v", res, err)
	}
	if w.name != "configs/r3.conf" || string(w.data) != "hostname r3\n" || w.perm != 0o600 || !w.write {
		t.Errorf("asked to write %q %q %#o write=%v", w.name, w.data, w.perm, w.write)
	}
	if w.stats["path"] != "configs/r3.conf" || w.stats["size"] != 12 || len(w.stats["sha256"].(string)) != 64 {
		t.Errorf("stats %v", w.stats)
	}
	if _, warned := w.stats[sdk.StatWarnings]; warned {
		t.Errorf("an unmasked file was warned about: %v", w.stats)
	}

	c := &writerContext{recorder: recorder{stats: map[string]any{}}}
	res, err = Check(ctx, c, nil, map[string]any{"path": "x", "content": "", "mode": "0640"})
	if err != nil || res.Changed || c.write || c.perm != 0o640 || len(c.data) != 0 {
		t.Errorf("Check = %v, %v; asked %#o write=%v data %q", res, err, c.perm, c.write, c.data)
	}
	if _, err := Check(ctx, c, nil, map[string]any{"path": "x", "content": "a", "mode": "755"}); err != nil || c.perm != 0o755 {
		t.Errorf("a three-digit mode: %v, %#o", err, c.perm)
	}
}

// TestWrite_WarnsAboutAMaskedConfiguration: content holding a masked
// secret is written, and the task warns that it cannot be applied as it is.
func TestWrite_WarnsAboutAMaskedConfiguration(t *testing.T) {
	w := &writerContext{recorder: recorder{stats: map[string]any{}}, changed: true}
	masked := netcfg.Mask("interface eth1\n ip ospf authentication-key s3cret\n")
	if _, err := Write(context.Background(), w, nil, map[string]any{"path": "r1.conf", "content": masked}); err != nil {
		t.Fatal(err)
	}
	warnings, _ := w.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "put each secret back") {
		t.Errorf("warnings %v", w.stats[sdk.StatWarnings])
	}
}

// TestWrite_RefusesBeforeWriting: a missing path or content, content that
// is not text, a mode that is unquoted, not octal or beyond permission
// bits, a context with no project and the writer's own refusal are each
// an error naming the method, and nothing is written for the first six.
func TestWrite_RefusesBeforeWriting(t *testing.T) {
	ctx := context.Background()
	for params, want := range map[*map[string]any]string{
		{"content": "x"}:                              "path",
		{"path": "x"}:                                 "content is required",
		{"path": "x", "content": nil}:                 "content is required",
		{"path": "x", "content": 42}:                  "content must be text",
		{"path": "x", "content": "x", "mode": 420}:    "quote it",
		{"path": "x", "content": "x", "mode": "rw"}:   "is not an octal mode",
		{"path": "x", "content": "x", "mode": "4755"}: "is not an octal mode",
	} {
		w := &writerContext{recorder: recorder{stats: map[string]any{}}}
		if _, err := Write(ctx, w, nil, *params); err == nil || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), fqcn) {
			t.Errorf("%v: %v; want it refused saying %q", *params, err, want)
		}
		if w.name != "" {
			t.Errorf("%v reached the writer", *params)
		}
	}
	if _, err := Write(ctx, &recorder{stats: map[string]any{}}, nil, map[string]any{"path": "x", "content": "x"}); !errors.Is(err, sdk.ErrNoProjectFiles) {
		t.Errorf("a context with no project: %v", err)
	}
	boom := errors.New("x passes through a symbolic link")
	if _, err := Write(ctx, &writerContext{recorder: recorder{stats: map[string]any{}}, err: boom}, nil, map[string]any{"path": "x", "content": "x"}); !errors.Is(err, boom) {
		t.Errorf("the writer's refusal: %v", err)
	}
}

// TestWrite_IsRegisteredAsItsDocSays: the registry holds the method as a
// deviceless, controller-side, checkable, not-reversible implementation.
func TestWrite_IsRegisteredAsItsDocSays(t *testing.T) {
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatal("not registered")
	}
	m := d.Manifest
	if m.Status != collection.StatusImplemented || !m.SupportsCheck || m.Reversibility.Reversible || m.Reversibility.Notes == "" ||
		m.ExecutionContext.Device != collection.DeviceNone || m.ExecutionContext.Site != collection.SiteController {
		t.Errorf("manifest %+v", m)
	}
	if d.NeedsDevice(map[string]any{"path": "x", "content": "x"}) {
		t.Error("a call needs a device")
	}
}

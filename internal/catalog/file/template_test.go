package file_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// These tests run file.template against the same real SSH server and real
// /bin/sh file.copy's tests use. The method is handed RENDERED text (the
// engine renders; internal/engine's template_source_test.go proves that
// half), so what is proven here is that it writes those bytes the way
// file.copy does, checks without writing, undoes, and refuses an unrendered
// src. Every identifier is prefixed "template" because the sibling methods
// share one test package.

// templateParams is a file.template task's params with host key checks off.
func templateParams(dest, content string) map[string]any {
	return copyParams(dest, content)
}

// TestTemplate_Registered proves the method is registered as implemented,
// reversible and checkable, with src declared a template file.
func TestTemplate_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.template")
	if !ok {
		t.Fatal("collection.Lookup(file.template) found nothing; did this package's init() run?")
	}
	if d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil || d.Check == nil {
		t.Errorf("status %v, invoke set %v, check set %v; want implemented with both", d.Manifest.Status, d.Invoke != nil, d.Check != nil)
	}
	if !d.Manifest.SupportsCheck || !d.Manifest.Reversibility.Reversible {
		t.Error("file.template must support check and be reversible, as file.copy is")
	}
	var found bool
	for _, p := range d.Manifest.Doc.Params {
		if p.Name == "src" && p.Format == collection.ParamFormatTemplateFile {
			found = true
		}
	}
	if !found {
		t.Error("src is not declared ParamFormatTemplateFile, so the engine would never render it")
	}
}

// TestTemplate_WritesChecksAndUndoes is the table over a create, a converged
// rerun, an update and a check, each read back from the real filesystem.
func TestTemplate_WritesChecksAndUndoes(t *testing.T) {
	server := startCopyServer(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.conf")
	device := newCopyTarget(server)
	ctx := context.Background()

	run := func(t *testing.T, content string, check bool) (*copyContext, collection.Result) {
		t.Helper()
		rc := newCopyContext(server)
		params := templateParams(dest, content)
		params["mode"] = "0644"
		fn := file.Template
		if check {
			fn = file.CheckTemplate
		}
		res, err := fn(ctx, rc, device, params)
		if err != nil {
			t.Fatalf("file.template (check=%v): %v", check, err)
		}
		return rc, res
	}

	t.Run("a check on a missing file predicts and writes nothing", func(t *testing.T) {
		_, res := run(t, "env=prod\n", true)
		if !res.Changed {
			t.Error("check predicted no change for a file that does not exist")
		}
		if _, err := os.Lstat(dest); err == nil {
			t.Fatal("a check created the file")
		}
	})

	t.Run("create writes the rendered bytes and records a removal", func(t *testing.T) {
		rc, res := run(t, "env=prod\n", false)
		if !res.Changed || copyFileBytes(t, dest) != "env=prod\n" || copyFileMode(t, dest) != "0644" {
			t.Fatalf("changed=%v bytes=%q mode=%s", res.Changed, copyFileBytes(t, dest), copyFileMode(t, dest))
		}
		inv := copyRecordedInverse(t, rc)
		if inv["fqcn"] != "file.remove" {
			t.Errorf("inverse = %v, want a file.remove of the created file", inv)
		}
	})

	t.Run("the same bytes are a converged run", func(t *testing.T) {
		rc, res := run(t, "env=prod\n", false)
		if res.Changed {
			t.Error("rewriting identical bytes reported a change")
		}
		if _, has := rc.stats["inverse"]; has {
			t.Error("a converged run emitted an inverse")
		}
		if _, res := run(t, "env=prod\n", true); res.Changed {
			t.Error("check predicted a change for identical bytes")
		}
	})

	t.Run("new bytes overwrite, the diff holds checksums and never the text", func(t *testing.T) {
		rc, res := run(t, "env=stage\ntoken=hunter2-secret\n", false)
		if !res.Changed || copyFileBytes(t, dest) != "env=stage\ntoken=hunter2-secret\n" {
			t.Fatalf("changed=%v bytes=%q", res.Changed, copyFileBytes(t, dest))
		}
		for key, v := range rc.stats {
			if strings.Contains(strings.ToLower(toText(v)), "hunter2") {
				t.Errorf("stat %q carries the rendered text: %v", key, v)
			}
		}
		if inv := copyRecordedInverse(t, rc); inv["fqcn"] != "file.permissions" {
			t.Errorf("inverse = %v, want file.permissions for an overwrite", inv)
		}
	})
}

// toText flattens a stat value to text for the leak assertion above.
func toText(v any) string {
	return strings.TrimSpace(strings.Join(strings.Fields(fmt.Sprint(v)), " "))
}

// TestTemplate_RefusesAnUnrenderedSrc: src reaching the method means the
// engine did not render it, and writing it would put template text on a
// device, so both the run and the check refuse before connecting.
func TestTemplate_RefusesAnUnrenderedSrc(t *testing.T) {
	for name, fn := range map[string]collection.Method{"run": file.Template, "check": file.CheckTemplate} {
		t.Run(name, func(t *testing.T) {
			_, err := fn(context.Background(), nil, nil, map[string]any{"dest": "/etc/x", "src": "t/x.conf"})
			if err == nil || !strings.Contains(err.Error(), "unrendered") {
				t.Fatalf("err = %v, want a refusal naming the unrendered src", err)
			}
		})
	}
}

// TestTemplate_NeedsContent: with neither content nor src the error names
// file.template, not file.copy.
func TestTemplate_NeedsContent(t *testing.T) {
	_, err := file.Template(context.Background(), nil, nil, map[string]any{"dest": "/etc/x"})
	if err == nil || !strings.Contains(err.Error(), "content or src is required") {
		t.Fatalf("err = %v", err)
	}
}

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestCatalogEntries proves the registry catalog_builtins.go's blank
// import populates is actually reachable from this package: every FQCN in
// catalogdata.Collections resolves through collection.Lookup, and at
// least one of the four real net.catalyst.* methods is present with
// Status implemented, the same registry tools/gendocs reads for the
// generated module pages.
func TestCatalogEntries(t *testing.T) {
	entries, err := catalogEntries()
	if err != nil {
		t.Fatalf("catalogEntries() error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("catalogEntries() returned no entries")
	}

	found := false
	for _, e := range entries {
		if e.FQCN == "net.catalyst.device_facts" {
			found = true
			if e.Manifest.Status != collection.StatusImplemented {
				t.Errorf("net.catalyst.device_facts: Status = %q, want %q", e.Manifest.Status, collection.StatusImplemented)
			}
		}
	}
	if !found {
		t.Error("catalogEntries() did not include net.catalyst.device_facts")
	}
}

func TestRunDoc_List(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"--list", "net.catalyst"}); err != nil {
			t.Fatalf("runDoc(--list net.catalyst) error: %v", err)
		}
	})
	if !strings.Contains(out, "net.catalyst.device_facts") {
		t.Errorf("--list net.catalyst output missing net.catalyst.device_facts, got:\n%s", out)
	}
	if strings.Contains(out, "pkg.apt.install") {
		t.Errorf("--list net.catalyst output should not include an unrelated namespace, got:\n%s", out)
	}
}

func TestRunDoc_ListUnknownNamespace(t *testing.T) {
	err := runDoc([]string{"--list", "does.not.exist"})
	if err == nil {
		t.Fatal("runDoc(--list does.not.exist) returned nil error, want a no-match error")
	}
}

func TestRunDoc_Entry(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"net.catalyst.device_facts"}); err != nil {
			t.Fatalf("runDoc(net.catalyst.device_facts) error: %v", err)
		}
	})
	for _, want := range []string{"net.catalyst.device_facts", "attributes:", "parameters:", "returns:", "examples:"} {
		if !strings.Contains(out, want) {
			t.Errorf("runDoc entry output missing %q, got:\n%s", want, out)
		}
	}
}

func TestRunDoc_EntryDeclared(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"archive.create"}); err != nil {
			t.Fatalf("runDoc(archive.create) error: %v", err)
		}
	})
	if !strings.Contains(out, "declared, not implemented") {
		t.Errorf("declared entry output missing the declared-status line, got:\n%s", out)
	}
	if strings.Contains(out, "parameters:") || strings.Contains(out, "examples:") {
		t.Errorf("declared entry output should omit parameters/examples, got:\n%s", out)
	}
}

func TestRunDoc_UnknownFQCN(t *testing.T) {
	err := runDoc([]string{"nope.nope"})
	if err == nil {
		t.Fatal("runDoc(nope.nope) returned nil error, want an unknown-fqcn error")
	}
	if !strings.Contains(err.Error(), "unknown fqcn") {
		t.Errorf("runDoc(nope.nope) error = %v, want it to mention 'unknown fqcn'", err)
	}
}

func TestRunDoc_Snippet(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"--snippet", "net.catalyst.device_facts"}); err != nil {
			t.Fatalf("runDoc(--snippet ...) error: %v", err)
		}
	})
	if !strings.Contains(out, "fqcn: net.catalyst.device_facts") {
		t.Errorf("snippet output missing fqcn line, got:\n%s", out)
	}
}

func TestRunDoc_SnippetDeclaredFallsBackToSkeleton(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"--snippet", "archive.create"}); err != nil {
			t.Fatalf("runDoc(--snippet archive.create) error: %v", err)
		}
	})
	if !strings.Contains(out, "- name: TODO") || !strings.Contains(out, "fqcn: archive.create") {
		t.Errorf("declared snippet should fall back to a bare skeleton, got:\n%s", out)
	}
}

func TestRunDoc_SnippetRequiresFQCN(t *testing.T) {
	err := runDoc([]string{"--snippet"})
	if err == nil {
		t.Fatal("runDoc(--snippet) with no fqcn returned nil error")
	}
}

func TestRunDoc_JSONFullCatalog(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"--json"}); err != nil {
			t.Fatalf("runDoc(--json) error: %v", err)
		}
	})

	var m map[string]collection.Manifest
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("runDoc(--json) output did not parse as a Manifest map: %v", err)
	}
	entry, ok := m["net.catalyst.device_facts"]
	if !ok {
		t.Fatal("runDoc(--json) output missing net.catalyst.device_facts")
	}
	if entry.Status != collection.StatusImplemented {
		t.Errorf("net.catalyst.device_facts Status = %q, want %q", entry.Status, collection.StatusImplemented)
	}
	if !strings.Contains(out, ">=1.0.0") {
		t.Error("runDoc(--json) output should contain a plain, unescaped \">=1.0.0\" engineVersion, not an HTML-escaped one")
	}
}

func TestRunDoc_JSONSingleEntry(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"--json", "net.catalyst.device_facts"}); err != nil {
			t.Fatalf("runDoc(--json net.catalyst.device_facts) error: %v", err)
		}
	})

	var m collection.Manifest
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("runDoc(--json <fqcn>) output did not parse as a single Manifest: %v", err)
	}
	if m.Status != collection.StatusImplemented {
		t.Errorf("Status = %q, want %q", m.Status, collection.StatusImplemented)
	}
}

func TestRunDoc_NoArgsIsUsageError(t *testing.T) {
	if err := runDoc(nil); err == nil {
		t.Fatal("runDoc(nil) returned nil error, want a usage error")
	}
}

func TestRunDoc_TooManyPositionalArgs(t *testing.T) {
	if err := runDoc([]string{"a", "b"}); err == nil {
		t.Fatal("runDoc with two positional args returned nil error")
	}
}

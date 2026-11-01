// Tests of refreshing an OpenAPI contract.json from its vendored file.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
)

// TestRun_RefreshesOpenAPIContractsOnly: an OpenAPI contract gets its
// vendored file's operations and digest, a second run writes nothing, a CLI
// contract is left exactly as written, and a vendored file that is not an
// OpenAPI document is refused.
func TestRun_RefreshesOpenAPIContractsOnly(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, coverageledger.CollectionsDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("x.api/contract.json", `{"collection": "x.api", "title": "an api", "source": {"kind": "openapi", "url": "u", "version": "1", "sha256": "", "file": "source/api.json"}, "ops": []}`)
	write("x.api/map.yaml", "")
	write("x.api/source/api.json", `{"paths": {"/a": {"get": {"summary": "read a & b"}}}}`)
	cli := `{"collection": "x.cli", "title": "a tool", "source": {"kind": "cli", "url": "u", "version": "1", "sha256": ""}, "ops": [{"id": "go"}]}`
	write("x.cli/contract.json", cli)
	write("x.cli/map.yaml", "")

	if err := run(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, coverageledger.CollectionsDir, "x.api", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id": "GET /a"`, `"summary": "read a & b"`, `"sha256": "`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("contract.json %s does not hold %s", got, want)
		}
	}
	// The digest is the vendored file's, by value.
	sum := sha256.Sum256([]byte(`{"paths": {"/a": {"get": {"summary": "read a & b"}}}}`))
	if want := `"sha256": "` + hex.EncodeToString(sum[:]) + `"`; !strings.Contains(string(got), want) {
		t.Errorf("contract.json %s does not hold the vendored file's digest %s", got, want)
	}
	// A second run finds nothing to change.
	if changed, err := refresh(mustLoad(t, root, "x.api")); err != nil || changed {
		t.Errorf("a second refresh changed %v, err %v", changed, err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, coverageledger.CollectionsDir, "x.cli", "contract.json")); string(after) != cli {
		t.Errorf("a CLI contract was rewritten: %s", after)
	}

	write("x.api/source/api.json", `{"openapi": "3.0.0"}`)
	if err := run(root); err == nil || !strings.Contains(err.Error(), "x.api") {
		t.Errorf("a vendored file with no paths: %v", err)
	}
}

// mustLoad reads one ledger under root.
func mustLoad(t *testing.T, root, name string) coverageledger.Ledger {
	t.Helper()
	l, err := coverageledger.Load(filepath.Join(root, coverageledger.CollectionsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

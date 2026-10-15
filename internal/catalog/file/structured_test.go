// Tests for file.json and file.yaml against a real SSH server whose
// commands run through a real /bin/sh on this machine.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// structured runs fqcn (checking when check is set) with params.
func structured(t *testing.T, server copyServer, fqcn string, check bool, params map[string]any) (*copyContext, collection.Result, error) {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	params["insecure_skip_host_key_verify"] = true
	rc := newCopyContext(server)
	call := desc.Invoke
	if check {
		call = desc.Check
	}
	res, err := call(context.Background(), rc, newCopyTarget(server), params)
	return rc, res, err
}

// TestJSON_SetsAKeyAndKeepsTheRest: a check predicts and writes nothing;
// the run sets the key keeping order, numbers and mode; again is no
// change; the undo carries the earlier text; absent removes the key.
func TestJSON_SetsAKeyAndKeepsTheRest(t *testing.T) {
	server := startCopyServer(t)
	p := filepath.Join(t.TempDir(), "config.json")
	in := "{\n    \"id\": 12345678901234567890,\n    \"server\": {\n        \"port\": 80\n    },\n    \"name\": \"web\"\n}\n"
	if err := os.WriteFile(p, []byte(in), 0o640); err != nil {
		t.Fatal(err)
	}
	params := func() map[string]any { return map[string]any{"path": p, "key": "server.port", "value": 8443} }

	_, res, err := structured(t, server, "file.json", true, params())
	if err != nil || !res.Changed {
		t.Fatalf("check: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(p); string(got) != in {
		t.Fatal("a check wrote the file")
	}
	rc, res, err := structured(t, server, "file.json", false, params())
	if err != nil || !res.Changed {
		t.Fatalf("run: %+v %v", res, err)
	}
	want := strings.Replace(in, "\"port\": 80", "\"port\": 8443", 1)
	if got, _ := os.ReadFile(p); string(got) != want {
		t.Fatalf("file\n%s\nwant\n%s", got, want)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	if inv["fqcn"] != "file.copy" || inv["params"].(map[string]any)["content"] != in {
		t.Errorf("undo %v", inv)
	}
	if _, res, err := structured(t, server, "file.json", false, params()); err != nil || res.Changed {
		t.Errorf("again: %+v %v", res, err)
	}
	if _, res, err := structured(t, server, "file.json", false, map[string]any{"path": p, "key": "name", "state": "absent"}); err != nil || !res.Changed {
		t.Fatalf("absent: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "\"name\"") || !strings.Contains(string(got), "12345678901234567890") {
		t.Errorf("after absent:\n%s", got)
	}
}

// TestYAML_SetsAKeyAndKeepsComments: comments survive, a missing file is
// refused unless create, and a created file's undo removes it.
func TestYAML_SetsAKeyAndKeepsComments(t *testing.T) {
	server := startCopyServer(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(p, []byte("# app values\nweb:\n  replicas: 1   # scaled by ops\n  image: app:1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := structured(t, server, "file.yaml", false, map[string]any{"path": p, "key": "web.replicas", "value": 3}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	for _, want := range []string{"# app values", "replicas: 3 # scaled by ops", "image: app:1.0"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("%q is not in\n%s", want, got)
		}
	}
	missing := filepath.Join(dir, "new.yaml")
	if _, _, err := structured(t, server, "file.yaml", false, map[string]any{"path": missing, "key": "a", "value": 1}); err == nil || !strings.Contains(err.Error(), "create") {
		t.Errorf("a missing file: %v", err)
	}
	rc, _, err := structured(t, server, "file.yaml", false, map[string]any{"path": missing, "key": "a.b", "value": []any{1, 2}, "create": true})
	if err != nil {
		t.Fatal(err)
	}
	if inv, _ := rc.stats[sdk.StatInverse].(map[string]any); inv["fqcn"] != "file.remove" {
		t.Errorf("undo %v", inv)
	}
	if got, _ := os.ReadFile(missing); !strings.Contains(string(got), "b:") {
		t.Errorf("created:\n%s", got)
	}
}

// TestStructured_Refuses: a document with a duplicate key, a YAML alias
// on the path, a relative path and a value with state absent, each with
// nothing written.
func TestStructured_Refuses(t *testing.T) {
	server := startCopyServer(t)
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.json")
	_ = os.WriteFile(dup, []byte(`{"a":1,"a":2}`), 0o644)
	alias := filepath.Join(dir, "alias.yaml")
	_ = os.WriteFile(alias, []byte("base: &b {port: 80}\nweb: *b\n"), 0o644)
	for name, tc := range map[string]struct {
		fqcn   string
		params map[string]any
		want   string
	}{
		"duplicate key":   {"file.json", map[string]any{"path": dup, "key": "a", "value": 3}, "appears twice"},
		"alias":           {"file.yaml", map[string]any{"path": alias, "key": "web.port", "value": 3}, "alias"},
		"relative path":   {"file.json", map[string]any{"path": "conf.json", "key": "a", "value": 3}, "absolute"},
		"absent, a value": {"file.json", map[string]any{"path": dup, "key": "a", "value": 3, "state": "absent"}, "goes with state present"},
		"no value":        {"file.yaml", map[string]any{"path": alias, "key": "a"}, "value is required"},
	} {
		if _, _, err := structured(t, server, tc.fqcn, false, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := os.ReadFile(dup); string(got) != `{"a":1,"a":2}` {
		t.Error("a refused document was written")
	}
}

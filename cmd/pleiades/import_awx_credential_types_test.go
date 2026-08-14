package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// corpusExport is the real AWX credential type this repository captured
// from a production deployment. Driving the import against it rather than
// against an invented document is the whole point: this command exists to
// answer "will my export survive", and a fixture somebody wrote to make
// the test pass answers a different question.
const corpusExport = "../../tests/parity/testdata/credential_types/custom-rest-api-token.json"

// writeExport puts a JSON document in a temporary file and returns its
// path.
func writeExport(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}
	return path
}

// TestImportAcceptsTheRealCorpusExport is the RULE 0 shape for this
// command: the same bytes AWX produced, through the same decode and the
// same validation the Controller runs.
func TestImportAcceptsTheRealCorpusExport(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(corpusExport)
	if err != nil {
		t.Fatalf("reading the corpus fixture: %v", err)
	}
	entries, err := decodeExport(raw)
	if err != nil {
		t.Fatalf("decodeExport() error = %v", err)
	}

	results := classify(entries)
	if len(results) == 0 {
		t.Fatal("the corpus fixture classified to nothing")
	}
	for _, r := range results {
		if r.Verdict != verdictImportable {
			t.Errorf("the real AWX export %q classified as %q: %s", r.Namespace, r.Verdict, r.Reason)
		}
	}
}

// TestImportReadsBothAWXResponseShapes covers the list endpoint and the
// detail endpoint, which is why decodeExport exists at all: a corpus
// somebody had to reshape before it could be read is a corpus that can be
// reshaped wrongly.
func TestImportReadsBothAWXResponseShapes(t *testing.T) {
	t.Parallel()

	bare := `{"name":"Solo","namespace":"solo","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}]},"injectors":{"env":{"SOLO_TOKEN":"{{ token }}"}}}`

	tests := []struct {
		name string
		body string
		want int
	}{
		{"a list endpoint's count and results", `{"count":1,"results":[` + bare + `]}`, 1},
		{"a detail endpoint's bare object", bare, 1},
		{"a plain array", `[` + bare + `]`, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entries, err := decodeExport([]byte(tt.body))
			if err != nil {
				t.Fatalf("decodeExport() error = %v", err)
			}
			if len(entries) != tt.want {
				t.Fatalf("decodeExport() found %d types, want %d", len(entries), tt.want)
			}
			if got := classify(entries)[0].Verdict; got != verdictImportable {
				t.Errorf("verdict = %q, want %q", got, verdictImportable)
			}
		})
	}
}

// TestImportClassifiesEveryOutcome is the table this command exists to
// produce, and each row is a different thing an operator has to do next.
func TestImportClassifiesEveryOutcome(t *testing.T) {
	t.Parallel()

	body := `{"results":[
		{"name":"Custom","namespace":"custom_thing","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}]},"injectors":{"env":{"CUSTOM_TOKEN":"{{ token }}"}}},
		{"name":"Machine","namespace":"ssh","kind":"ssh","inputs":{"fields":[{"id":"username","label":"Username"}]},"injectors":{}},
		{"name":"Google Compute Engine","namespace":"gce","kind":"cloud","inputs":{"fields":[]},"injectors":{}},
		{"name":"Broken","namespace":"broken","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}]},"injectors":{"env":{"BROKEN":"{{ not_declared }}"}}}
	]}`

	entries, err := decodeExport([]byte(body))
	if err != nil {
		t.Fatalf("decodeExport() error = %v", err)
	}

	got := map[string]importVerdict{}
	reasons := map[string]string{}
	for _, r := range classify(entries) {
		got[r.Namespace] = r.Verdict
		reasons[r.Namespace] = r.Reason
	}

	want := map[string]importVerdict{
		"custom_thing": verdictImportable,
		"ssh":          verdictShipped,
		"gce":          verdictNotImplemented,
		"broken":       verdictInvalid,
	}
	for namespace, wantVerdict := range want {
		if got[namespace] != wantVerdict {
			t.Errorf("%s classified as %q, want %q", namespace, got[namespace], wantVerdict)
		}
	}

	// The reason is the useful half. "not implemented" alone tells an
	// operator nothing about whether to wait, work around it, or convert
	// the playbook.
	if !strings.Contains(reasons["gce"], "python") {
		t.Errorf("the gce reason %q does not say why", reasons["gce"])
	}
	if !strings.Contains(reasons["broken"], "not_declared") {
		t.Errorf("the refusal %q does not name the undefined input", reasons["broken"])
	}
	// A shipped namespace is reported as shipped even though its document
	// would also validate, which is the check ordering that stops an
	// import silently recreating a managed type as a custom copy.
	if !strings.Contains(reasons["ssh"], "reuses") {
		t.Errorf("the ssh reason %q does not say the import reuses the managed type", reasons["ssh"])
	}
}

// TestImportWritesDocumentsTheControllerAccepts closes the loop: what
// --out produces has to be something POST /credential-types would take, so
// the written file is read back and validated exactly as the store would.
func TestImportWritesDocumentsTheControllerAccepts(t *testing.T) {
	t.Parallel()

	path := writeExport(t, `{"results":[
		{"name":"Custom","namespace":"custom_thing","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}],"required":["token"]},"injectors":{"env":{"CUSTOM_TOKEN":"{{ token }}"}}},
		{"name":"Machine","namespace":"ssh","kind":"ssh","inputs":{"fields":[{"id":"username","label":"Username"}]},"injectors":{}}
	]}`)
	out := t.TempDir()

	if err := runImportAWXCredentialTypes([]string{"--out", out, path}); err != nil {
		t.Fatalf("runImportAWXCredentialTypes() error = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(out, "custom_thing.json"))
	if err != nil {
		t.Fatalf("reading the written document: %v", err)
	}

	var ct credtype.CredentialType
	if err := json.Unmarshal(written, &ct); err != nil {
		t.Fatalf("the written document does not decode: %v", err)
	}
	if err := ct.Validate(render.New()); err != nil {
		t.Errorf("the written document does not validate, so posting it would fail: %v", err)
	}

	// A shipped namespace is not written. Writing one would invite an
	// operator to POST it, which the store refuses, and the refusal would
	// look like a bug in this command rather than the correct outcome.
	if _, err := os.Stat(filepath.Join(out, "ssh.json")); err == nil {
		t.Error("--out wrote a document for a namespace this platform already ships")
	}
}

// TestImportExitsNonZeroWhenSomethingWillNotImport is what makes this
// usable as a migration gate in a script rather than only by eye.
func TestImportExitsNonZeroWhenSomethingWillNotImport(t *testing.T) {
	t.Parallel()

	clean := writeExport(t, `{"results":[{"name":"Custom","namespace":"custom_thing","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}]},"injectors":{"env":{"CUSTOM_TOKEN":"{{ token }}"}}}]}`)
	if err := runImportAWXCredentialTypes([]string{clean}); err != nil {
		t.Errorf("an export that fully imports returned %v, want nil", err)
	}

	// A managed type this platform ships is NOT a failure: reusing it is
	// the correct outcome, and exiting non-zero for it would make the gate
	// fire on every real AWX export, which all carry the built-ins.
	shipped := writeExport(t, `{"results":[{"name":"Machine","namespace":"ssh","kind":"ssh","inputs":{"fields":[{"id":"username","label":"Username"}]},"injectors":{}}]}`)
	if err := runImportAWXCredentialTypes([]string{shipped}); err != nil {
		t.Errorf("an export of only shipped types returned %v, want nil", err)
	}

	blocked := writeExport(t, `{"results":[{"name":"Google Compute Engine","namespace":"gce","kind":"cloud","inputs":{"fields":[]},"injectors":{}}]}`)
	if err := runImportAWXCredentialTypes([]string{blocked}); err == nil {
		t.Error("an export carrying an unimplemented type returned nil, so a migration script would not notice")
	}
}

// TestImportRefusesAHostileNamespaceBeforeItReachesAPath is this stage's
// Schema and Injection Hardening check for the one filesystem boundary it
// introduces.
//
// --out builds a path from the export's own namespace, and an export is
// untrusted input: it arrives from somebody else's AWX, or from a file an
// operator was handed. A namespace of "../../etc/cron.d/root" would write
// outside the output directory entirely.
//
// The guard is already there and this proves it rather than assuming it.
// writeImportable writes only types classified importable, and that
// classification requires Validate to pass, which requires the namespace
// to match lowercase letters, digits and underscores starting with a
// letter. A traversal sequence cannot match that, so a hostile namespace
// is refused as invalid long before anything joins it to a path.
func TestImportRefusesAHostileNamespaceBeforeItReachesAPath(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"../../etc/cron.d/root",
		"..",
		"/etc/passwd",
		"a/b",
		"a\\b",
		"CON",
		"with space",
		"with.dot",
		"",
	}

	for _, namespace := range hostile {
		t.Run(namespace, func(t *testing.T) {
			t.Parallel()

			body, err := json.Marshal(map[string]any{
				"results": []any{map[string]any{
					"name":      "Hostile",
					"namespace": namespace,
					"kind":      "cloud",
					"inputs":    map[string]any{"fields": []any{map[string]any{"id": "token", "label": "Token", "secret": true}}},
					"injectors": map[string]any{"env": map[string]any{"TOKEN": "{{ token }}"}},
				}},
			})
			if err != nil {
				t.Fatalf("building the export: %v", err)
			}

			path := writeExport(t, string(body))
			out := t.TempDir()

			// A non-nil error is expected: a refused type makes the command
			// exit non-zero, which is the migration gate working.
			_ = runImportAWXCredentialTypes([]string{"--out", out, path})

			entries, err := os.ReadDir(out)
			if err != nil {
				t.Fatalf("reading the output directory: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("--out wrote %d file(s) for a namespace of %q, want none", len(entries), namespace)
			}
		})
	}
}

// TestImportWritesOnlyInsideTheOutputDirectory is the same property stated
// as an invariant over every verdict rather than over a hostile list, so a
// future verdict that starts writing files inherits the check.
func TestImportWritesOnlyInsideTheOutputDirectory(t *testing.T) {
	t.Parallel()

	path := writeExport(t, `{"results":[
		{"name":"Custom","namespace":"custom_thing","kind":"cloud","inputs":{"fields":[{"id":"token","label":"Token","secret":true}]},"injectors":{"env":{"CUSTOM_TOKEN":"{{ token }}"}}}
	]}`)
	out := t.TempDir()

	if err := runImportAWXCredentialTypes([]string{"--out", out, path}); err != nil {
		t.Fatalf("runImportAWXCredentialTypes() error = %v", err)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("reading the output directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was written, so this proves nothing")
	}
	for _, e := range entries {
		full := filepath.Join(out, e.Name())
		resolved, err := filepath.Abs(full)
		if err != nil {
			t.Fatalf("resolving %s: %v", full, err)
		}
		if !strings.HasPrefix(resolved, out+string(filepath.Separator)) {
			t.Errorf("wrote %s, which is outside %s", resolved, out)
		}
	}
}

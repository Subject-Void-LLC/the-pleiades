// Package journal_test: the Schema and Injection Hardening audit, run as
// tests rather than written as prose.
//
// Phase 39's categories are SQL, deserialization, NATS subject
// construction, filesystem paths and auth or token handling. This phase
// adds real surface in three of them: a file name built from a run id, a
// subject built from a job id, and a new table. Each is given a concrete
// adversarial input here, so the audit's verdict is something that fails
// when it stops being true rather than a sentence in a checklist.
package journal_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// hostileStrings are inputs chosen for what each one would do if it
// reached a path, a subject, or a query unescaped.
var hostileStrings = map[string]string{
	"path traversal":      "../../../../etc/passwd",
	"absolute path":       "/etc/shadow",
	"windows traversal":   `..\..\windows\system32`,
	"null byte":           "a\x00b",
	"nats full wildcard":  ">",
	"nats token wildcard": "*",
	"subject separator":   "a.b.c",
	"sql quote":           "' OR '1'='1",
	"sql comment":         "x'; DROP TABLE journal_entries; --",
	"shell metachars":     "$(touch /tmp/pwned)",
	"newline":             "a\nb",
	"very long":           strings.Repeat("a", 4096),
}

func TestHardeningFilesystemPathsRefuseEveryHostileRunID(t *testing.T) {
	// Category: filesystem paths. The run id becomes a file name, so an
	// id that could name something else must be refused rather than
	// escaped. The whitelist is [A-Za-z0-9-], which admits nothing here.
	store, root := newStore(t)
	dir := filepath.Join(root, ".pleiades", "journal")

	for name, hostile := range hostileStrings {
		t.Run(name, func(t *testing.T) {
			err := store.Record(context.Background(), []engine.JournalEntry{entry(hostile, 1, "tasks[0]")})
			if err == nil {
				t.Fatalf("Record accepted the run id %q", hostile)
			}
			// And nothing was created anywhere under the directory, which
			// is the assertion that would catch a refusal that happened
			// after the file was opened.
			found, walkErr := os.ReadDir(dir)
			if walkErr != nil {
				t.Fatalf("reading the journal directory: %v", walkErr)
			}
			if len(found) != 0 {
				t.Errorf("the refused id left %d files behind: %v", len(found), found)
			}
		})
	}
}

func TestHardeningSubjectConstructionCannotBeWidened(t *testing.T) {
	// Category: NATS subject construction, the one FAILURE_PATTERNS.md
	// #63 already records for this codebase: an unvalidated id once
	// widened a subject wildcard into every job's logs.
	//
	// Two layers answer it. internal/runner refuses a dispatch whose job
	// id is not a uuid before it reaches any subject, and SubjectToken
	// encodes anything illegal that got past that.
	root := topology.StreamSubjectRoot[:len(topology.StreamSubjectRoot)-1]
	for name, hostile := range hostileStrings {
		t.Run(name, func(t *testing.T) {
			got := topology.JournalSubject(hostile)
			if !strings.HasPrefix(got, root) {
				t.Errorf("JournalSubject(%q) = %q, which escapes the stream root %q", hostile, got, root)
			}
			if strings.ContainsAny(strings.TrimPrefix(got, "pleiades.jobs.journal."), ".*>") {
				t.Errorf("JournalSubject(%q) = %q, whose final token is not a single literal token", hostile, got)
			}
		})
	}
}

func TestHardeningSQLTreatsAHostileIDAsAValue(t *testing.T) {
	// Category: SQL. The store writes through ent's generated client, so
	// every value is a bound parameter and none is concatenated. The
	// adversarial input is a job id that would drop the table if it were
	// not.
	store, path := newEntStore(t)
	hostile := hostileStrings["sql comment"]

	entry := walkEntry(hostile, "device-1", 0, 1, "tasks[0]")
	if _, err := store.Save(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The table still exists and holds the id verbatim, as data.
	rows := rawQuery(t, path, "SELECT job_id FROM journal_entries")
	if len(rows) != 1 || rows[0] != hostile {
		t.Fatalf("the hostile job id round tripped as %v, want it stored verbatim as a value", rows)
	}
}

func TestHardeningDeserializationSurvivesAHostilePayload(t *testing.T) {
	// Category: deserialization. The consumer decodes whatever arrives on
	// the subject, and a batch it cannot decode must be acknowledged
	// rather than retried forever.
	store, path := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())

	deep := strings.Repeat(`{"entries":[{"x":`, 2000) + "1" + strings.Repeat("}]}", 2000)
	cases := map[string]string{
		"wrong type for entries": `{"entries": "not an array"}`,
		"wrong type inside":      `{"entries": [{"sequence": "not a number"}]}`,
		"deeply nested":          deep,
		"empty object":           `{}`,
		"bare array":             `[]`,
		"not json at all":        `certainly not json`,
		"null":                   `null`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			evt := event.Event{ID: "key-1", Data: json.RawMessage(payload)}
			// It must not panic, and it must not ask for redelivery of
			// something that can never decode.
			if err := sub.Handle(evt); err != nil {
				t.Errorf("Handle asked for redelivery of an undecodable payload: %v", err)
			}
		})
	}
	if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "0" {
		t.Errorf("a hostile payload wrote %v rows", rows)
	}
}

func TestHardeningAHostileLabelIsStoredAsDataNotInterpreted(t *testing.T) {
	// The residual channel this phase documents rather than closes: a
	// runbook author's own task name, register and runbook id are stored
	// as written. The audit question is not whether they can contain
	// anything (they can) but whether storing them can do anything.
	store, path := newEntStore(t)
	e := walkEntry("job-1", "device-1", 0, 1, "tasks[0]")
	e.TaskName = hostileStrings["sql comment"]
	e.Register = hostileStrings["shell metachars"]
	e.DAGID = hostileStrings["nats full wildcard"]

	if _, err := store.Save(context.Background(), []engine.JournalEntry{e}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rows := rawQuery(t, path, "SELECT task_name FROM journal_entries")
	if len(rows) != 1 || rows[0] != e.TaskName {
		t.Fatalf("the hostile task name round tripped as %v, want it stored verbatim", rows)
	}
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		t.Fatal("storing a label executed it")
	}
}

func TestHardeningTheFileSinkNeverInterpretsAnEntry(t *testing.T) {
	// The Crawl sink's own version of the question above: every field
	// reaches the file through encoding/json, so a label carrying a
	// newline or a quote produces one escaped JSON line rather than two
	// lines or a broken one.
	store, root := newStore(t)
	e := entry("run-a", 1, "tasks[0]")
	e.TaskName = "first\nsecond\"third"
	e.Register = hostileStrings["null byte"]

	if err := store.Record(context.Background(), []engine.JournalEntry{e}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	path := filepath.Join(root, ".pleiades", "journal", "run-a.jsonl")
	data, err := os.ReadFile(path) // #nosec G304 -- a path this test built from its own t.TempDir
	if err != nil {
		t.Fatalf("reading the file: %v", err)
	}
	if lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n"); lines != 0 {
		t.Errorf("one entry produced %d extra lines: a label broke the JSON Lines framing:\n%s", lines, data)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimRight(string(data), "\n")), &decoded); err != nil {
		t.Fatalf("the written line is not valid JSON: %v\n%s", err, data)
	}
	if decoded["task_name"] != e.TaskName {
		t.Errorf("task_name round tripped as %q, want %q", decoded["task_name"], e.TaskName)
	}
}

// The layered answer, recorded rather than re-tested here. The sink's
// whitelist is the last line of defence, not the only one, and the two
// lines above it are already proven elsewhere:
//
//   - internal/runner refuses a dispatch whose job id is not a uuid,
//     before that id reaches any subject at all (agent_handle.go's
//     uuid.Parse guard, which Terms the message rather than retrying it).
//   - The engine mints RunID itself, on unexported per-call state, so no
//     caller supplies one. That it mints something this whitelist admits
//     is proven end to end by cmd/pleiades's own Release Gate: the run
//     id IS the file name, so an illegal one would fail Record, leave no
//     file, and fail that test at its first read.
//
// Writing a unit test for it here would need an exported minting
// function that exists only to be tested, which is a worse trade than
// the sentence above.

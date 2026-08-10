package legacy_test

import (
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// readFixture loads a real captured ansible-playbook -v run from
// testdata, produced by actually running ansible-core 2.19.11 in this
// repository's own development environment (RULE 0: this parser is
// tested against real Ansible output, not a hand-written guess of its
// shape).
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", name, err)
	}
	return data
}

var fixedNow = time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)

// TestParseStdout_RealPlaybookRun proves ParseStdout translates a real,
// two-host, three-task ansible-playbook -v run (one task using `debug`
// with a "msg" field, one `command` task that changes, one `command` task
// that fails but is ignore_errors'd) into the exact ordered event
// sequence a reader of that transcript would expect, including
// tolerating the interleaved [WARNING] diagnostic lines real Ansible
// prints between the TASK header and the first result line.
func TestParseStdout_RealPlaybookRun(t *testing.T) {
	output := readFixture(t, "real_playbook_run.txt")
	events := legacy.ParseStdout(output, fixedNow)

	want := []wire.JobEvent{
		{Status: "ok", Host: "hostA", Task: "say hello"},
		{Status: "ok", Host: "hostB", Task: "say hello"},
		{Status: "changed", Host: "hostA", Task: "a task that changes"},
		{Status: "changed", Host: "hostB", Task: "a task that changes"},
		{Status: "failed", Host: "hostA", Task: "a task that fails"},
		{Status: "failed", Host: "hostB", Task: "a task that fails"},
		{Status: "changed", Host: "hostA", Task: "task.completed"},
		{Status: "changed", Host: "hostB", Task: "task.completed"},
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		got := events[i]
		if got.Status != w.Status || got.Host != w.Host || got.Task != w.Task {
			t.Errorf("event %d = {Status:%q Host:%q Task:%q}, want {Status:%q Host:%q Task:%q}",
				i, got.Status, got.Host, got.Task, w.Status, w.Host, w.Task)
		}
		if got.Timestamp != fixedNow.UTC().Format(time.RFC3339) {
			t.Errorf("event %d Timestamp = %q, want the injected now", i, got.Timestamp)
		}
	}

	// The debug task's own "msg" field is the whole point of preserving
	// hostvars/capabilities as a task can read them back: assert the
	// exact message text made it through, not just that some message
	// exists.
	if got, want := events[0].EventData.Message, "hello from hostA"; got != want {
		t.Errorf("say hello/hostA message = %q, want %q", got, want)
	}
	if got, want := events[1].EventData.Message, "hello from hostB"; got != want {
		t.Errorf("say hello/hostB message = %q, want %q", got, want)
	}

	// The failing task's real "msg" field ("non-zero return code") must
	// also survive, proving decodeTrailingJSON's single-line path (this
	// result is not pretty-printed, unlike debug's).
	if got, want := events[4].EventData.Message, "non-zero return code"; got != want {
		t.Errorf("fail task/hostA message = %q, want %q", got, want)
	}

	// The final per-host summary must report the real recap counts, not
	// a fabricated one.
	wantSummary := "ok=3 changed=2 unreachable=0 failed=0 skipped=0 rescued=0 ignored=1"
	if got := events[6].EventData.Message; got != wantSummary {
		t.Errorf("hostA task.completed message = %q, want %q", got, wantSummary)
	}
}

// TestParseStdout_SkippedTask proves a "skipping:" result line (an
// Ansible-native word) translates to the closed "ok" status, never
// leaking the word "skipping" into wire.JobEvent.Status -- the concrete,
// checkable Anti-Corruption Layer claim this package's own Pattern Entry
// Gate makes.
func TestParseStdout_SkippedTask(t *testing.T) {
	output := readFixture(t, "real_playbook_run_skip.txt")
	events := legacy.ParseStdout(output, fixedNow)

	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (2 skip results + 2 recap): %+v", len(events), events)
	}
	for _, evt := range events {
		if evt.Status == "skipping" {
			t.Errorf("event leaked raw Ansible vocabulary %q into Status", evt.Status)
		}
	}
	if events[0].Status != "ok" || events[0].Host != "hostA" || events[0].Task != "conditionally skipped" {
		t.Errorf("events[0] = %+v, want ok/hostA/conditionally skipped", events[0])
	}
	// No host failed or changed anything, so the recap status must be "ok".
	if events[2].Status != "ok" || events[3].Status != "ok" {
		t.Errorf("recap events = %+v, want both ok", events[2:])
	}
}

// TestParseStdout_HardFailureWithErrorBlock proves the parser tolerates
// Ansible's multi-line "[ERROR]: Task failed..." diagnostic block (an
// "Origin:" line, a blank line, numbered source-context lines, and a
// caret pointer line), which real Ansible prints before a fatal result
// when the failing task carries no ignore_errors. None of those lines
// must be mistaken for a task result or corrupt the parser's line
// position for the real fatal: lines that follow.
func TestParseStdout_HardFailureWithErrorBlock(t *testing.T) {
	output := readFixture(t, "real_playbook_run_fail_noignore.txt")
	events := legacy.ParseStdout(output, fixedNow)

	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (2 fatal results + 2 recap): %+v", len(events), events)
	}
	for _, evt := range events {
		if evt.Task != "a task that fails hard" && evt.Task != "task.completed" {
			t.Errorf("event carries unexpected Task %q, the [ERROR] block's own text may have leaked in: %+v", evt.Task, evt)
		}
		if evt.Status != "failed" {
			t.Errorf("event Status = %q, want failed: %+v", evt.Status, evt)
		}
	}
}

// TestParseStdout_GenericUnreachableAndFailedPrefixes proves the
// "unreachable:"/"failed:" result-line prefixes (distinct from the
// "fatal: [host]: FAILED!/UNREACHABLE! =>" shape real ansible-playbook
// actually emits, which this package's other tests already cover against
// real captured output) also map into the closed "failed" status.
// Synthetic, not a real capture: this exact literal prefix shape is
// vocabulary Ansible's own result-line grammar defines but this
// package's other real fixtures never happened to exercise, the same
// defensive-completeness reasoning FuzzParseStdout's own hand-crafted
// seeds already apply elsewhere in this file.
func TestParseStdout_GenericUnreachableAndFailedPrefixes(t *testing.T) {
	output := "TASK [x] ***\nunreachable: [hostA] => {}\nfailed: [hostB] => {}\n"
	events := legacy.ParseStdout([]byte(output), fixedNow)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	for _, evt := range events {
		if evt.Status != "failed" {
			t.Errorf("event = %+v, want Status=failed", evt)
		}
	}
}

// TestParseStdout_EmptyInput proves an empty (or non-Ansible) input
// produces no events and never panics, rather than requiring every
// caller to special-case a zero-length ContainerResult.Output.
func TestParseStdout_EmptyInput(t *testing.T) {
	if events := legacy.ParseStdout(nil, fixedNow); len(events) != 0 {
		t.Errorf("got %d events for empty input, want 0: %+v", len(events), events)
	}
	if events := legacy.ParseStdout([]byte("not ansible output at all\njust some text\n"), fixedNow); len(events) != 0 {
		t.Errorf("got %d events for non-Ansible input, want 0: %+v", len(events), events)
	}
}

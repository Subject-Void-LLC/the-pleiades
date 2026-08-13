//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// TestGrandIntegration_EachKindReachesItsOwnAdapter is the adapter
// routing release gate: one runbook template and one playbook template,
// launched through the real API against the same inventory, each reaching
// a different execution engine through the production binaries.
//
// It exists because every layer of this path was once green while the
// path as a whole did not exist (FAILURE_PATTERNS.md #112). The routing
// package had tests, the legacy adapter had a release gate that composed
// it by hand, the kind travelled the wire, and no binary composed any of
// it: the runner built only the native adapter, the worker resolved every
// job through the runbook source, and the two playbook grammars were
// disjoint. This test is the one that cannot pass unless the composition
// itself is real, which is why it drives cmd/controller and cmd/runner as
// built binaries rather than composing anything in-process.
func TestGrandIntegration_EachKindReachesItsOwnAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the adapter routing gate in short mode")
	}

	h := startHarness(t, withAnsible())

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	adminToken := issuer.Token(t, &auth.Identity{Subject: "e2e-admin", Role: auth.RoleAdmin})

	// The runbook half: the same launch the grand integration test makes,
	// asserted here only as the control arm.
	status, body := h.launchTemplate(t, adminToken, h.templateID)
	if status != http.StatusAccepted {
		t.Fatalf("runbook launch returned %d, want 202. Body: %s\n%s", status, body, h.controller.output())
	}
	runbookJobID := requireStringField(t, body, "job_id")

	// The playbook half.
	status, body = h.launchTemplate(t, adminToken, h.playbookTemplateID)
	if status != http.StatusAccepted {
		t.Fatalf("playbook launch returned %d, want 202. Body: %s\n%s", status, body, h.controller.output())
	}
	playbookJobID := requireStringField(t, body, "job_id")

	// Both fan-outs complete: the playbook job must survive the
	// Controller's own worker, which used to resolve every job through
	// the runbook source and would have failed this one as "runbook not
	// found" before the Runner was ever consulted.
	runbookJob := h.pollJobUntilTerminal(t, adminToken, runbookJobID)
	playbookJob := h.pollJobUntilTerminal(t, adminToken, playbookJobID)

	for _, tc := range []struct {
		job  jobResponse
		kind string
	}{{runbookJob, "runbook"}, {playbookJob, "playbook"}} {
		if tc.job.State != "completed" {
			t.Fatalf("the %s job ended %q, want completed\n%s",
				tc.kind, tc.job.State, h.controller.output())
		}
		if tc.job.Kind != tc.kind {
			t.Errorf("the %s job's API view carries kind %q", tc.kind, tc.job.Kind)
		}
		if tc.job.Dispatched != 2 {
			t.Errorf("the %s job dispatched %d devices, want the inventory's 2", tc.kind, tc.job.Dispatched)
		}
	}

	// The execution evidence, read off each job's own log subject. The
	// native engine announces itself in its completion message ("native
	// execution finished successfully"); the legacy adapter reports what
	// a real ansible-playbook printed, parsed from its stdout, which is
	// where the fixture's own task name surfaces. Each marker appearing
	// under its own job, and only its own, is what "reached a different
	// adapter" observably means.
	const nativeMarker = "native execution"
	runbookLogs := h.collectJobLogs(t, runbookJobID, nativeMarker)
	playbookLogs := h.collectJobLogs(t, playbookJobID, harnessPlaybookTaskName)

	if strings.Contains(playbookLogs, nativeMarker) {
		t.Error("the playbook job's log stream carries native-engine output, so its dispatches reached the wrong adapter")
	}
	if strings.Contains(runbookLogs, harnessPlaybookTaskName) {
		t.Error("the runbook job's log stream carries the ansible fixture's task, so its dispatches reached the wrong adapter")
	}
}

// collectJobLogs reads jobID's log subject until an event mentioning
// marker arrives, returning everything read; it fails the test if the
// marker never shows.
//
// A marker rather than a count, unlike assertJobLogEvents, because the
// two engines publish different event volumes: the native engine two per
// device, ansible one per parsed stdout line per device.
func (h *harness) collectJobLogs(t *testing.T, jobID, marker string) string {
	t.Helper()

	consumer, err := h.js.CreateOrUpdateConsumer(context.Background(), topology.StreamName,
		topology.LogViewerConsumerConfig(jobID))
	if err != nil {
		t.Fatalf("creating the log viewer consumer: %v", err)
	}

	var all strings.Builder
	deadline := time.Now().Add(90 * time.Second * raceTimeScale)
	for time.Now().Before(deadline) {
		batch, err := consumer.Fetch(16, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("fetching job log events: %v", err)
		}
		for msg := range batch.Messages() {
			var envelope event.Event
			if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
				t.Fatalf("decoding the log envelope: %v", err)
			}
			var evt wire.JobEvent
			if err := json.Unmarshal(envelope.Data, &evt); err != nil {
				t.Fatalf("decoding the job event: %v", err)
			}
			all.WriteString(evt.Task + "\n" + evt.EventData.Message + "\n")
			_ = msg.Ack()
		}
		if strings.Contains(all.String(), marker) {
			return all.String()
		}
	}

	t.Fatalf("job %s's log stream never mentioned %q; read so far:\n%s\nrunner output:\n%s",
		jobID, marker, all.String(), h.runner.output())
	return ""
}

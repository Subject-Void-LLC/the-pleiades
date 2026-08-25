//go:build integration

// This file holds the Grand Integration Test's assertions.
//
// Every helper's doc comment states what would have to break for it to
// fail, which is the standard this phase's Adversarial Pattern
// Justification gate sets: an assertion that cannot fail is not a test,
// and the previous version of this file counted messages without reading
// them, which could barely fail at all.
package e2e

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// openReadBackClient opens a second client against the same database for
// reading results back.
//
// PostgreSQL handles concurrent connections natively, so unlike the
// SQLite path this needs no ordering dance with the controller's own
// client.
func (h *harness) openReadBackClient(t *testing.T) *ent.Client {
	t.Helper()
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: h.dsn})
	if err != nil {
		t.Fatalf("opening a client to read results back: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// assertNoJobsPersisted proves no job row exists.
//
// Breaks when: a rejected request still reaches the dispatch handler, so
// authentication stops short-circuiting. A 401 status on its own proves
// only that a number was written to a response; this is what proves
// nothing happened behind it.
func (h *harness) assertNoJobsPersisted(t *testing.T) {
	t.Helper()
	client := h.openReadBackClient(t)
	count, err := client.Job.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("counting jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d job rows exist after only rejected requests, so a rejected request reached the handler", count)
	}
}

// assertJobIDIsServerMinted proves the job identifier is a real UUID the
// server generated.
//
// Breaks when: the identifier becomes caller-derived. That matters beyond
// tidiness, because the job identifier is interpolated into a NATS
// subject for the log stream, so a caller-controlled value would be a
// subject injection boundary. FAILURE_PATTERNS.md #18 records the one
// time a comparable identifier really did reach a subject unvalidated.
func (h *harness) assertJobIDIsServerMinted(t *testing.T, jobID string) {
	t.Helper()
	if _, err := uuid.Parse(jobID); err != nil {
		t.Fatalf("job id %q is not a UUID, so it is not server minted: %v", jobID, err)
	}
}

// assertJobView checks the API's own rendering of the finished job.
//
// Breaks when: group filtering regresses (dispatched would read 4, since
// rtr3 and rtr4 are eligible in every way except membership), the
// missing-host skip branch stops firing or changes its wording, or the
// tallies stop being stamped at completion.
//
// The three tallies are deliberately three different numbers, so a bug
// that reported one value for all three cannot pass.
func (h *harness) assertJobView(t *testing.T, job jobResponse, jobID string) {
	t.Helper()

	if job.State != "completed" {
		t.Fatalf("job state = %q, want completed\n%s", job.State, h.controller.output())
	}
	if job.JobID != jobID {
		t.Fatalf("job view reports id %q, want %q", job.JobID, jobID)
	}
	if job.RunbookID != harnessRunbookID {
		t.Fatalf("job runbook_id = %q, want %q", job.RunbookID, harnessRunbookID)
	}
	// What the job says it came from. This is the property only a real mesh
	// can prove: the tenant was never submitted by the caller, it was
	// derived from the template's inventory when the job row was written,
	// and Job.organization_id had no writer at all before this phase.
	if job.Template != h.templateID {
		t.Fatalf("job template = %d, want the seeded %d", job.Template, h.templateID)
	}
	if job.TemplateName != "the grand integration test" {
		t.Fatalf("job template_name = %q, want the seeded template's own", job.TemplateName)
	}
	if job.Organization == 0 {
		t.Fatal("the job belongs to no organization, so a dispatch is untenanted: this is the column Phase 21 gave its first writer")
	}
	if job.Inventory == 0 {
		t.Fatal("the job names no inventory, so nothing records what it targeted")
	}
	if job.Kind != "runbook" {
		t.Fatalf("job kind = %q, want runbook: the kind is what the Runner routes on", job.Kind)
	}
	if job.Dispatched != 2 || job.Skipped != 1 || job.Failed != 0 {
		t.Fatalf("job tallies are dispatched=%d skipped=%d failed=%d, want 2/1/0. Tasks: %s",
			job.Dispatched, job.Skipped, job.Failed, describeTasks(job))
	}
	if len(job.Tasks) != 3 {
		t.Fatalf("job has %d tasks, want 3: %s", len(job.Tasks), describeTasks(job))
	}

	byName := map[string]string{}
	reasons := map[string]string{}
	for _, task := range job.Tasks {
		byName[task.DeviceName] = task.Outcome
		reasons[task.DeviceName] = task.Reason
	}

	for _, name := range []string{"rtr1", "rtr2"} {
		if byName[name] != "dispatched" {
			t.Fatalf("device %s outcome = %q, want dispatched: %s", name, byName[name], describeTasks(job))
		}
		if reasons[name] != "" {
			t.Fatalf("device %s was dispatched but carries the reason %q", name, reasons[name])
		}
	}
	if byName["rtr5"] != "skipped" {
		t.Fatalf("device rtr5 outcome = %q, want skipped: %s", byName["rtr5"], describeTasks(job))
	}
	// The exact wording, because a silent change to it would make the
	// reason field useless to an operator debugging a skip.
	if want := `device "rtr5" has no host property`; reasons["rtr5"] != want {
		t.Fatalf("device rtr5 skip reason = %q, want %q", reasons["rtr5"], want)
	}

	// The devices in the untargeted group must appear nowhere at all.
	for _, name := range []string{"rtr3", "rtr4"} {
		if _, present := byName[name]; present {
			t.Fatalf("device %s is in group %q but appears in a job targeting %q, so group filtering is not applied: %s",
				name, untargetGroup, targetGroup, describeTasks(job))
		}
	}
}

// assertJobInDatabase reads the finished job straight out of PostgreSQL.
//
// Breaks when: the API view and the stored row disagree. The API view
// alone could be internally consistent and still wrong, which is exactly
// the gap this closes, and reading the database back is what the Release
// Gate's own wording asks for.
func (h *harness) assertJobInDatabase(t *testing.T, jobID string) {
	t.Helper()
	ctx := context.Background()
	client := h.openReadBackClient(t)

	row, err := client.Job.Query().Where(job.JobIDEQ(jobID)).WithTasks().Only(ctx)
	if err != nil {
		t.Fatalf("reading job %s back: %v", jobID, err)
	}

	if row.State.String() != "completed" {
		t.Fatalf("stored state = %q, want completed", row.State)
	}
	if row.DispatchedCount != 2 || row.SkippedCount != 1 || row.FailedCount != 0 {
		t.Fatalf("stored tallies are %d/%d/%d, want 2/1/0", row.DispatchedCount, row.SkippedCount, row.FailedCount)
	}
	if row.Actor != "e2e-admin" {
		t.Fatalf("stored actor = %q, want the token subject %q", row.Actor, "e2e-admin")
	}
	if row.FailureReason != "" {
		t.Fatalf("a completed job stored the failure reason %q", row.FailureReason)
	}
	// BeginFanOut bumps the fencing token when it claims the job. A zero
	// fence would mean the guard that stops two workers fanning out the
	// same job never armed.
	if row.Fence < 1 {
		t.Fatalf("stored fence = %d, want at least 1", row.Fence)
	}

	if len(row.Edges.Tasks) != 3 {
		t.Fatalf("stored task rows = %d, want 3", len(row.Edges.Tasks))
	}
	// Every stored task must name the identifier the database generated
	// at seed time, not the display name.
	seeded := map[string]string{}
	for _, d := range h.devices {
		seeded[d.name] = d.deviceID
	}
	for _, task := range row.Edges.Tasks {
		want, known := seeded[task.DeviceName]
		if !known {
			t.Fatalf("stored task names the unknown device %q", task.DeviceName)
		}
		if task.DeviceID != want {
			t.Fatalf("stored task for %s carries device_id %q, want the seeded %q", task.DeviceName, task.DeviceID, want)
		}
	}

	// Exactly one job overall, which is what proves the two rejected
	// requests earlier created nothing.
	total, err := client.Job.Query().Count(ctx)
	if err != nil {
		t.Fatalf("counting jobs: %v", err)
	}
	if total != 1 {
		t.Fatalf("%d job rows exist, want exactly 1", total)
	}
}

// assertPropertiesEncryptedAtRest proves device properties are ciphertext
// in the database.
//
// Breaks when: the envelope hook stops firing on write, which is a real
// plaintext leak. It is asserted with a raw query rather than through ent
// precisely because ent's interceptor would decrypt the value on the way
// out and hide the very thing being checked.
//
// It also proves something the rest of this test depends on: the seeder
// encrypted these rows, and the controller, a different process, read
// them back and resolved a host from them. That only works if the key
// really crossed the process boundary intact.
func (h *harness) assertPropertiesEncryptedAtRest(t *testing.T) {
	t.Helper()

	db, err := stdsql.Open("postgres", h.dsn)
	if err != nil {
		t.Fatalf("opening a raw connection: %v", err)
	}
	defer db.Close()

	// A literal query with no interpolation of any kind.
	var stored string
	err = db.QueryRowContext(context.Background(),
		"SELECT properties FROM devices WHERE name = 'rtr1'").Scan(&stored)
	if err != nil {
		t.Fatalf("reading rtr1 properties raw: %v", err)
	}

	if strings.Contains(stored, "10.0.0.1") {
		t.Fatalf("rtr1's management address is stored in plaintext: %s", stored)
	}
	if !strings.Contains(stored, crypto.EncryptedKeyMarker) {
		t.Fatalf("rtr1's properties carry no encryption marker, so the envelope hook did not fire: %s", stored)
	}
}

// observeDispatches subscribes to the per-device dispatch subject.
//
// It must be called before the launch. It deliberately creates a fresh
// ephemeral consumer rather than reusing topology.DispatchConsumerConfig,
// which is the durable group the real Runner pulls from: joining that
// group would make this test steal messages from the process under test.
func (h *harness) observeDispatches(t *testing.T) jetstream.Consumer {
	t.Helper()
	consumer, err := h.js.CreateOrUpdateConsumer(context.Background(), topology.StreamName, jetstream.ConsumerConfig{
		// Every device's dispatch, which is what the fleet consumer sees.
		FilterSubject: topology.DispatchSubjectAll(),
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	if err != nil {
		t.Fatalf("creating the dispatch observer: %v", err)
	}
	return consumer
}

// assertDispatchPayloads decodes every per-device dispatch message and
// checks it field by field against what was seeded.
//
// Breaks when: any field is populated from the wrong accessor. The
// device identifier check is the sharpest one, because pkg/wire's own doc
// comment records that this exact field was once filled from ID() where
// Name() was meant, and comparing against the database is the only way to
// catch that returning. The host is checked as a pairing rather than as a
// set, so a bug that shuffled hosts between devices cannot pass.
func (h *harness) assertDispatchPayloads(t *testing.T, consumer jetstream.Consumer, jobID string) {
	t.Helper()

	// Ask for one more than expected, so "and no more than these" is part
	// of the claim rather than an assumption.
	const want = 2
	payloads := map[string]wire.DispatchPayload{}
	envelopes := map[string]event.Event{}

	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	for len(payloads) < want+1 && time.Now().Before(deadline) {
		batch, err := consumer.Fetch(want+1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("fetching dispatch messages: %v", err)
		}
		got := 0
		for msg := range batch.Messages() {
			got++
			var envelope event.Event
			if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
				t.Fatalf("decoding the dispatch envelope: %v", err)
			}
			var payload wire.DispatchPayload
			if err := json.Unmarshal(envelope.Data, &payload); err != nil {
				t.Fatalf("decoding the dispatch payload: %v", err)
			}
			payloads[payload.DeviceName] = payload
			envelopes[payload.DeviceName] = envelope
		}
		if got == 0 && len(payloads) >= want {
			break
		}
	}

	if len(payloads) != want {
		names := make([]string, 0, len(payloads))
		for name := range payloads {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Fatalf("observed %d dispatch payloads (%v), want exactly %d", len(payloads), names, want)
	}

	seeded := map[string]*seededDevice{}
	for _, d := range h.devices {
		seeded[d.name] = d
	}

	for _, name := range []string{"rtr1", "rtr2"} {
		payload, ok := payloads[name]
		if !ok {
			t.Fatalf("no dispatch payload for %s", name)
		}
		expect := seeded[name]

		if payload.JobID != jobID {
			t.Fatalf("%s payload job_id = %q, want %q", name, payload.JobID, jobID)
		}
		if payload.RunbookID != harnessRunbookID {
			t.Fatalf("%s payload runbook_id = %q, want %q", name, payload.RunbookID, harnessRunbookID)
		}
		if payload.DeviceID != expect.deviceID {
			t.Fatalf("%s payload device_id = %q, want the seeded identifier %q; a payload carrying the display name here is the exact regression pkg/wire documents",
				name, payload.DeviceID, expect.deviceID)
		}
		if payload.DeviceHost != expect.host {
			t.Fatalf("%s payload device_host = %q, want %q; hosts are checked paired to their device so a shuffle cannot pass",
				name, payload.DeviceHost, expect.host)
		}
		// The runbook fixture declares no metadata block, and the nil
		// case means interruptible.
		if !payload.Interruptible {
			t.Fatalf("%s payload is not interruptible, but the runbook declares no metadata block", name)
		}
		// cisco_router implements the SSH transport capability with a
		// default port, so a zero here means the type assertion that
		// carries it onto the wire stopped matching.
		if payload.SSHPort != 22 {
			t.Fatalf("%s payload ssh_port = %d, want 22", name, payload.SSHPort)
		}
		// No credential store is configured, so nothing may appear here.
		// This is a leak assertion, not a shape one.
		if len(payload.Secrets) != 0 {
			t.Fatalf("%s payload carries %d secrets, want none", name, len(payload.Secrets))
		}
		if len(payload.Tags) != 0 {
			t.Fatalf("%s payload carries tags %v, but none were seeded", name, payload.Tags)
		}

		// Capabilities are compared as a set, deliberately. The record
		// type builds its slice by ranging a map, so the order is
		// genuinely non-deterministic for a database-hydrated device.
		// Do not "fix" this into a slice comparison.
		wantCaps := map[capability.Name]bool{
			capability.NameSSHTransport: true,
			capability.NameCiscoIOS:     true,
			// Phase 73 gave cisco.Router an IPAddress() accessor so
			// pleiades.builtin.wait.port could dispatch against a real
			// device at all, which makes every router NetworkAddressable
			// and is deliberate: internal/archtest's own sweep records
			// that this one was "fixed for real instead of allowlisted".
			// This expectation was not updated with it, so the Grand
			// Integration Test has been failing on main ever since.
			capability.NameNetworkAddressable: true,
		}
		if len(payload.Capabilities) != len(wantCaps) {
			t.Fatalf("%s payload capabilities = %v, want the set %v", name, payload.Capabilities, wantCaps)
		}
		for _, got := range payload.Capabilities {
			if !wantCaps[got] {
				t.Fatalf("%s payload carries the unexpected capability %q", name, got)
			}
		}

		envelope := envelopes[name]
		if envelope.Type != "runbook.dispatched" {
			t.Fatalf("%s envelope type = %q, want runbook.dispatched", name, envelope.Type)
		}
		if envelope.Actor != "e2e-admin" {
			t.Fatalf("%s envelope actor = %q, want the token subject", name, envelope.Actor)
		}
		// The idempotency key is what stops a redelivery dispatching the
		// same device twice.
		if wantKey := jobID + ":" + expect.deviceID; envelope.IdempotencyKey != wantKey {
			t.Fatalf("%s envelope idempotency key = %q, want %q", name, envelope.IdempotencyKey, wantKey)
		}
	}

	// The untargeted devices must not appear on the wire either.
	for _, name := range []string{"rtr3", "rtr4", "rtr5"} {
		if _, present := payloads[name]; present {
			t.Fatalf("device %s was dispatched to but should not have been", name)
		}
	}
}

// assertJobLogEvents proves the Runner process actually executed.
//
// Breaks when: the Runner never runs (the controller publishing alone
// would satisfy every earlier assertion), the execution adapter changes
// its event contract, or the two binaries disagree about the runbook
// directory, since the Runner resolves the runbook itself.
//
// Ordering is asserted per host only. Both devices publish to the same
// subject from a concurrent worker pool, so the interleaving between them
// is genuinely not deterministic, and asserting a global order would be
// asserting something the system never promised.
func (h *harness) assertJobLogEvents(t *testing.T, jobID string) {
	t.Helper()

	consumer, err := h.js.CreateOrUpdateConsumer(context.Background(), topology.StreamName,
		topology.LogViewerConsumerConfig(jobID))
	if err != nil {
		t.Fatalf("creating the log viewer consumer: %v", err)
	}

	// Two devices, two events each.
	const want = 4
	byHost := map[string][]wire.JobEvent{}
	total := 0

	deadline := time.Now().Add(45 * time.Second * raceTimeScale)
	for total < want && time.Now().Before(deadline) {
		batch, err := consumer.Fetch(want, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("fetching job log events: %v", err)
		}
		for msg := range batch.Messages() {
			var envelope event.Event
			if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
				t.Fatalf("decoding the log envelope: %v", err)
			}
			if envelope.Type != "job.log" {
				t.Fatalf("log envelope type = %q, want job.log", envelope.Type)
			}
			var evt wire.JobEvent
			if err := json.Unmarshal(envelope.Data, &evt); err != nil {
				t.Fatalf("decoding the job event: %v", err)
			}
			byHost[evt.Host] = append(byHost[evt.Host], evt)
			total++
		}
	}

	if total != want {
		t.Fatalf("observed %d job log events, want %d; the runner may not have executed\n%s",
			total, want, h.runner.output())
	}

	for _, name := range []string{"rtr1", "rtr2"} {
		var host string
		for _, d := range h.devices {
			if d.name == name {
				host = d.host
			}
		}
		events := byHost[host]
		if len(events) != 2 {
			t.Fatalf("host %s (%s) produced %d events, want 2", name, host, len(events))
		}

		started, completed := events[0], events[1]
		if started.Status != "started" {
			t.Fatalf("%s first event status = %q, want started", name, started.Status)
		}
		if started.Task != "runbook:"+harnessRunbookID {
			t.Fatalf("%s first event task = %q, want runbook:%s", name, started.Task, harnessRunbookID)
		}
		if completed.Task != "task.completed" {
			t.Fatalf("%s second event task = %q, want task.completed", name, completed.Task)
		}
		// "ok" and not "changed": the builtin noop reports no change, and
		// a noop that started reporting one would be a real convergence
		// bug rather than a cosmetic one.
		if completed.Status != "ok" {
			t.Fatalf("%s completion status = %q, want ok. Message: %s", name, completed.Status, completed.EventData.Message)
		}
	}
}

// assertNoDeadLetters proves nothing failed its way through the Runner's
// full redelivery budget.
//
// Breaks when: execution fails repeatedly. Without this, a test could see
// its four log events and still be sitting on top of a mesh that was
// dead-lettering other work.
func (h *harness) assertNoDeadLetters(t *testing.T) {
	t.Helper()

	subject := topology.DeadLetterSubject(topology.DispatchSubjectAll())
	consumer, err := h.js.CreateOrUpdateConsumer(context.Background(), topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: subject,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	if err != nil {
		t.Fatalf("creating the dead letter observer: %v", err)
	}

	batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetching dead letters: %v", err)
	}
	for msg := range batch.Messages() {
		t.Fatalf("a message was dead lettered on %s: %s", subject, msg.Data())
	}
}

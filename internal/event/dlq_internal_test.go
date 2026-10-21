// Package event: a dead letter's contents, without a broker.
package event

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

// stubMsg is a delivered message with only its subject and body; any other
// method panics through the nil interface, which a test would show.
type stubMsg struct {
	jetstream.Msg
	subject string
	data    []byte
}

func (m stubMsg) Subject() string { return m.subject }
func (m stubMsg) Data() []byte    { return m.data }

// TestDeadLetter_KeepsIdentifiersOnly: the Event's identifiers come across,
// its data does not, whatever field holds it, and the reason is masked by
// shape before it leaves the process.
func TestDeadLetter_KeepsIdentifiersOnly(t *testing.T) {
	const sentinel = "sentinel-5b1e-never-in-a-dead-letter"
	body, err := json.Marshal(Event{
		ID: "evt-1", Type: "runbook.dispatched", CorrelationID: "corr-1", CausationID: "cause-1",
		Actor: "alice", TraceID: "trace-1", IdempotencyKey: "job-1:rtr1",
		Data: json.RawMessage(`{"secrets":{"password":"` + sentinel + `"},"fields":{"a":"` + sentinel + `"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	d := deadLetter(stubMsg{subject: "pleiades.jobs.dispatch.rtr1", data: body}, 5, "device said: Authorization: Bearer "+jwt)

	if d.EventID != "evt-1" || d.EventType != "runbook.dispatched" || d.CorrelationID != "corr-1" ||
		d.IdempotencyKey != "job-1:rtr1" || d.Actor != "alice" || d.OriginalSubject != "pleiades.jobs.dispatch.rtr1" || d.NumDelivered != 5 {
		t.Errorf("identifiers %+v", d)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), sentinel) {
		t.Errorf("the dead letter copies the event's data: %s", out)
	}
	for _, kept := range []string{"cause-1", "trace-1"} {
		if strings.Contains(string(out), kept) {
			t.Errorf("the dead letter holds %q, which is not on its allowlist: %s", kept, out)
		}
	}
	if strings.Contains(d.Reason, jwt) {
		t.Errorf("the reason was not masked: %q", d.Reason)
	}
}

// TestDeadLetter_ANonEventKeepsNothingOfIt: a body that is not an Event
// gives no identifiers and is not copied.
func TestDeadLetter_ANonEventKeepsNothingOfIt(t *testing.T) {
	d := deadLetter(stubMsg{subject: "s", data: []byte("not json, password=hunter2hunter2")}, 1, "bad")
	out, _ := json.Marshal(d)
	if d.EventID != "" || d.IdempotencyKey != "" || strings.Contains(string(out), "hunter2") {
		t.Errorf("dead letter %s", out)
	}
}

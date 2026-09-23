// Proving, against a real broker, that mesh renewal leaves nothing behind.
//
// This is a container test rather than a string comparison on purpose.
// Whether a subject is captured by a stream is a decision the SERVER
// makes from the stream's configured subject filter, and asserting it by
// reading the filter in Go is asserting our own reading of NATS wildcard
// rules. The rule in question is the one this repository has already been
// wrong about once, in the opposite direction: ">" matches one or more
// tokens, never zero, which Phase 101a measured after a grant had been
// built assuming otherwise.
package topology_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestMeshRenewalIsNeverStoredOnTheStream is the guard on the hyphen in
// meshRenewSubject.
//
// Renaming it to "pleiades.mesh.renew", which reads better and is the
// obvious tidy-up, puts every renewal request in the fleet onto the
// dispatch stream for the outage budget's retention window. This fails
// when that happens, and it fails with the count rather than with a
// string comparison, so the message says what actually went wrong.
func TestMeshRenewalIsNeverStoredOnTheStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	nc, err := nats.Connect(testsupport.StartNATS(t).URL())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.CreateStream(ctx, topology.StreamConfig(topology.DefaultOutageBudget))
	if err != nil {
		t.Fatalf("provisioning the stream: %v", err)
	}

	msgs := func(when string) uint64 {
		t.Helper()
		info, err := stream.Info(ctx)
		if err != nil {
			t.Fatalf("stream info %s: %v", when, err)
		}
		return info.State.Msgs
	}

	start := msgs("at the start")

	// ---- The positive control, which has to come first. ----
	//
	// Without it, "the renewal was not stored" is satisfied just as well
	// by a stream that stores nothing at all, or by a publish that never
	// reached the server.
	if err := nc.Publish(topology.DispatchSubject("control-device"), []byte("a dispatch")); err != nil {
		t.Fatalf("publishing the control dispatch: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flushing the control dispatch: %v", err)
	}
	if got := msgs("after the control dispatch"); got != start+1 {
		t.Fatalf("the control dispatch did not reach the stream (%d then %d), so this test cannot tell storage from silence", start, got)
	}
	afterControl := msgs("after the control")

	// ---- The claim: a renewal request is not captured. ----
	if err := nc.Publish(topology.MeshRenewSubject(), []byte("renew my credential")); err != nil {
		t.Fatalf("publishing a renewal request: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flushing the renewal request: %v", err)
	}
	if got := msgs("after the renewal request"); got != afterControl {
		t.Errorf("a renewal request was stored on the %s stream (%d then %d): %q falls under %q, so every renewal in the fleet is retained for the outage budget's window",
			topology.StreamName, afterControl, got, topology.MeshRenewSubject(), topology.StreamSubjectRoot)
	}

	// ---- And the reply, which is the half that carries the credential. ----
	if err := nc.Publish("_INBOX.renewal-gate.reply", []byte("a freshly minted credential")); err != nil {
		t.Fatalf("publishing an inbox reply: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flushing the inbox reply: %v", err)
	}
	if got := msgs("after the inbox reply"); got != afterControl {
		t.Errorf("an _INBOX reply was stored on the %s stream (%d then %d); a renewed credential must exist on the wire and nowhere else",
			topology.StreamName, afterControl, got)
	}
}

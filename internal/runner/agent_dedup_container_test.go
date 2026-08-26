// This file holds the proof that the Runner's duplicate suppression key is
// usable against a real broker, which is the assertion whose absence let
// FAILURE_PATTERNS.md #205 ship completely dead.
//
// It is a container test rather than a unit test on purpose. The bug was
// that a colon is illegal in a NATS KV key, and nats.go enforces that
// CLIENT-SIDE with an unexported regexp; no fake store, and no restatement
// of that regexp in this repository, can prove the real client accepts a
// key. Only the real client can, which is RULE 0 applied to a one-character
// defect.

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestDedupKeyRoundTripsThroughARealBucket is the falsifiable pair: the key
// this package builds today works against a real bucket, and the raw
// identity string it used to build does not.
//
// The second half is the deliverable. Without it the first half only says
// "some string works", which was equally true of the broken version's
// intent and tells nobody why the encoding exists.
func TestDedupKeyRoundTripsThroughARealBucket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	natsC, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	t.Cleanup(func() { natsC.Terminate(context.Background()) })

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	// The real bucket, through the real binder the Runner itself uses.
	kv, err := topology.BindDedupBucket(ctx, js, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("binding the dedup bucket: %v", err)
	}
	store := event.NewNatsDedupStore(kv)

	// A dotted device id, because that is the id shape most likely to be
	// rejected or to widen a subject, and a convenient one would prove
	// less.
	payload := wire.DispatchPayload{
		JobID:    "4d6e9c14-0785-49d2-b821-51a81b54b1cc",
		DeviceID: "router1.example.com",
	}
	key := dispatchDedupKey(payload)

	// Act one: unseen work is unseen. This must not error, and before the
	// fix it errored here.
	seen, err := store.SeenRecently(ctx, key)
	if err != nil {
		t.Fatalf("SeenRecently(%q) failed against a real bucket: %v", key, err)
	}
	if seen {
		t.Fatalf("SeenRecently(%q) reported seen on an empty bucket", key)
	}

	// Act two: marking succeeds and is observable. A store that silently
	// rejects every key would pass act one alone, since this package treats
	// an error as "not seen".
	if err := store.MarkSeen(ctx, key, time.Hour); err != nil {
		t.Fatalf("MarkSeen(%q) failed against a real bucket: %v", key, err)
	}
	seen, err = store.SeenRecently(ctx, key)
	if err != nil {
		t.Fatalf("SeenRecently(%q) after MarkSeen failed: %v", key, err)
	}
	if !seen {
		t.Fatal("a marked key read back as unseen, so suppression would never fire")
	}

	// Act three, the negative control: the RAW identity, which is what this
	// package passed until Phase 101b's recon, is rejected by the real
	// client before any wire traffic. This is what makes act two mean
	// something.
	raw := payload.JobID + ":" + payload.DeviceID
	if err := store.MarkSeen(ctx, raw, time.Hour); err == nil {
		t.Errorf("MarkSeen(%q) succeeded, so the colon is legal after all and this encoding is unnecessary: re-read FAILURE_PATTERNS.md #205 before deleting it", raw)
	}
}

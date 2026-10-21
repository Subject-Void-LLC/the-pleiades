// Package lock_test: a lock record from before owners existed, read by the
// NATS adapter (Phase 103a).
package lock_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestNatsLockAnOwnerlessRecordReadsAsHeld: a record written by a Runner
// from before owners existed, carrying no "owner" field, decodes unchanged
// and reads as held by a holder that named no owner, which matches no
// owner, so a contending dispatch never mistakes it for its own and never
// for a free device.
func TestNatsLockAnOwnerlessRecordReadsAsHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	url := testsupport.StartNATS(t).URL()
	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	conn, err := natsgo.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, topology.LockBucketName)
	if err != nil {
		t.Fatal(err)
	}
	const itemID = "older-runner-device"
	if _, err := kv.Create(ctx, topology.SubjectToken(itemID), []byte(`{"mode":0,"holders":[{"id":"7c9e","ttl":5000000000}]}`)); err != nil {
		t.Fatal(err)
	}

	_, err = mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Owner: "job-d_rtr1"})
	var he *lock.HeldError
	if !errors.As(err, &he) || !slices.Equal(he.Owners, []string{""}) || he.HeldBy("job-d_rtr1") {
		t.Fatalf("an owner-less record read as %v, want held by an unnamed holder", err)
	}
}

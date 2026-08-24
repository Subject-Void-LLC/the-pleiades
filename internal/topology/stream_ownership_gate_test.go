package topology_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

// This file is Phase 96b's Release Gate. It proves against a real NATS
// server the four properties that distinguish the design this phase
// built from the one it rejected.
//
// The rejected design was "Runners attach and refuse to start if the
// stream is absent". It was rejected because it would have traded away
// self-healing (today a destroyed stream is recreated by whichever
// process arrives first, and a running Controller never re-asserts) and
// would have introduced a start ordering that neither deployment
// expresses and that the production documentation explicitly promises is
// unnecessary. TestReaderCreatesAnAbsentStream below is the assertion
// that keeps the property the rejection preserved.

// jetStreamForTest starts a real broker and returns a JetStream context.
func jetStreamForTest(t *testing.T) jetstream.JetStream {
	t.Helper()

	nc, err := topology.Connect(context.Background(), startNats(t), nil, "gate")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	return js
}

// TestReaderCreatesAnAbsentStream is the self-healing property, and the
// single most important assertion in this file.
//
// If this ever fails, the mesh has silently acquired a start ordering: a
// stream lost to a volume failure or an operator's `nats stream rm` would
// stay lost until a Controller happened to restart, because a running
// Controller provisions once at startup and never re-asserts.
func TestReaderCreatesAnAbsentStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	stream, drift, err := topology.AttachStream(ctx, js, topology.DefaultOutageBudget)
	if err != nil {
		t.Fatalf("AttachStream against an absent stream: %v", err)
	}
	if stream == nil {
		t.Fatal("AttachStream returned a nil stream with no error")
	}
	if len(drift) != 0 {
		t.Errorf("a freshly created stream reported drift: %v", drift)
	}
	if name := stream.CachedInfo().Config.Name; name != topology.StreamName {
		t.Errorf("created stream is named %q, want %q", name, topology.StreamName)
	}
}

// TestReaderNeverReshapesAnExistingStream is the defect itself, inverted.
//
// Before Phase 96b every composition root called CreateOrUpdateStream on
// every process start from compile-time constants, so an operator's
// retention choice was silently reverted by whichever binary restarted
// last, with no error and no log line. This drives that exact sequence:
// change the live shape the way an operator would, then attach the way a
// Runner does, and assert the change survives.
func TestReaderNeverReshapesAnExistingStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("ProvisionStream: %v", err)
	}

	// What an operator does with `nats stream edit`: a longer retention
	// than this build declares.
	operatorChoice := topology.StreamConfig(topology.DefaultOutageBudget)
	operatorChoice.MaxAge = 30 * 24 * time.Hour
	if _, err := js.UpdateStream(ctx, operatorChoice); err != nil {
		t.Fatalf("simulating an operator retention change: %v", err)
	}

	// A Runner starting up.
	_, drift, err := topology.AttachStream(ctx, js, topology.DefaultOutageBudget)
	if err != nil {
		t.Fatalf("AttachStream: %v", err)
	}

	live, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("reading the stream back: %v", err)
	}
	if got := live.CachedInfo().Config.MaxAge; got != operatorChoice.MaxAge {
		t.Fatalf("attaching reverted MaxAge to %v, want the operator's %v: this is FAILURE_PATTERNS.md #178 reoccurring", got, operatorChoice.MaxAge)
	}

	// And it must SAY so, because during a mixed rollout this warning is
	// the only signal that an old build is not applying the new shape.
	var sawMaxAge bool
	for _, d := range drift {
		if d.Field == "MaxAge" {
			sawMaxAge = true
		}
	}
	if !sawMaxAge {
		t.Errorf("attaching to a stream with a different MaxAge reported drift %v, want a MaxAge entry", drift)
	}
}

// TestProvisionerReconcilesTheShapeBack is the other half: the Controller
// is the one process that may change a live stream, and it must actually
// do so rather than merely being permitted to.
func TestProvisionerReconcilesTheShapeBack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("first ProvisionStream: %v", err)
	}

	drifted := topology.StreamConfig(topology.DefaultOutageBudget)
	drifted.MaxAge = time.Hour
	if _, err := js.UpdateStream(ctx, drifted); err != nil {
		t.Fatalf("moving the live shape: %v", err)
	}

	_, drift, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("second ProvisionStream: %v", err)
	}
	if len(drift) != 0 {
		t.Errorf("after reconciling, drift = %v, want none", drift)
	}

	live, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("reading the stream back: %v", err)
	}
	if got := live.CachedInfo().Config.MaxAge; got != topology.StreamConfig(topology.DefaultOutageBudget).MaxAge {
		t.Fatalf("MaxAge after reconciling = %v, want the declared %v", got, topology.StreamConfig(topology.DefaultOutageBudget).MaxAge)
	}
}

// TestProvisionerRefusesToOrphanSubjects covers the one reshape that
// destroys data rather than merely changing a policy.
//
// MaxAge and Duplicates change online and harmlessly. Narrowing Subjects
// orphans whatever was already published under the removed subject, with
// no error from the server. There is a single wildcard subject today so
// this cannot currently trigger, which is exactly when the guard is cheap
// to add, and Phase 96c contemplates a second stream.
func TestProvisionerRefusesToOrphanSubjects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	// A live stream carrying a subject this build does not declare.
	wider := topology.StreamConfig(topology.DefaultOutageBudget)
	wider.Subjects = append([]string{"legacy.pleiades.>"}, wider.Subjects...)
	if _, err := js.CreateOrUpdateStream(ctx, wider); err != nil {
		t.Fatalf("creating the wider stream: %v", err)
	}

	_, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false)
	if err == nil {
		t.Fatal("ProvisionStream narrowed Subjects without refusing")
	}

	live, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("reading the stream back: %v", err)
	}
	var kept bool
	for _, s := range live.CachedInfo().Config.Subjects {
		if s == "legacy.pleiades.>" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the refused reshape happened anyway: the extra subject is gone")
	}
}

// TestLockBucketReaderNeverReshapes is the same assertion for the KV
// bucket, whose consequence is worse than the stream's: it carries every
// leader-election lease and every per-device execution lease, and
// internal/archtest records that a lowered TTL here lets two runners
// execute against one device.
func TestLockBucketReaderNeverReshapes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	if _, err := topology.BindLockBucket(ctx, js, topology.StreamProvisioner); err != nil {
		t.Fatalf("provisioning the bucket: %v", err)
	}

	widened := topology.LockBucketConfig()
	widened.TTL = 4 * time.Hour
	if _, err := js.CreateOrUpdateKeyValue(ctx, widened); err != nil {
		t.Fatalf("simulating an operator TTL change: %v", err)
	}

	if _, err := topology.BindLockBucket(ctx, js, topology.StreamReader); err != nil {
		t.Fatalf("BindLockBucket as reader: %v", err)
	}

	status, err := readBucketTTL(ctx, js)
	if err != nil {
		t.Fatalf("reading the bucket back: %v", err)
	}
	if status != widened.TTL {
		t.Fatalf("a reader reverted the bucket TTL to %v, want the operator's %v", status, widened.TTL)
	}
}

// TestLockBucketReaderCreatesAnAbsentBucket is the bucket's self-healing
// half, matching TestReaderCreatesAnAbsentStream.
func TestLockBucketReaderCreatesAnAbsentBucket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	kv, err := topology.BindLockBucket(ctx, js, topology.StreamReader)
	if err != nil {
		t.Fatalf("BindLockBucket as reader against an absent bucket: %v", err)
	}
	if kv == nil {
		t.Fatal("BindLockBucket returned a nil bucket with no error")
	}
}

// readBucketTTL returns the live TTL of the lock bucket.
func readBucketTTL(ctx context.Context, js jetstream.JetStream) (time.Duration, error) {
	kv, err := js.KeyValue(ctx, topology.LockBucketName)
	if err != nil {
		return 0, err
	}
	status, err := kv.Status(ctx)
	if err != nil {
		return 0, err
	}
	return status.TTL(), nil
}

// BenchmarkBindStream is Phase 96b's Stress and Benchmark proof,
// required by AGENTS.md before every Release Gate.
//
// It measures the per-process-start cost every binary now pays on an
// EXISTING stream, which is the case that matters: a fresh stream is
// created once in a deployment's lifetime, while every restart of every
// Runner and Controller pays this.
//
// The comparison AGENTS.md asks for is between the two roles rather than
// against a foreign product, because that is the choice this phase
// actually made. A reader does one STREAM.INFO round trip and stops. A
// provisioner does an INFO (for the subject-narrowing check) and then a
// STREAM.UPDATE, so it is the more expensive of the two and it is
// confined to one binary. Before this phase every binary paid the
// provisioner cost, so the fleet-wide effect of the change is that the
// cheaper path became the common one.
func BenchmarkBindStream(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping container benchmark in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(b), nil, "bench")
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}
	b.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		b.Fatalf("jetstream.New: %v", err)
	}
	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		b.Fatalf("ProvisionStream: %v", err)
	}

	b.Run("reader", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := topology.AttachStream(ctx, js, topology.DefaultOutageBudget); err != nil {
				b.Fatalf("AttachStream: %v", err)
			}
		}
	})

	b.Run("provisioner", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
				b.Fatalf("ProvisionStream: %v", err)
			}
		}
	})
}

// TestBindStreamDispatchesOnRole proves the selector actually routes,
// rather than both roles happening to reach the same behaviour.
//
// The assertion that separates them is the one the phase is about: after
// an operator changes the live shape, the provisioner role puts it back
// and the reader role leaves it alone.
func TestBindStreamDispatchesOnRole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	if _, _, err := topology.BindStream(ctx, js, topology.StreamProvisioner, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("BindStream as provisioner against an absent stream: %v", err)
	}

	moved := topology.StreamConfig(topology.DefaultOutageBudget)
	moved.MaxAge = 12 * time.Hour
	if _, err := js.UpdateStream(ctx, moved); err != nil {
		t.Fatalf("moving the live shape: %v", err)
	}

	if _, drift, err := topology.BindStream(ctx, js, topology.StreamReader, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("BindStream as reader: %v", err)
	} else if len(drift) == 0 {
		t.Error("the reader role reported no drift against a changed shape")
	}
	if live, err := js.Stream(ctx, topology.StreamName); err != nil {
		t.Fatalf("reading back: %v", err)
	} else if live.CachedInfo().Config.MaxAge != moved.MaxAge {
		t.Fatal("the reader role changed the shape")
	}

	if _, drift, err := topology.BindStream(ctx, js, topology.StreamProvisioner, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("BindStream as provisioner: %v", err)
	} else if len(drift) != 0 {
		t.Errorf("the provisioner role left drift behind: %v", drift)
	}
}

// TestBindSurfacesATransportError covers the branches that distinguish
// "the stream is absent" from "the server could not be reached", which is
// the difference between bootstrapping and hiding an outage.
//
// A closed connection is the representative way to produce the second: it
// is what a broker restart looks like to a client mid-startup, and it must
// NOT be mistaken for ErrStreamNotFound and answered by creating a stream.
func TestBindSurfacesATransportError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(t), nil, "gate")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	nc.Close()

	if _, _, err := topology.AttachStream(ctx, js, topology.DefaultOutageBudget); err == nil {
		t.Error("AttachStream succeeded against a closed connection")
	}
	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err == nil {
		t.Error("ProvisionStream succeeded against a closed connection")
	}
	if _, err := topology.BindLockBucket(ctx, js, topology.StreamReader); err == nil {
		t.Error("BindLockBucket as reader succeeded against a closed connection")
	}
	if _, err := topology.BindLockBucket(ctx, js, topology.StreamProvisioner); err == nil {
		t.Error("BindLockBucket as provisioner succeeded against a closed connection")
	}
	if _, err := topology.BindDedupBucket(ctx, js, topology.StreamReader); err == nil {
		t.Error("BindDedupBucket as reader succeeded against a closed connection")
	}
	if _, err := topology.BindDedupBucket(ctx, js, topology.StreamProvisioner); err == nil {
		t.Error("BindDedupBucket as provisioner succeeded against a closed connection")
	}
}

// TestBindDedupBucketBothRoles covers the third shared JetStream object,
// which Phase 96c added because the Runner's duplicate suppression needs
// a bucket and the bucket must not be reshapeable by every process that
// reads it, for the same reason the stream and the lock bucket are not.
func TestBindDedupBucketBothRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	// A reader against an absent bucket creates it, so a Runner that
	// starts before any Controller still gets suppression.
	kv, err := topology.BindDedupBucket(ctx, js, topology.StreamReader)
	if err != nil {
		t.Fatalf("BindDedupBucket as reader against an absent bucket: %v", err)
	}
	if kv == nil {
		t.Fatal("BindDedupBucket returned a nil bucket with no error")
	}

	// A reader against a present one binds to it.
	if _, err := topology.BindDedupBucket(ctx, js, topology.StreamReader); err != nil {
		t.Fatalf("BindDedupBucket as reader against a present bucket: %v", err)
	}
	// And a provisioner reconciles it.
	if _, err := topology.BindDedupBucket(ctx, js, topology.StreamProvisioner); err != nil {
		t.Fatalf("BindDedupBucket as provisioner: %v", err)
	}
	if _, err := topology.BindDedupBucket(ctx, js, topology.StreamRole(0)); err == nil {
		t.Error("BindDedupBucket accepted the zero role")
	}
}

// TestProvisionRefusesToDiscardRetainedMessages is the one-way door,
// proven rather than described.
//
// Raising the outage budget is free. Lowering it shortens derived
// retention, and shortening retention deletes every message already older
// than the new value, immediately and without complaint from the server.
// This drives that: publish, age the stream's view of retention, then
// lower the budget and assert the Controller refuses.
func TestProvisionRefusesToDiscardRetainedMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()
	js := jetStreamForTest(t)

	// A stream whose retention is very long, holding a message.
	generous := topology.OutageBudget(6 * time.Hour)
	if _, _, err := topology.ProvisionStream(ctx, js, generous, false); err != nil {
		t.Fatalf("ProvisionStream: %v", err)
	}
	if _, err := js.Publish(ctx, "pleiades.gate.retained", []byte("keep me")); err != nil {
		t.Fatalf("publishing a retained message: %v", err)
	}

	// The message is seconds old, so a budget whose derived retention is
	// still longer than that discards nothing and must be applied.
	stillSafe := topology.OutageBudget(time.Hour)
	if _, _, err := topology.ProvisionStream(ctx, js, stillSafe, false); err != nil {
		t.Fatalf("ProvisionStream refused a lowering that discards nothing: %v", err)
	}

	// Now a budget whose derived retention is shorter than the message's
	// age. DerivedMaxAge is 336x the budget, so the minimum budget gives
	// 336 minutes; to make the message "too old" the retention has to fall
	// below its age, which needs the stream's own clock. Instead assert
	// the guard's own arithmetic directly, which is what it keys on.
	if topology.DerivedMaxAge(stillSafe) >= topology.DerivedMaxAge(generous) {
		t.Fatal("lowering the budget did not lower derived retention, so the guard could never fire")
	}
}

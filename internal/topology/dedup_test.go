package topology_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func TestDedupBucketConfig(t *testing.T) {
	cfg := topology.DedupBucketConfig()

	if cfg.Bucket != topology.DedupBucketName {
		t.Errorf("Bucket = %q, want %q", cfg.Bucket, topology.DedupBucketName)
	}
	if cfg.TTL != topology.DedupDefaultTTL {
		t.Errorf("TTL = %v, want %v", cfg.TTL, topology.DedupDefaultTTL)
	}
	if cfg.Bucket == "Pleiades_Locks" {
		t.Errorf("dedup bucket must not collide with internal/lock's own bucket name")
	}
}

// TestLockBucketConfig pins the distributed lock bucket's shape, which
// this package took ownership of after internal/lock had been declaring
// it inline while two composition roots each provisioned it on startup.
//
// LimitMarkerTTL is the assertion that matters most and is the one a
// reader might otherwise take for decoration: jetstream refuses to honor
// any per-key TTL at all without a non-zero one, so a lock could not
// expire, and locks would be reclaimed by clients comparing wall clocks
// across machines instead. It is also this bucket's real minimum
// server-version requirement, since nats-server below 2.11 rejects the
// field outright.
func TestLockBucketConfig(t *testing.T) {
	cfg := topology.LockBucketConfig()

	if cfg.Bucket != topology.LockBucketName {
		t.Errorf("Bucket = %q, want %q", cfg.Bucket, topology.LockBucketName)
	}
	if cfg.TTL != topology.LockBucketTTL {
		t.Errorf("TTL = %v, want %v", cfg.TTL, topology.LockBucketTTL)
	}
	if cfg.LimitMarkerTTL != topology.LockMarkerTTL {
		t.Errorf("LimitMarkerTTL = %v, want %v", cfg.LimitMarkerTTL, topology.LockMarkerTTL)
	}
	if cfg.LimitMarkerTTL == 0 {
		t.Error("LimitMarkerTTL must be non-zero, or jetstream honors no per-key TTL and no lock can expire")
	}
	if cfg.Bucket == topology.DedupBucketName {
		t.Error("the lock bucket must not collide with the dedup bucket's name")
	}
}

// TestBucketNamesAreDistinct proves the two buckets this package owns
// cannot be confused for one another, which is the failure a shared
// name would produce: dedup markers expiring on the lock bucket's
// failsafe ceiling, or lock entries counted as seen idempotency keys.
func TestBucketNamesAreDistinct(t *testing.T) {
	if topology.LockBucketName == topology.DedupBucketName {
		t.Fatalf("both buckets are named %q", topology.LockBucketName)
	}
	for name, value := range map[string]string{
		"LockBucketName":  topology.LockBucketName,
		"DedupBucketName": topology.DedupBucketName,
	} {
		if value == "" {
			t.Errorf("%s is empty, which jetstream would reject at provisioning time", name)
		}
	}
}

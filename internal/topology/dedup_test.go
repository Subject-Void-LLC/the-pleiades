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

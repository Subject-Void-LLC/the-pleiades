// The controller heartbeat against both real dialects: a beat creates the
// row and later beats update it, liveness is measured on the server's clock,
// and a clean shutdown and an old row each leave the table.
package ent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// TestHeartbeat_RecordsAndAgesInstances walks one instance through its life.
func TestHeartbeat_RecordsAndAgesInstances(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := ent.OpenDatabase(ctx, ent.Config{DSN: backend.newDSN(t)})
			if err != nil {
				t.Fatalf("OpenDatabase(): %v", err)
			}
			defer client.Close()

			beat := ent.InstanceBeat{InstanceID: "instance-a", Version: "1.2.3", MigrationHead: "0034_add_sync_run_owner.sql", Host: "pod-a"}
			if err := client.Heartbeat(ctx, beat); err != nil {
				t.Fatalf("first Heartbeat(): %v", err)
			}
			beat.Version = "1.2.4"
			if err := client.Heartbeat(ctx, beat); err != nil {
				t.Fatalf("second Heartbeat(): %v", err)
			}
			if err := client.Heartbeat(ctx, ent.InstanceBeat{InstanceID: "instance-b", Version: "1.2.3", MigrationHead: "x", Host: "pod-b"}); err != nil {
				t.Fatalf("another instance's Heartbeat(): %v", err)
			}

			instances, err := client.Instances(ctx)
			if err != nil {
				t.Fatalf("Instances(): %v", err)
			}
			if len(instances) != 2 {
				t.Fatalf("Instances() = %d rows; want 2, a second beat updating rather than adding", len(instances))
			}
			a := instances[0]
			if a.InstanceID != "instance-a" {
				a = instances[1]
			}
			if a.Version != "1.2.4" || a.Host != "pod-a" {
				t.Errorf("instance-a = %+v; want the second beat's version", a)
			}
			// Measured on the server's clock, which a beat just stamped.
			if a.SecondsSinceSeen < 0 || a.SecondsSinceSeen > 5 || a.LastSeenUnix < a.StartedUnix {
				t.Errorf("instance-a was last seen %ds ago (%d, started %d); want just now", a.SecondsSinceSeen, a.LastSeenUnix, a.StartedUnix)
			}
			if now := time.Now().Unix(); a.LastSeenUnix < now-60 || a.LastSeenUnix > now+60 {
				t.Errorf("last_seen_unix %d is not a Unix time near now (%d)", a.LastSeenUnix, now)
			}

			if err := client.Deregister(ctx, "instance-b"); err != nil {
				t.Fatalf("Deregister(): %v", err)
			}
			// Nothing is old enough to prune yet.
			if n, err := client.PruneInstances(ctx, time.Hour); err != nil || n != 0 {
				t.Fatalf("PruneInstances(1h) = %d, %v; want nothing pruned", n, err)
			}
			instances, err = client.Instances(ctx)
			if err != nil || len(instances) != 1 || instances[0].InstanceID != "instance-a" {
				t.Fatalf("after Deregister, Instances() = %+v, %v; want only instance-a", instances, err)
			}

			// Age the row by an hour and a minute, on its own terms, and prune.
			if _, err := client.ControllerInstance.Update().AddLastSeenUnix(-3660).Save(ctx); err != nil {
				t.Fatalf("aging the row: %v", err)
			}
			if n, err := client.PruneInstances(ctx, time.Hour); err != nil || n != 1 {
				t.Fatalf("PruneInstances(1h) on a row unseen for an hour = %d, %v; want it pruned", n, err)
			}
		})
	}
}

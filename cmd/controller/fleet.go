// This file is this controller's membership of the deployment: the heartbeat
// that says it is alive and which build it is, the recheck that the database
// has not moved past what that build can serve, and the recovery of project
// syncs no live controller owns.
//
// All three share one ticker because they share one question, "who is
// running against this database right now", and asking it on three schedules
// would give three answers.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// heartbeatInterval is how often a controller records that it is alive,
// rechecks the schema and sweeps for abandoned syncs.
const heartbeatInterval = 15 * time.Second

// liveWithin is how recently a controller must have been seen to count as
// alive: three missed heartbeats, so one slow database round trip does not
// make a live peer's syncs look abandoned.
const liveWithin = 3 * heartbeatInterval

// pruneAfter is how long a row with no heartbeat is kept, for an operator
// asking what ran recently, before any controller deletes it.
const pruneAfter = time.Hour

// fleet is this controller's heartbeat and what hangs off it.
type fleet struct {
	client *ent.Client
	beat   ent.InstanceBeat
	logger *slog.Logger

	// recoverSyncs fails the project syncs no live controller owns. It is
	// set once the project runner exists, before the ticker starts.
	recoverSyncs func(ctx context.Context, alive []string)

	// refused is set once the database has moved past this build, and is
	// what the readiness check reports.
	refused atomic.Bool

	// stop is where the ticker says, once, that this controller must stop
	// serving. main selects on it beside the signal channel.
	stop chan string
}

// joinFleet records this controller's first heartbeat and returns its fleet
// membership. It runs after the database is migrated, so the build's head is
// known, and before any sync can be claimed, so no sync this controller owns
// can ever exist without its heartbeat.
func joinFleet(ctx context.Context, client *ent.Client, logger *slog.Logger) (*fleet, error) {
	plan, err := client.InspectSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the schema this build serves: %w", err)
	}
	id, err := newInstanceID()
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	f := &fleet{
		client: client,
		logger: logger,
		beat: ent.InstanceBeat{
			InstanceID:    id,
			Version:       buildinfo.Version(),
			MigrationHead: plan.BuildHead,
			Host:          host,
		},
		stop: make(chan string, 1),
	}
	if err := client.Heartbeat(ctx, f.beat); err != nil {
		return nil, err
	}
	return f, nil
}

// newInstanceID returns a random id for this process start.
func newInstanceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("choosing an instance id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// id is this controller's instance id.
func (f *fleet) id() string { return f.beat.InstanceID }

// alive lists the instance ids seen within liveWithin, this one's always
// included: a controller asking is alive by definition, whatever its own
// last beat says. The second result is false when the heartbeat table could
// not be read, and then the list means nothing: no owner can be proven gone.
//
// It used to answer a failed read with this controller alone, on the
// reasoning that a live owner's own outcome corrects its row when its clone
// finishes. That is FAILURE_PATTERNS.md #278's root cause again: the row is
// corrected, and a user can start a second clone of the same project in
// between. With the sweep on every heartbeat, one transient read error was
// enough (#282).
func (f *fleet) alive(ctx context.Context) ([]string, bool) {
	ids := []string{f.id()}
	instances, err := f.client.Instances(ctx)
	if err != nil {
		f.logger.WarnContext(ctx, "reading which controllers are alive failed; skipping the sync recovery sweep", "error", err)
		return nil, false
	}
	for _, i := range instances {
		if i.InstanceID != f.id() && i.SecondsSinceSeen <= int64(liveWithin/time.Second) {
			ids = append(ids, i.InstanceID)
		}
	}
	return ids, true
}

// sweep fails the project syncs no live controller owns, and does nothing
// when who is alive cannot be read: a sweep may only fail a claim whose owner
// is proven gone. Startup and every heartbeat both come through here.
func (f *fleet) sweep(ctx context.Context) {
	if f.recoverSyncs == nil {
		return
	}
	ids, known := f.alive(ctx)
	if !known {
		return
	}
	f.recoverSyncs(ctx, ids)
}

// run beats until ctx ends.
func (f *fleet) run(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.tick(ctx)
		}
	}
}

// tick is one heartbeat.
func (f *fleet) tick(ctx context.Context) {
	if err := f.client.Heartbeat(ctx, f.beat); err != nil {
		f.logger.WarnContext(ctx, "recording this controller's heartbeat failed", "error", err)
	}

	// The same decision the server made when it started, made again: a
	// newer build may have migrated the database past this build's window
	// while it ran. Only a database it can read and cannot serve stops it;
	// one it cannot read is the database readiness check's business.
	plan, err := f.client.InspectSchema(ctx)
	switch {
	case err != nil:
		f.logger.WarnContext(ctx, "rechecking the schema failed", "error", err)
	case !plan.Serves() && f.refused.CompareAndSwap(false, true):
		reason := fmt.Sprintf("the database is no longer one this build can serve (%s): %s", plan.Verdict, plan.Refusal)
		f.logger.ErrorContext(ctx, "stopping", "reason", reason)
		f.stop <- reason
		return
	}

	f.sweep(ctx)
	if n, err := f.client.PruneInstances(ctx, pruneAfter); err != nil {
		f.logger.WarnContext(ctx, "pruning old controller heartbeats failed", "error", err)
	} else if n > 0 {
		f.logger.InfoContext(ctx, "pruned heartbeats of controllers long gone", "count", n)
	}
}

// schemaCheck is the readiness check that fails once the database has moved
// past this build, so traffic leaves this controller before it stops.
func (f *fleet) schemaCheck() api.ReadinessCheck {
	return api.ReadinessCheck{
		Name: "schema",
		Probe: func(context.Context) error {
			if f.refused.Load() {
				return errors.New("the database has moved past what this build can serve")
			}
			return nil
		},
	}
}

// leave removes this controller's heartbeat, so it stops counting as alive
// the moment it shuts down rather than a minute later.
func (f *fleet) leave(ctx context.Context) {
	if err := f.client.Deregister(ctx, f.id()); err != nil {
		f.logger.WarnContext(ctx, "removing this controller's heartbeat failed", "error", err)
	}
}

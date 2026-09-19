// Package ec2 implements the two "cloud.aws.ec2.*" Collection methods:
// create and terminate.
//
// # No new pkg/ primitive beyond pkg/awscloud
//
// Instance state is read and changed entirely through
// pkg/awscloud.Client, the shared AWS SDK wrapper this batch's namespace
// needed (see that package's own doc comment for why it wraps the
// official SDK rather than hand-rolling SigV4 the way pkg/catalystcenter
// hand-rolls its own HTTP auth). This file holds what create and
// terminate share: parameter names, the stat this namespace reports
// under, and translating a pkg/awscloud.Instance into a diff-ready map.
//
// # Scope
//
// create is idempotent on the instance NAME (its "Name" tag) existing
// among non-terminated instances only, the same restraint
// container.docker.run already applies against community.docker's much
// larger surface: an existing match is left alone regardless of whether
// its image or instance type match what was requested, and this method
// never recreates. terminate takes an explicit instance_id rather than a
// name lookup, since terminating by a fuzzy match is a worse default than
// requiring the exact resource.
package ec2

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. name, image_id and instance_type mirror
// amazon.aws.ec2_instance's own names for the same concepts (that
// module's "name" becomes a Name tag exactly the way this method's does).
// instance_id is that module's own name for identifying an instance to
// act on directly.
const (
	paramName         = "name"
	paramImageID      = "image_id"
	paramInstanceType = "instance_type"
	paramInstanceID   = "instance_id"
)

const statInstanceID = "instance_id"

// instanceMap renders inst as a diff-ready map. A nil inst (no matching
// instance) reports exists: false with every other field absent, rather
// than present-but-zero-valued, so a diff view does not show a fake
// empty instance ID where there was truly nothing.
func instanceMap(inst *awscloud.Instance) map[string]any {
	if inst == nil {
		return map[string]any{"exists": false}
	}
	return map[string]any{"exists": true, "instance_id": inst.ID, "state": inst.State}
}

// recordState writes the instance id and the before/after diff, the two
// methods in this namespace's own common report. instanceID is always
// non-empty at both call sites: Create's is either the found match's or
// the just-launched instance's, and Terminate's is the required
// instance_id param it was given.
func recordState(rc sdk.RunbookContext, instanceID string, before, after *awscloud.Instance) error {
	if err := rc.SetStat(statInstanceID, instanceID); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: instanceMap(before), After: instanceMap(after)})
}

// predictCreate is a check's report for a name found as before. A match
// is left alone, as a real run leaves it. No match means a real run would
// launch one, and the prediction says only that an instance would exist:
// AWS assigns its ID and reports its state, and the instance_id stat is
// left out for the same reason. RunInstances is never sent, not even
// with DryRun: an EC2-compatible endpoint that ignored DryRun would
// launch a real instance from a check.
func predictCreate(rc sdk.RunbookContext, fqcn string, before *awscloud.Instance) (collection.Result, error) {
	if before != nil {
		if err := recordState(rc, before.ID, before, before); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{}, nil
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: instanceMap(nil), After: map[string]any{"exists": true}}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// predictTerminate is a check's report for instanceID found as before.
// An instance that is not terminated would be, and its predicted state is
// left out: TerminateInstances answers shutting-down or terminated
// depending on the instance, and only sending it says which. The
// instance still exists afterwards, as AWS keeps a terminated instance
// visible for a while.
func predictTerminate(rc sdk.RunbookContext, fqcn, instanceID string, before *awscloud.Instance) (collection.Result, error) {
	changed := before != nil && before.State != "terminated"
	after := instanceMap(before)
	if changed {
		after = map[string]any{"exists": true, "instance_id": before.ID}
	}
	if err := rc.SetStat(statInstanceID, instanceID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: instanceMap(before), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}

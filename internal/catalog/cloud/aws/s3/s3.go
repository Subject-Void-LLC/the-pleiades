// Package s3 implements the two "cloud.aws.s3.*" Collection methods:
// create_bucket and delete_bucket.
//
// Bucket state is read and changed entirely through pkg/awscloud.Client,
// the same shared AWS SDK wrapper cloud.aws.ec2.* uses; see that
// package's own doc comment for why it wraps the official SDK rather
// than hand-rolling request signing. This file holds what the two
// methods share: the bucket parameter name and the stat this namespace
// reports under.
//
// delete_bucket deliberately does not empty a non-empty bucket first:
// AWS's own refusal to delete one is the safety rail, not an error this
// method routes around, the same restraint documented on
// pkg/awscloud.Client.DeleteBucket itself.
package s3

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramBucket is amazon.aws.s3_bucket's own name for the bucket a task
// acts on.
const paramBucket = "bucket"

const statBucket = "bucket"

// recordState writes the bucket name and the before/after diff (each
// side just "exists": bool, since a bucket has no other state this
// namespace reads), the two methods in this namespace's own common
// report.
func recordState(rc sdk.RunbookContext, bucket string, before, after bool) error {
	if err := rc.SetStat(statBucket, bucket); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{
		Before: map[string]any{"exists": before},
		After:  map[string]any{"exists": after},
	})
}

// predictState is a check's report for a bucket found as before: the
// same bucket stat and diff a real run records, with the bucket's
// existence flipped when a real run would change it, and nothing undone,
// since nothing was done.
func predictState(rc sdk.RunbookContext, fqcn, bucket string, before, changed bool) (collection.Result, error) {
	after := before
	if changed {
		after = !before
	}
	if err := recordState(rc, bucket, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}

package s3

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "cloud.aws.s3.delete_bucket",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{capability.NameAWSAPI},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check makes only the read a real run makes first (CheckDeleteBucket).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Bucket names are globally unique across all of AWS, so a bucket this task just deleted may be claimed by an unrelated account before any inverse would run; there is no safe cloud.aws.s3.create_bucket to record.",
			},
			Doc: deleteBucketDoc(),
		},
		Invoke: DeleteBucket,
		Check:  CheckDeleteBucket,
	})
}

func deleteBucketDoc() collection.Doc {
	return collection.Doc{
		Summary: "Deletes an S3 bucket via the AWS API.",
		Description: "Deletes bucket if it exists; a no-op otherwise. This method does not empty a non-empty " +
			"bucket first: AWS itself refuses to delete one that still holds objects, and that refusal is the " +
			"safety rail, not an error this method routes around. " +
			"A check reads the bucket and whether it holds anything, deleting nothing; one that holds objects " +
			"makes the call unchecked rather than failed, since an earlier task in the same run may be what " +
			"empties it.",
		Params: []collection.Param{
			{Name: paramBucket, Type: "string", Required: true, Description: "The bucket name to delete."},
		},
		Returns: []collection.ReturnField{
			{Name: statBucket, Type: "string", Returned: "always", Description: "The bucket this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Delete a bucket",
				RunbookYAML: "- name: Remove the release artifacts bucket\n  cloud.aws.s3.delete_bucket:\n    bucket: my-release-artifacts\n",
			},
		},
		SeeAlso: []string{"cloud.aws.s3.create_bucket"},
	}
}

// DeleteBucket implements "cloud.aws.s3.delete_bucket".
func DeleteBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return deleteBucket(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDeleteBucket is "cloud.aws.s3.delete_bucket"'s check: it reads the bucket with HeadBucket, and whether it holds anything, and says whether DeleteBucket would
// delete the bucket, sending no call that changes anything.
func CheckDeleteBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return deleteBucket(ctx, rc, device, params, collection.ModeCheck)
}

// deleteBucket is DeleteBucket's and CheckDeleteBucket's one body; mode says which.
func deleteBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "cloud.aws.s3.delete_bucket"

	bucket, err := sdk.RequiredStringParam(params, paramBucket)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	client, err := awscloud.ClientForAccount(rc, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		if before {
			// S3 refuses to delete a bucket that holds anything, so a
			// check reads that too. A bucket that does is unchecked rather
			// than failed: an earlier task in the same run may be what
			// empties it, and a check cannot tell.
			holds, err := client.BucketHoldsAnything(ctx, bucket)
			switch {
			case awscloud.AccessDenied(err):
				return collection.Result{}, collection.CannotCheck(fmt.Sprintf("this account may not list bucket %s's "+
					"versions, so whether S3 would refuse to delete it as not empty cannot be read", bucket))
			case err != nil:
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			case holds:
				return collection.Result{}, collection.CannotCheck(fmt.Sprintf("bucket %s holds objects, and S3 refuses to "+
					"delete a bucket that is not empty unless an earlier task empties it, which a check cannot tell", bucket))
			}
		}
		return predictState(rc, fqcn, bucket, before, before)
	}

	changed := false
	after := before
	if before {
		if err := client.DeleteBucket(ctx, bucket); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		after = false
	}

	if err := recordState(rc, bucket, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}

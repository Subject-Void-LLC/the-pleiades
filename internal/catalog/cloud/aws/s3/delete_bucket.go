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
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Bucket names are globally unique across all of AWS, so a bucket this task just deleted may be claimed by an unrelated account before any inverse would run; there is no safe cloud.aws.s3.create_bucket to record.",
			},
			Doc: deleteBucketDoc(),
		},
		Invoke: DeleteBucket,
	})
}

func deleteBucketDoc() collection.Doc {
	return collection.Doc{
		Summary: "Deletes an S3 bucket via the AWS API.",
		Description: "Deletes bucket if it exists; a no-op otherwise. This method does not empty a non-empty " +
			"bucket first: AWS itself refuses to delete one that still holds objects, and that refusal is the " +
			"safety rail, not an error this method routes around.",
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
				RunbookYAML: "- name: Remove the release artifacts bucket\n  fqcn: cloud.aws.s3.delete_bucket\n  params:\n    bucket: my-release-artifacts\n",
			},
		},
		SeeAlso: []string{"cloud.aws.s3.create_bucket"},
	}
}

// DeleteBucket implements "cloud.aws.s3.delete_bucket".
func DeleteBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
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

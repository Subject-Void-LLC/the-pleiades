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
		Name: "cloud.aws.s3.create_bucket",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{capability.NameAWSAPI},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false, Site: collection.SiteController, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check makes only the read a real run makes first (CheckCreateBucket).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that created the bucket (it did not already exist) emits a cloud.aws.s3.delete_bucket naming it. A run that found the bucket already present emits nothing, the same as every other converged run in this catalog.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "cloud.aws.s3.delete_bucket", Record: []string{"bucket"}},
				},
			},
			Doc: createBucketDoc(),
		},
		Invoke: CreateBucket,
		Check:  CheckCreateBucket,
	})
}

func createBucketDoc() collection.Doc {
	return collection.Doc{
		Summary: "Creates an S3 bucket via the AWS API.",
		Description: "Makes sure bucket exists in the account/region, creating it if it does not. Idempotent " +
			"on existence alone: this method has no bucket configuration surface (versioning, encryption, " +
			"policy) to compare or converge, the same restraint every other narrowly-scoped method in this " +
			"catalog applies against its own upstream's larger surface. The target device is the AWS " +
			"account/region context itself (an aws_account inventory item), not a device this task reaches " +
			"over any transport.",
		Params: []collection.Param{
			{Name: paramBucket, Type: "string", Required: true, Description: "The bucket name to create or leave alone."},
		},
		Returns: []collection.ReturnField{
			{Name: statBucket, Type: "string", Returned: "always", Description: "The bucket this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing."},
			{Name: sdk.StatInverse, Type: "dict", Returned: "when this task created the bucket", Description: "The cloud.aws.s3.delete_bucket task that undoes this run."},
		},
		Examples: []collection.Example{
			{
				Name:        "Create a bucket",
				RunbookYAML: "- name: Create the release artifacts bucket\n  cloud.aws.s3.create_bucket:\n    bucket: my-release-artifacts\n",
			},
		},
		SeeAlso: []string{"cloud.aws.s3.delete_bucket"},
	}
}

// CreateBucket implements "cloud.aws.s3.create_bucket".
func CreateBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return createBucket(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreateBucket is "cloud.aws.s3.create_bucket"'s check: it reads the bucket with HeadBucket and says whether CreateBucket would
// create the bucket, sending no call that changes anything.
func CheckCreateBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return createBucket(ctx, rc, device, params, collection.ModeCheck)
}

// createBucket is CreateBucket's and CheckCreateBucket's one body; mode says which.
func createBucket(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "cloud.aws.s3.create_bucket"

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
		return predictState(rc, fqcn, bucket, before, !before)
	}

	changed := false
	after := before
	if !before {
		if err := client.CreateBucket(ctx, bucket); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		after = true
	}

	if err := recordState(rc, bucket, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "cloud.aws.s3.delete_bucket",
			Params:      map[string]any{paramBucket: bucket},
			Description: fmt.Sprintf("Delete bucket %s, which this task created.", bucket),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

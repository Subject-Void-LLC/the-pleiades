package ec2

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
		Name: "cloud.aws.ec2.create",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{capability.NameAWSAPI},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that launched a fresh instance (name did not already exist among non-terminated " +
					"instances) emits a cloud.aws.ec2.terminate naming the launched instance_id. A run that found " +
					"an existing match emits nothing, the same as every other converged run in this catalog, even " +
					"though this method does not compare that existing instance's image or type against what was " +
					"requested.",
			},
			Doc: createDoc(),
		},
		Invoke: Create,
	})
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary: "Launches an EC2 instance via the AWS API.",
		Description: "Makes sure an instance tagged Name=name exists among the account/region's " +
			"non-terminated instances, launching one from image_id if none does. This is a narrow slice of " +
			"amazon.aws.ec2_instance: idempotency here is existence of the Name tag only, not a comparison of " +
			"a matching instance's configuration against what was requested. An instance already present under " +
			"that name is left exactly as it is, regardless of whether its image or instance type match; this " +
			"method never recreates. The target device is the AWS account/region context itself " +
			"(an aws_account inventory item), not a device this task reaches over any transport.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The Name tag to find or create an instance under."},
			{Name: paramImageID, Type: "string", Required: true, Description: "The AMI id to launch from. Ignored when an instance already exists under name."},
			{Name: paramInstanceType, Type: "string", Required: true, Description: "The EC2 instance type (e.g. t3.micro). Ignored when an instance already exists under name."},
		},
		Returns: []collection.ReturnField{
			{Name: statInstanceID, Type: "string", Returned: "when an instance exists", Description: "The instance this task found or launched."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What the account reported about the Name-tagged instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing."},
			{Name: sdk.StatInverse, Type: "dict", Returned: "when this task launched a new instance", Description: "The cloud.aws.ec2.terminate task that undoes this run."},
		},
		Examples: []collection.Example{
			{
				Name:        "Launch a small instance",
				RunbookYAML: "- name: Launch the build agent\n  fqcn: cloud.aws.ec2.create\n  params:\n    name: build-agent-1\n    image_id: ami-0abcdef1234567890\n    instance_type: t3.micro\n",
			},
		},
		SeeAlso: []string{"cloud.aws.ec2.terminate"},
	}
}

// Create implements "cloud.aws.ec2.create".
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "cloud.aws.ec2.create"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	imageID, err := sdk.RequiredStringParam(params, paramImageID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	instanceType, err := sdk.RequiredStringParam(params, paramInstanceType)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	client, err := awscloud.ClientForAccount(rc, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := client.FindInstanceByName(ctx, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	changed := false
	after := before
	if before == nil {
		launched, err := client.RunInstance(ctx, name, imageID, instanceType)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		after = launched
	}

	if err := recordState(rc, after.ID, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "cloud.aws.ec2.terminate",
			Params:      map[string]any{paramInstanceID: after.ID},
			Description: fmt.Sprintf("Terminate %s, which this task launched.", after.ID),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

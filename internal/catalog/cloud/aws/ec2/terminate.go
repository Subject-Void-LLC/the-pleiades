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
		Name: "cloud.aws.ec2.terminate",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{capability.NameAWSAPI},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			// A check makes only the read a real run makes first (CheckTerminate).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A terminated instance's storage (unless an EBS volume was explicitly detached beforehand, which this method does not do) and identity are gone; there is nothing a cloud.aws.ec2.create could restore.",
			},
			Doc: terminateDoc(),
		},
		Invoke: Terminate,
		Check:  CheckTerminate,
	})
}

func terminateDoc() collection.Doc {
	return collection.Doc{
		Summary: "Terminates an EC2 instance via the AWS API.",
		Description: "Terminates the instance named by instance_id. A no-op if AWS has no record of that id " +
			"at all, or if it is already terminated. Unlike cloud.aws.ec2.create, this takes an exact " +
			"instance_id rather than a Name-tag lookup: terminating by a fuzzy match is a worse default than " +
			"requiring the exact resource for a destructive action.",
		Params: []collection.Param{
			{Name: paramInstanceID, Type: "string", Required: true, Description: "The instance id to terminate."},
		},
		Returns: []collection.ReturnField{
			{Name: statInstanceID, Type: "string", Returned: "always", Description: "The instance this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What the account reported about the instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Terminate an instance",
				RunbookYAML: "- name: Tear down the build agent\n  cloud.aws.ec2.terminate:\n    instance_id: i-0123456789abcdef0\n",
			},
		},
		SeeAlso: []string{"cloud.aws.ec2.create"},
	}
}

// Terminate implements "cloud.aws.ec2.terminate".
func Terminate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return terminate(ctx, rc, device, params, collection.ModeExecute)
}

// CheckTerminate is "cloud.aws.ec2.terminate"'s check: it reads the instance with DescribeInstances and says whether Terminate would
// terminate the instance, sending no call that changes anything.
func CheckTerminate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return terminate(ctx, rc, device, params, collection.ModeCheck)
}

// terminate is Terminate's and CheckTerminate's one body; mode says which.
func terminate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "cloud.aws.ec2.terminate"

	instanceID, err := sdk.RequiredStringParam(params, paramInstanceID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	client, err := awscloud.ClientForAccount(rc, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := client.DescribeInstance(ctx, instanceID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		return predictTerminate(rc, fqcn, instanceID, before)
	}

	changed := false
	after := before
	if before != nil && before.State != "terminated" {
		terminated, err := client.TerminateInstance(ctx, instanceID)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		after = terminated
	}

	if err := recordState(rc, instanceID, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}

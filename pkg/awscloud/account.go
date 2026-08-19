package awscloud

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Secret keys ClientForAccount reads from the RunbookContext.
//
// Credentials arrive through InjectSecrets rather than through task
// params, for the identical reason net.catalyst.*'s own client resolver
// documents: params come from a runbook file, and a runbook file is
// committed to version control. The names match
// internal/credtype/managed/types/aws.json's own field ids exactly
// (username = access key, password = secret key, security_token = an
// optional STS session token), so a device bound to that already-existing
// AWX-derived credential type needs no new credential type or injector
// wiring to reach the cloud.aws.* methods.
const (
	SecretAccessKeyID     = "username"
	SecretSecretAccessKey = "password"
	SecretSessionToken    = "security_token"
)

// endpointOverrider is satisfied by inventory/devices/aws.Account, but is
// deliberately its own small interface here rather than folded into
// capability.AWSAPICapable: an endpoint override is a test/LocalStack
// concern, not a capability a real AWS account has or lacks, so it has no
// business in the capability vocabulary a plan-time check reasons about.
// A device that does not implement it (any other AWSAPICapable type a
// future device package adds) simply always targets real AWS.
type endpointOverrider interface {
	AWSEndpointOverride() string
}

// ClientForAccount builds an authenticated Client for the AWS
// account/region a task targets.
//
// It lives here, in pkg/awscloud, rather than in a cloud.aws.* Collection
// package, because cloud.aws.ec2.* and cloud.aws.s3.* are separate Go
// packages (their FQCN's second segment maps to a separate directory,
// unlike net.catalyst.*'s single flat package) and
// internal/archtest.TestCatalogPackagesImportOnlyPkg forbids either from
// importing the other or a shared internal/ sibling: a Collection package
// may import only pkg/ and stdlib. pkg/sdk.Connect already establishes
// this exact shape for the SSH-transport methods (device + secrets in,
// a working connection out); this is that same contract for an
// AWS-API-addressed one.
//
// It reads the region from the device's own AWSAPICapable accessor
// rather than from a task parameter, mirroring
// net/catalyst/client.go's clientForDevice: the same runbook retargets a
// different account or region by changing which device it targets, with
// nothing in the runbook file itself to edit.
func ClientForAccount(rc sdk.RunbookContext, device inventory.InventoryItem, fqcn string) (*Client, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: cloud.aws methods address a specific AWS account, set the task's target", fqcn)
	}
	if !device.HasCapability(capability.NameAWSAPI) {
		return nil, fmt.Errorf("%s: device %q does not have %s", fqcn, device.Name(), capability.NameAWSAPI)
	}

	addressable, ok := device.(capability.AWSAPICapable)
	if !ok {
		// HasCapability already ANDs the structural assertion, so this is
		// unreachable through a correctly built device type. It is here
		// because the alternative to an explicit refusal is a panic.
		return nil, fmt.Errorf("%s: device %q declares %s but does not implement it", fqcn, device.Name(), capability.NameAWSAPI)
	}

	region := addressable.AWSRegion()
	if region == "" {
		return nil, fmt.Errorf("%s: device %q has no AWS region configured", fqcn, device.Name())
	}

	secrets := rc.InjectSecrets()
	accessKeyID := secrets[SecretAccessKeyID]
	if accessKeyID == "" {
		return nil, fmt.Errorf("%s: no %q secret available for device %q", fqcn, SecretAccessKeyID, device.Name())
	}

	var opts []Option
	if eo, ok := device.(endpointOverrider); ok {
		if endpoint := eo.AWSEndpointOverride(); endpoint != "" {
			opts = append(opts, WithEndpoint(endpoint))
		}
	}

	return New(region, accessKeyID, secrets[SecretSecretAccessKey], secrets[SecretSessionToken], opts...)
}

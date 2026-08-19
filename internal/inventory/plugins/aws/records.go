package aws

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Property keys this plugin writes onto every discovered record.
//
// host and ip are the two exceptions, deliberately unprefixed, mirroring
// catalystcenter/records.go's own reasoning exactly: they are the keys
// internal/transport/ssh and internal/api already read, so writing them
// under a plugin-specific name would produce a device nothing else can
// reach.
//
// region and endpointOverride are ALSO deliberately unprefixed, for a
// different but related reason: they are the exact property keys
// inventory/devices/aws.Account.AWSRegion and .AWSEndpointOverride read.
// The synthetic account record this plugin emits (accountRecord below)
// classifies as an aws_account, so it must write the same keys that
// concrete type's own accessors expect, or a synced account device would
// answer AWSRegion() with "" despite this plugin having connected to a
// real one.
const (
	propHost             = "host"
	propIP               = "ip"
	propRegion           = "region"
	propEndpointOverride = "endpoint_override"

	propRole             = "aws_role"
	propInstanceID       = "aws_instance_id"
	propInstanceType     = "aws_instance_type"
	propImageID          = "aws_image_id"
	propAvailabilityZone = "aws_availability_zone"
	propPlatform         = "aws_platform"
	propState            = "aws_state"
)

// roleAccount marks the synthetic record describing the AWS account/region
// itself, mirroring catalystcenter's roleController. roleInstance marks a
// real discovered EC2 instance, mirroring its roleManaged. Classify
// branches on this, which is why it is a constant rather than a string
// repeated across files.
const (
	roleAccount  = "account"
	roleInstance = "instance"
)

// accountRecord builds the Record describing the AWS account/region this
// plugin connected to.
//
// Emitting it is deliberate, not incidental: it means a sync leaves
// inventory in a state where cloud.aws.* tasks can run without a separate
// manual add-host step, the same "no separate manual step" reasoning
// catalystcenter's own controllerRecord states. endpoint carries through
// unchanged (empty for real AWS, a LocalStack URL in a test), so a device
// synced from a LocalStack-backed connection stays pointed at that same
// backend rather than silently retargeting real AWS.
func accountRecord(region, endpoint string) record.Record {
	props := map[string]inventory.PropertyValue{
		propRole:   roleAccount,
		propRegion: region,
	}
	if endpoint != "" {
		props[propEndpointOverride] = endpoint
	}
	return record.Record{
		// Derived from region, not anything AWS reports: an account has no
		// entry in its own EC2 inventory to take an ID from, and one
		// account/region pair should always resolve to the same DeviceID
		// across repeated syncs.
		ID:         inventory.DeviceID("aws-account:" + region),
		Name:       "aws-" + region,
		Properties: props,
	}
}

// instanceRecord converts one discovered EC2 instance into a
// storage-agnostic Record.
//
// Type, Capabilities, and State are deliberately left unset: Discover
// yields unclassified records and Classify is what fills those in, the
// same split catalystcenter's own deviceRecord documents.
func instanceRecord(inst awscloud.Instance) record.Record {
	host := inst.PublicIP
	if host == "" {
		// A stopped instance, or one with no public IP assigned, still
		// gets a real Record: the private IP is what is left to try, and
		// an instance with neither still syncs with an empty host rather
		// than this plugin inventing a placeholder. Whatever later tries
		// to reach it over SSH fails there, honestly.
		host = inst.PrivateIP
	}

	props := map[string]inventory.PropertyValue{
		propRole:       roleInstance,
		propInstanceID: inst.ID,
	}
	for key, value := range map[string]string{
		propHost:             host,
		propIP:               host,
		propInstanceType:     inst.InstanceType,
		propImageID:          inst.ImageID,
		propAvailabilityZone: inst.AvailabilityZone,
		propPlatform:         inst.Platform,
		propState:            inst.State,
	} {
		if value != "" {
			props[key] = value
		}
	}

	return record.Record{
		// The instance ID is the stable identifier, not the Name tag: a
		// tag is renameable and DeviceID must not be.
		ID:         inventory.DeviceID(inst.ID),
		Name:       instanceName(inst),
		Properties: props,
	}
}

// instanceName picks the display name for a discovered instance,
// preferring its Name tag and falling back to the instance ID. An
// instance with an empty Name tag would otherwise be unaddressable by
// name.
func instanceName(inst awscloud.Instance) string {
	if inst.Name != "" {
		return inst.Name
	}
	return inst.ID
}

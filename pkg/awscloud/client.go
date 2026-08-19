// Package awscloud is a minimal AWS API client, covering the EC2 and S3
// operations the cloud.aws.* Collection methods need.
//
// It lives under pkg/ rather than internal/ for a structural reason, not a
// visibility one, the same reason pkg/catalystcenter does: a built-in
// Collection method may import only pkg/ and stdlib
// (internal/archtest's TestCatalogPackagesImportOnlyPkg), so a client
// shared between the cloud.aws.* methods has to live here or be written
// twice.
//
// Unlike pkg/catalystcenter, this package does not hand-roll its HTTP
// client and auth. AWS request signing (SigV4) is security-critical
// cryptographic code, not a REST convenience layer, and reimplementing it
// is exactly the kind of homegrown auth code this codebase's own
// injection-hardening discipline warns against. This package wraps the
// official github.com/aws/aws-sdk-go-v2 instead, scoped to only the
// modules the four cloud.aws.* methods need (core, credentials,
// service/ec2, service/s3), and translates every response into this
// package's own small types so nothing under internal/catalog needs to
// import an AWS SDK type directly.
//
// Scope is deliberately narrow, matching every other batch's restraint:
// one instance per RunInstance call, no launch template support, no S3
// bucket policy/versioning/lifecycle configuration, no pagination beyond
// what a single DescribeInstances page returns. This is the same
// restraint container.docker.run and identity.user.* already applied
// against their own upstream's much larger surface.
package awscloud

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Client talks to one AWS account/region. The zero value is not usable;
// construct one with New.
type Client struct {
	ec2    *ec2.Client
	s3     *s3.Client
	region string
}

// options holds what the Option functions below configure, kept separate
// from Client itself so New can finish validating before anything is
// built, matching pkg/catalystcenter.Option's shape.
type options struct {
	endpoint string
}

// Option customizes a Client at construction time.
type Option func(*options)

// WithEndpoint redirects every AWS API call this Client makes to a
// non-AWS base URL (a LocalStack container, most often) instead of real
// AWS. The default, an empty string, targets real AWS.
//
// This exists for tests, and for nothing else: a real device
// (inventory/devices/aws.Account) exposes the identical override as its
// own AWSEndpointOverride property precisely so a runbook itself never
// carries this value, only inventory data an operator controls out of
// band does.
func WithEndpoint(url string) Option {
	return func(o *options) { o.endpoint = url }
}

// New builds a Client for one AWS account/region, authenticating with a
// static access key and secret key, plus an optional session token for
// temporary (STS) credentials.
//
// It deliberately does not use config.LoadDefaultConfig. That loader
// falls back through environment variables and ~/.aws/config on whatever
// machine runs the pleiades process, which is exactly the side-channel
// credential path this platform's SSH methods already reject: a
// credential belongs to the device (here, the aws.Account) and arrives
// through RunbookContext.InjectSecrets, never through ambient process
// state.
func New(region, accessKeyID, secretAccessKey, sessionToken string, opts ...Option) (*Client, error) {
	if region == "" {
		return nil, fmt.Errorf("awscloud: region is required")
	}
	if accessKeyID == "" {
		return nil, fmt.Errorf("awscloud: access key id is required")
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, sessionToken),
	}

	ec2Client := ec2.NewFromConfig(cfg, func(co *ec2.Options) {
		if o.endpoint != "" {
			co.BaseEndpoint = aws.String(o.endpoint)
			// Setting BaseEndpoint alone is not sufficient in this SDK
			// generation to fully override the resolved endpoint for
			// every operation; the endpoint resolver must be told to
			// honor it too, per
			// https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/endpoints/#with-both.
			// This delegates straight back to the default resolver
			// rather than reimplementing endpoint logic, so real AWS
			// behavior (the o.endpoint == "" path) is untouched.
			co.EndpointResolverV2 = ec2.NewDefaultEndpointResolverV2()
		}
	})
	s3Client := s3.NewFromConfig(cfg, func(co *s3.Options) {
		if o.endpoint != "" {
			co.BaseEndpoint = aws.String(o.endpoint)
			co.EndpointResolverV2 = s3.NewDefaultEndpointResolverV2()
			// Path-style addressing is what every non-AWS S3-compatible
			// endpoint (LocalStack included) expects: virtual-hosted
			// style requires a real DNS wildcard for
			// bucket.endpoint-host, which a local container does not
			// have. Left unset (false) against real AWS, where
			// virtual-hosted style is the modern default.
			co.UsePathStyle = true
		}
	})

	return &Client{ec2: ec2Client, s3: s3Client, region: region}, nil
}

// Instance is the subset of an EC2 instance's state this package's callers
// need: enough for cloud.aws.ec2.* to decide idempotency and record a
// diff, and enough for the "aws" inventory sync plugin to place a
// discovered instance in inventory as an ordinary linux_server, not a
// mirror of the SDK's much larger Instance type.
type Instance struct {
	ID               string
	State            string // pending | running | shutting-down | terminated | stopping | stopped
	Name             string // the Name tag, "" if unset
	PublicIP         string // "" if the instance has none (stopped, or no public IP assigned)
	PrivateIP        string
	InstanceType     string
	ImageID          string
	AvailabilityZone string
	Platform         string // "windows" for a Windows instance, "" for Linux (EC2 reports nothing else)
}

func instanceFrom(i ec2types.Instance) *Instance {
	inst := &Instance{
		ID:           aws.ToString(i.InstanceId),
		PublicIP:     aws.ToString(i.PublicIpAddress),
		PrivateIP:    aws.ToString(i.PrivateIpAddress),
		InstanceType: string(i.InstanceType),
		ImageID:      aws.ToString(i.ImageId),
	}
	if i.State != nil {
		inst.State = string(i.State.Name)
	}
	if i.Placement != nil {
		inst.AvailabilityZone = aws.ToString(i.Placement.AvailabilityZone)
	}
	if i.Platform == ec2types.PlatformValuesWindows {
		inst.Platform = "windows"
	}
	for _, tag := range i.Tags {
		if aws.ToString(tag.Key) == "Name" {
			inst.Name = aws.ToString(tag.Value)
			break
		}
	}
	return inst
}

// activeInstanceStates excludes "terminated": a terminated instance still
// shows up in DescribeInstances for a while, and FindInstanceByName
// treats one as absent so a create task launches a fresh instance rather
// than reporting no-op against a dead one.
var activeInstanceStates = []string{"pending", "running", "shutting-down", "stopping", "stopped"}

// FindInstanceByName returns the first non-terminated instance tagged
// Name=name, or nil if none exists. This is the query
// cloud.aws.ec2.create's idempotency check runs before ever calling
// RunInstance.
func (c *Client) FindInstanceByName(ctx context.Context, name string) (*Instance, error) {
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{name}},
			{Name: aws.String("instance-state-name"), Values: activeInstanceStates},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("awscloud: describing instances tagged %q: %w", name, err)
	}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			return instanceFrom(i), nil
		}
	}
	return nil, nil
}

// ListInstancesPage returns one page of every non-terminated instance in
// this Client's account/region, starting after nextToken ("" for the
// first page), and the token to pass for the next page ("" once there is
// no more).
//
// This is a thin pass-through of DescribeInstances' own NextToken/
// MaxResults pagination, not a design choice this package invented: the
// "aws" inventory sync plugin's own iterator pulls exactly one page at a
// time (mirroring net.catalyst.*'s deviceIterator) so memory stays flat
// regardless of fleet size, and EC2's token shape is what that pull has
// to drive.
func (c *Client) ListInstancesPage(ctx context.Context, nextToken string, maxResults int32) ([]Instance, string, error) {
	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: activeInstanceStates},
		},
		MaxResults: aws.Int32(maxResults),
	}
	if nextToken != "" {
		input.NextToken = aws.String(nextToken)
	}

	out, err := c.ec2.DescribeInstances(ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("awscloud: listing instances: %w", err)
	}

	var instances []Instance
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			instances = append(instances, *instanceFrom(i))
		}
	}
	return instances, aws.ToString(out.NextToken), nil
}

// RunInstance launches exactly one instance of instanceType from imageID,
// tagged Name=name, and returns it.
func (c *Client) RunInstance(ctx context.Context, name, imageID, instanceType string) (*Instance, error) {
	out, err := c.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: ec2types.InstanceType(instanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String(name)}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("awscloud: launching instance %q: %w", name, err)
	}
	if len(out.Instances) == 0 {
		return nil, fmt.Errorf("awscloud: RunInstances for %q returned no instances", name)
	}
	return instanceFrom(out.Instances[0]), nil
}

// DescribeInstance returns instanceID's current state, or nil if AWS no
// longer has any record of it at all (as opposed to "terminated", which
// is still a real, returned state for a time after termination).
func (c *Client) DescribeInstance(ctx context.Context, instanceID string) (*Instance, error) {
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		if errorCode(err) == "InvalidInstanceID.NotFound" {
			return nil, nil
		}
		return nil, fmt.Errorf("awscloud: describing instance %q: %w", instanceID, err)
	}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			return instanceFrom(i), nil
		}
	}
	return nil, nil
}

// TerminateInstance terminates instanceID and returns its state
// immediately after the call (ordinarily "shutting-down", not yet
// "terminated" — termination is asynchronous on AWS's side).
func (c *Client) TerminateInstance(ctx context.Context, instanceID string) (*Instance, error) {
	out, err := c.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return nil, fmt.Errorf("awscloud: terminating instance %q: %w", instanceID, err)
	}
	if len(out.TerminatingInstances) == 0 {
		return nil, fmt.Errorf("awscloud: TerminateInstances for %q returned no result", instanceID)
	}
	change := out.TerminatingInstances[0]
	inst := &Instance{ID: instanceID}
	if change.CurrentState != nil {
		inst.State = string(change.CurrentState.Name)
	}
	return inst, nil
}

// BucketExists reports whether bucket exists and this account can see it.
func (c *Client) BucketExists(ctx context.Context, bucket string) (bool, error) {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return true, nil
	}
	switch errorCode(err) {
	case "NotFound", "NoSuchBucket":
		return false, nil
	}
	return false, fmt.Errorf("awscloud: checking bucket %q: %w", bucket, err)
}

// CreateBucket creates bucket in this Client's region.
//
// S3 treats us-east-1 specially: passing a LocationConstraint of
// "us-east-1" explicitly is itself an error (AWS's API predates
// us-east-1 needing one), so the constraint is only sent for every other
// region. Getting this wrong silently creates the bucket in us-east-1
// regardless of the client's configured region, which is the kind of
// wrong-region surprise that only shows up once something else expects
// the bucket where it was asked to be.
func (c *Client) CreateBucket(ctx context.Context, bucket string) error {
	input := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	if c.region != "us-east-1" {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(c.region),
		}
	}
	if _, err := c.s3.CreateBucket(ctx, input); err != nil {
		return fmt.Errorf("awscloud: creating bucket %q: %w", bucket, err)
	}
	return nil
}

// DeleteBucket deletes bucket. AWS itself refuses when the bucket is not
// empty; this method does not empty it first. That is deliberate scope,
// not an oversight: recursively deleting every object in a bucket is a
// destructive, unbounded operation this method's own name does not
// promise, and AWS's refusal is the safety rail, not an error to route
// around.
func (c *Client) DeleteBucket(ctx context.Context, bucket string) error {
	if _, err := c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return fmt.Errorf("awscloud: deleting bucket %q: %w", bucket, err)
	}
	return nil
}

// errorCode extracts the AWS API error code (e.g. "NoSuchBucket",
// "InvalidInstanceID.NotFound") from err, or "" if err is not an AWS API
// error at all (a connection failure, a context cancellation, and so
// on) — those are real errors this package never mistakes for "the
// resource is absent".
func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

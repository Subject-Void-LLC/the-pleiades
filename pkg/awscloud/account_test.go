package awscloud_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

const testFQCN = "cloud.aws.ec2.create"

// ctxStub is a minimal sdk.RunbookContext, matching every other batch's
// own test harness shape.
type ctxStub struct {
	secrets map[string]string
}

func (c *ctxStub) InjectSecrets() map[string]string     { return c.secrets }
func (c *ctxStub) SetStat(key string, value any) error  { return nil }
func (c *ctxStub) EmitFact(key string, value any) error { return nil }

// awsAccount wraps inventorytest.Stub with the two accessors
// inventory/devices/aws.Account provides, so tests can build one without
// depending on that concrete package (which would need registering with
// record.RegisterType, a heavier fixture than these tests need).
type awsAccount struct {
	*inventorytest.Stub
	region   string
	endpoint string
}

func (a *awsAccount) AWSRegion() string           { return a.region }
func (a *awsAccount) AWSEndpointOverride() string { return a.endpoint }

func newAWSAccount(region, endpoint string) *awsAccount {
	return &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "test-account", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   region,
		endpoint: endpoint,
	}
}

func TestClientForAccount_RequiresDevice(t *testing.T) {
	_, err := awscloud.ClientForAccount(&ctxStub{}, nil, testFQCN)
	if err == nil {
		t.Error("ClientForAccount with a nil device: got nil error, want one")
	}
}

func TestClientForAccount_RequiresCapability(t *testing.T) {
	device := &inventorytest.Stub{StubName: "no-aws-api"}
	_, err := awscloud.ClientForAccount(&ctxStub{}, device, testFQCN)
	if err == nil {
		t.Error("ClientForAccount against a device lacking AWSAPICapable: got nil error, want one")
	}
}

func TestClientForAccount_RequiresRegion(t *testing.T) {
	device := newAWSAccount("", "")
	rc := &ctxStub{secrets: map[string]string{awscloud.SecretAccessKeyID: "AKIA...", awscloud.SecretSecretAccessKey: "secret"}}
	_, err := awscloud.ClientForAccount(rc, device, testFQCN)
	if err == nil {
		t.Error("ClientForAccount against a device with no region: got nil error, want one")
	}
}

func TestClientForAccount_RequiresAccessKeySecret(t *testing.T) {
	device := newAWSAccount("us-east-1", "")
	_, err := awscloud.ClientForAccount(&ctxStub{}, device, testFQCN)
	if err == nil {
		t.Error("ClientForAccount with no username secret: got nil error, want one")
	}
}

func TestClientForAccount_Succeeds(t *testing.T) {
	device := newAWSAccount("us-east-1", "")
	rc := &ctxStub{secrets: map[string]string{awscloud.SecretAccessKeyID: "AKIA...", awscloud.SecretSecretAccessKey: "secret"}}
	c, err := awscloud.ClientForAccount(rc, device, testFQCN)
	if err != nil {
		t.Fatalf("ClientForAccount: %v", err)
	}
	if c == nil {
		t.Fatal("ClientForAccount returned a nil Client alongside a nil error")
	}
}

func TestClientForAccount_HonorsEndpointOverride(t *testing.T) {
	device := newAWSAccount("us-east-1", "http://127.0.0.1:1")
	rc := &ctxStub{secrets: map[string]string{awscloud.SecretAccessKeyID: "AKIA...", awscloud.SecretSecretAccessKey: "secret"}}
	c, err := awscloud.ClientForAccount(rc, device, testFQCN)
	if err != nil {
		t.Fatalf("ClientForAccount: %v", err)
	}
	if _, err := c.BucketExists(t.Context(), "whatever"); err == nil {
		t.Error("BucketExists against the overridden (unreachable) endpoint: got nil error, want one")
	}
}

// TestClientForAccount_DeclaredButNotStructurallyImplemented exercises the
// one branch a correctly built device type can never reach:
// inventorytest.Stub.HasCapability answers from its declared Caps list
// alone, with no structural assertion (unlike every real device type's
// own HasCapability, which ANDs the two), so a bare Stub declaring
// NameAWSAPI passes the capability check while still failing the type
// assertion to capability.AWSAPICapable right after, since Stub has no
// AWSRegion method at all. ClientForAccount must fail closed there
// rather than panic.
func TestClientForAccount_DeclaredButNotStructurallyImplemented(t *testing.T) {
	device := &inventorytest.Stub{StubName: "bare", Caps: []capability.Name{capability.NameAWSAPI}}
	rc := &ctxStub{secrets: map[string]string{awscloud.SecretAccessKeyID: "AKIA...", awscloud.SecretSecretAccessKey: "secret"}}
	_, err := awscloud.ClientForAccount(rc, device, testFQCN)
	if err == nil {
		t.Error("ClientForAccount against a Stub declaring the capability without implementing it: got nil error, want one")
	}
}

var _ inventory.InventoryItem = (*awsAccount)(nil)

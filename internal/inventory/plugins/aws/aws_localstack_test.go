package aws_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	awsplugin "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// These run against a real LocalStack container, the same reasoning and
// harness shape as pkg/awscloud's and cloud.aws.{ec2,s3}'s own tests: this
// plugin addresses the real AWS HTTP API, so there is no fake shell
// script or hand-rolled httptest fake that would be representative here.

const (
	testAccessKeyID     = "test"
	testSecretAccessKey = "test"
	testRegion          = "us-east-1"
)

// staticStore is a credential.Store returning one fixed credential,
// mirroring catalystcenter_test.go's own test double exactly.
type staticStore struct{ cred credential.Credential }

func (s staticStore) Lookup(context.Context, string) (credential.Credential, error) {
	return s.cred, nil
}

var (
	sharedContainerOnce sync.Once
	sharedContainerErr  error
	sharedContainer     *localstack.LocalStackContainer
	sharedEndpoint      string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedContainer != nil {
		_ = sharedContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

func requireLocalStack(tb testing.TB) string {
	tb.Helper()
	token := os.Getenv("LOCALSTACK_AUTH_TOKEN")
	if token == "" {
		tb.Skip("set LOCALSTACK_AUTH_TOKEN to run the aws sync plugin's real-LocalStack-backed tests")
	}
	sharedContainerOnce.Do(func() {
		ctx := context.Background()
		ctr, err := localstack.Run(ctx, testsupport.LocalStackImage,
			testcontainers.WithEnv(map[string]string{"LOCALSTACK_AUTH_TOKEN": token}),
		)
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to start localstack container: %w", err)
			return
		}
		sharedContainer = ctr

		mappedPort, err := ctr.MappedPort(ctx, "4566/tcp")
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to get mapped port: %w", err)
			return
		}
		host, err := ctr.Host(ctx)
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to get container host: %w", err)
			return
		}
		sharedEndpoint = fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
	})
	if sharedContainerErr != nil {
		tb.Fatalf("shared LocalStack container setup failed: %v", sharedContainerErr)
	}
	return sharedEndpoint
}

// connect builds a plugin pointed at the shared LocalStack container and
// connects it, mirroring catalystcenter_test.go's own connect helper.
func connect(t *testing.T) *awsplugin.Aws {
	t.Helper()
	endpoint := requireLocalStack(t)

	p := awsplugin.New(
		awsplugin.WithRegion(testRegion),
		awsplugin.WithCredentialStore(staticStore{credential.Credential{Username: testAccessKeyID, Password: testSecretAccessKey}}),
	)
	t.Cleanup(func() { _ = p.Close() })

	cfg := syncplugin.Config{Name: awsplugin.Name, Endpoint: endpoint, ReadOnly: true}
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return p
}

// rawClient builds a pkg/awscloud.Client pointed at the same shared
// container, for fixtures this test launches directly rather than through
// the plugin under test.
func rawClient(t *testing.T) *awscloud.Client {
	t.Helper()
	endpoint := requireLocalStack(t)
	c, err := awscloud.New(testRegion, testAccessKeyID, testSecretAccessKey, "", awscloud.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("awscloud.New: %v", err)
	}
	return c
}

func newRepo(t *testing.T) inv.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating inventory: %v", err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

// ---------- Connect ----------

func TestConnect_RequiresRegion(t *testing.T) {
	p := awsplugin.New(awsplugin.WithCredentialStore(staticStore{}))
	err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name})
	if err == nil {
		t.Error("Connect with no region configured: got nil error, want one")
	}
}

func TestConnect_RequiresCredentialStore(t *testing.T) {
	p := awsplugin.New(awsplugin.WithRegion(testRegion))
	err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name})
	if err == nil {
		t.Error("Connect with no credential store configured: got nil error, want one")
	}
}

func TestConnect_InvalidConfig(t *testing.T) {
	p := awsplugin.New(awsplugin.WithRegion(testRegion), awsplugin.WithCredentialStore(staticStore{}))
	// Config.Validate requires a non-empty Name; this Config has none.
	if err := p.Connect(context.Background(), syncplugin.Config{}); err == nil {
		t.Error("Connect with an invalid Config: got nil error, want one")
	}
}

// failingStore is a credential.Store that always fails to resolve.
type failingStore struct{ err error }

func (s failingStore) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{}, s.err
}

func TestConnect_CredentialLookupFailure(t *testing.T) {
	p := awsplugin.New(awsplugin.WithRegion(testRegion), awsplugin.WithCredentialStore(failingStore{err: fmt.Errorf("no such credential")}))
	if err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name}); err == nil {
		t.Error("Connect with a failing credential store: got nil error, want one")
	}
}

func TestConnect_EmptyCredentialUsername(t *testing.T) {
	p := awsplugin.New(awsplugin.WithRegion(testRegion), awsplugin.WithCredentialStore(staticStore{credential.Credential{}}))
	if err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name}); err == nil {
		t.Error("Connect with a credential carrying an empty access key: got nil error, want one")
	}
}

func TestConnect_Succeeds(t *testing.T) {
	p := connect(t)
	if err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name, Endpoint: requireLocalStack(t), ReadOnly: true}); err != nil {
		t.Errorf("second Connect: %v (Connect must be safe to call more than once)", err)
	}
}

func TestConnect_UnreachableEndpoint(t *testing.T) {
	requireLocalStack(t) // still gate on the token, even though this test never reaches the container
	p := awsplugin.New(
		awsplugin.WithRegion(testRegion),
		awsplugin.WithCredentialStore(staticStore{credential.Credential{Username: testAccessKeyID, Password: testSecretAccessKey}}),
	)
	err := p.Connect(context.Background(), syncplugin.Config{Name: awsplugin.Name, Endpoint: "http://127.0.0.1:1"})
	if err == nil {
		t.Error("Connect against an unreachable endpoint: got nil error, want one")
	}
}

// ---------- Discover ----------

func TestDiscover_NotConnected(t *testing.T) {
	p := awsplugin.New()
	if _, err := p.Discover(context.Background()); err != syncplugin.ErrNotConnected {
		t.Errorf("Discover before Connect: err = %v, want ErrNotConnected", err)
	}
}

func TestDiscover_YieldsAccountThenInstances(t *testing.T) {
	c := rawClient(t)
	ctx := context.Background()
	name := "pleiades-test-" + t.Name()
	launched, err := c.RunInstance(ctx, name, "ami-12345678", "t2.micro")
	if err != nil {
		t.Fatalf("RunInstance: %v", err)
	}

	p := connect(t)
	it, err := p.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	defer func() { _ = it.Close() }()

	if !it.Next(ctx) {
		t.Fatal("Discover's iterator yielded nothing; want the account record first")
	}
	first := it.Record()
	if first.Properties["aws_role"] != "account" {
		t.Errorf("first record's aws_role = %v, want %q", first.Properties["aws_role"], "account")
	}
	if first.Properties["region"] != testRegion {
		t.Errorf("first record's region = %v, want %q", first.Properties["region"], testRegion)
	}

	found := false
	for it.Next(ctx) {
		rec := it.Record()
		if rec.ID == inventory.DeviceID(launched.ID) {
			found = true
			if rec.Properties["aws_role"] != "instance" {
				t.Errorf("instance record's aws_role = %v, want %q", rec.Properties["aws_role"], "instance")
			}
			if rec.Properties["host"] == "" || rec.Properties["host"] == nil {
				t.Error("instance record has no host property")
			}
		}
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iteration ended with error: %v", err)
	}
	if !found {
		t.Errorf("Discover never yielded the instance %s this test just launched", launched.ID)
	}
}

// ---------- Classify ----------
//
// These need no live connection: Classify reads only the record it is
// given and the rule set New() always builds, so they run even without
// LOCALSTACK_AUTH_TOKEN set.

func TestClassify_Account(t *testing.T) {
	p := awsplugin.New()
	cls, err := p.Classify(context.Background(), record.Record{
		Properties: map[string]inventory.PropertyValue{"aws_role": "account", "region": testRegion},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if cls.Quarantined() {
		t.Fatalf("account record quarantined: %+v", cls)
	}
	if cls.Type != "aws_account" {
		t.Errorf("Type = %q, want %q", cls.Type, "aws_account")
	}
}

func TestClassify_LinuxInstance(t *testing.T) {
	p := awsplugin.New()
	cls, err := p.Classify(context.Background(), record.Record{
		Properties: map[string]inventory.PropertyValue{"aws_role": "instance", "aws_instance_id": "i-abc"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if cls.Quarantined() {
		t.Fatalf("Linux instance quarantined: %+v", cls)
	}
	if cls.Type != "linux_server" {
		t.Errorf("Type = %q, want %q", cls.Type, "linux_server")
	}
}

func TestClassify_WindowsInstance(t *testing.T) {
	p := awsplugin.New()
	cls, err := p.Classify(context.Background(), record.Record{
		Properties: map[string]inventory.PropertyValue{"aws_role": "instance", "aws_platform": "windows"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if cls.Quarantined() {
		t.Fatalf("Windows instance quarantined: %+v", cls)
	}
	if cls.Type != "windows_server" {
		t.Errorf("Type = %q, want %q", cls.Type, "windows_server")
	}
}

// TestClassify_UnrecognizedPlatform_Quarantines covers the one platform
// value this plugin cannot classify: anything other than "" (Linux) or
// "windows", which EC2's DescribeInstances does not define today but a
// future AWS API change could. It must quarantine rather than guess, the
// same restraint TestClassify_UnrecognizedRole_Quarantines below asserts
// for an unrecognized aws_role.
func TestClassify_UnrecognizedPlatform_Quarantines(t *testing.T) {
	p := awsplugin.New()
	cls, err := p.Classify(context.Background(), record.Record{
		Properties: map[string]inventory.PropertyValue{"aws_role": "instance", "aws_platform": "some-future-platform"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !cls.Quarantined() {
		t.Error("an unrecognized instance platform was not quarantined, want it to be")
	}
	if cls.Reason == "" {
		t.Error("a quarantined classification must explain why")
	}
}

func TestClassify_UnrecognizedRole_Quarantines(t *testing.T) {
	p := awsplugin.New()
	cls, err := p.Classify(context.Background(), record.Record{Properties: map[string]inventory.PropertyValue{}})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !cls.Quarantined() {
		t.Error("a record with no recognized aws_role was not quarantined, want it to be")
	}
}

// ---------- Sync ----------

func TestSync_NotConnected(t *testing.T) {
	p := awsplugin.New()
	if _, err := p.Sync(context.Background(), nil); err != syncplugin.ErrNotConnected {
		t.Errorf("Sync before Connect: err = %v, want ErrNotConnected", err)
	}
}

func TestSync_RoundTripsRealInstances(t *testing.T) {
	c := rawClient(t)
	ctx := context.Background()
	name := "pleiades-test-" + t.Name()
	if _, err := c.RunInstance(ctx, name, "ami-12345678", "t2.micro"); err != nil {
		t.Fatalf("RunInstance: %v", err)
	}

	p := connect(t)
	repo := newRepo(t)
	result, err := p.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if result.Total() == 0 {
		t.Fatal("Sync reconciled zero devices")
	}
	if result.Count(syncplugin.OutcomeAdded) == 0 {
		t.Error("Sync added zero devices, want at least the account plus the launched instance")
	}

	item, err := repo.GetByName(ctx, name)
	if err != nil {
		t.Fatalf("GetByName(%s): %v", name, err)
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Errorf("synced instance does not have %s", capability.NameLinux)
	}
	host, _ := item.Properties().String("host")
	if host == "" {
		t.Error("synced instance has no host property")
	}
}

// ---------- Close ----------

func TestClose_IdempotentAndSafeUnconnected(t *testing.T) {
	p := awsplugin.New()
	if err := p.Close(); err != nil {
		t.Errorf("Close on an unconnected plugin: %v, want nil", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
}

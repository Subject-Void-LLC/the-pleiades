package ec2_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	ec2mod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/cloud/aws/ec2"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// These run against a real LocalStack container (no fake shell script is
// possible here: cloud.aws.ec2.* addresses the AWS HTTP API directly, not
// a device transport). See pkg/awscloud's own tests for why LocalStack
// needs LOCALSTACK_AUTH_TOKEN and how these tests skip without one.

// ---------- harness ----------

type ctxStub struct {
	secrets   map[string]string
	stats     map[string]any
	failOnKey string
}

func (c *ctxStub) InjectSecrets() map[string]string { return c.secrets }
func (c *ctxStub) SetStat(key string, value any) error {
	if c.failOnKey != "" && key == c.failOnKey {
		return fmt.Errorf("ctxStub: injected failure recording %q", key)
	}
	c.stats[key] = value
	return nil
}
func (c *ctxStub) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// awsAccount wraps inventorytest.Stub with the two accessors
// inventory/devices/aws.Account provides.
type awsAccount struct {
	*inventorytest.Stub
	region   string
	endpoint string
}

func (a *awsAccount) AWSRegion() string           { return a.region }
func (a *awsAccount) AWSEndpointOverride() string { return a.endpoint }

var _ inventory.InventoryItem = (*awsAccount)(nil)

func noAWSDevice() inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-aws-api"}
}

const (
	testAccessKeyID     = "test"
	testSecretAccessKey = "test"
)

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
		tb.Skip("set LOCALSTACK_AUTH_TOKEN to run cloud.aws.ec2's real-LocalStack-backed tests")
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

// harness bundles a device pointed at the shared LocalStack container and
// a context stub, and gives every test its own unique instance Name so
// tests never see each other's launched instances.
type harness struct {
	rc     *ctxStub
	device *awsAccount
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	endpoint := requireLocalStack(t)
	return &harness{
		rc: &ctxStub{
			secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey},
			stats:   map[string]any{},
		},
		device: &awsAccount{
			Stub:     &inventorytest.Stub{StubName: "test-account", Caps: []capability.Name{capability.NameAWSAPI}},
			region:   "us-east-1",
			endpoint: endpoint,
		},
	}
}

func uniqueName(t *testing.T) string { return "pleiades-test-" + t.Name() }

// ---------- registration ----------

func lookup(t *testing.T, fqcn string) collection.Descriptor {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	return desc
}

func TestRegistration(t *testing.T) {
	for _, fqcn := range []string{"cloud.aws.ec2.create", "cloud.aws.ec2.terminate"} {
		desc := lookup(t, fqcn)
		if desc.Manifest.Status != collection.StatusImplemented {
			t.Errorf("%s: Status = %v, want StatusImplemented", fqcn, desc.Manifest.Status)
		}
		if desc.Invoke == nil {
			t.Errorf("%s: Invoke is nil", fqcn)
		}
		if desc.Manifest.Doc.Summary == "" {
			t.Errorf("%s: Doc.Summary is empty", fqcn)
		}
		if len(desc.Manifest.SupportedTransports) != 0 {
			t.Errorf("%s: SupportedTransports = %v, want empty (this namespace addresses the AWS API, not a device transport)", fqcn, desc.Manifest.SupportedTransports)
		}
	}
	if !lookup(t, "cloud.aws.ec2.create").Manifest.Reversibility.Reversible {
		t.Error("cloud.aws.ec2.create: expected Reversible: true")
	}
	if lookup(t, "cloud.aws.ec2.terminate").Manifest.Reversibility.Reversible {
		t.Error("cloud.aws.ec2.terminate: expected Reversible: false")
	}
}

// ---------- Create ----------

func TestCreate_NoDeviceCapability(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := ec2mod.Create(context.Background(), rc, noAWSDevice(), map[string]any{"name": "x", "image_id": "ami-1", "instance_type": "t2.micro"})
	if err == nil {
		t.Error("Create against a device lacking AWSAPICapable: got nil error, want one")
	}
}

func TestCreate_MissingRequiredParams(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	device := &awsAccount{Stub: &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}}, region: "us-east-1"}
	for _, params := range []map[string]any{
		{"image_id": "ami-1", "instance_type": "t2.micro"},
		{"name": "x", "instance_type": "t2.micro"},
		{"name": "x", "image_id": "ami-1"},
	} {
		if _, err := ec2mod.Create(context.Background(), rc, device, params); err == nil {
			t.Errorf("Create with params %v missing a required key: got nil error, want one", params)
		}
	}
}

func TestCreate_LaunchesWhenAbsent(t *testing.T) {
	h := newHarness(t)
	name := uniqueName(t)

	result, err := ec2mod.Create(context.Background(), h.rc, h.device, map[string]any{
		"name": name, "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !result.Changed {
		t.Error("Create launching a fresh instance: Changed = false, want true")
	}
	if h.rc.stats["instance_id"] == "" || h.rc.stats["instance_id"] == nil {
		t.Error("Create: instance_id stat not recorded")
	}
	if _, ok := h.rc.stats["inverse"]; !ok {
		t.Error("Create that launched a fresh instance: no inverse recorded")
	}
}

func TestCreate_IdempotentWhenPresent(t *testing.T) {
	h := newHarness(t)
	name := uniqueName(t)
	ctx := context.Background()

	first, err := ec2mod.Create(ctx, h.rc, h.device, map[string]any{
		"name": name, "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	firstID := h.rc.stats["instance_id"]

	h.rc.stats = map[string]any{}
	second, err := ec2mod.Create(ctx, h.rc, h.device, map[string]any{
		"name": name, "image_id": "ami-99999999", "instance_type": "t2.large",
	})
	if err != nil {
		t.Fatalf("Create (second): %v", err)
	}
	if second.Changed {
		t.Error("Create against an already-present Name tag: Changed = true, want false")
	}
	if _, ok := h.rc.stats["inverse"]; ok {
		t.Error("Create that changed nothing: an inverse was recorded, want none")
	}
	if h.rc.stats["instance_id"] != firstID {
		t.Errorf("Create (second).instance_id = %v, want the first call's %v (must not have relaunched)", h.rc.stats["instance_id"], firstID)
	}
	_ = first
}

func TestCreate_InstanceIDStatFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "instance_id"
	_, err := ec2mod.Create(context.Background(), h.rc, h.device, map[string]any{
		"name": uniqueName(t), "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err == nil {
		t.Error("Create with SetStat(\"instance_id\") injected to fail: got nil error, want one")
	}
}

func TestCreate_DiffFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "diff"
	_, err := ec2mod.Create(context.Background(), h.rc, h.device, map[string]any{
		"name": uniqueName(t), "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err == nil {
		t.Error("Create with SetStat(\"diff\") injected to fail: got nil error, want one")
	}
}

func TestCreate_InverseFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "inverse"
	_, err := ec2mod.Create(context.Background(), h.rc, h.device, map[string]any{
		"name": uniqueName(t), "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err == nil {
		t.Error("Create with SetStat(\"inverse\") injected to fail: got nil error, want one")
	}
}

func TestCreate_ConnectionFailure(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
	device := &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   "us-east-1",
		endpoint: "http://127.0.0.1:1",
	}
	_, err := ec2mod.Create(context.Background(), rc, device, map[string]any{
		"name": "x", "image_id": "ami-12345678", "instance_type": "t2.micro",
	})
	if err == nil {
		t.Error("Create against an unreachable endpoint: got nil error, want one")
	}
}

// ---------- Terminate ----------

func TestTerminate_NoDeviceCapability(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := ec2mod.Terminate(context.Background(), rc, noAWSDevice(), map[string]any{"instance_id": "i-whatever"})
	if err == nil {
		t.Error("Terminate against a device lacking AWSAPICapable: got nil error, want one")
	}
}

func TestTerminate_MissingRequiredParam(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	device := &awsAccount{Stub: &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}}, region: "us-east-1"}
	if _, err := ec2mod.Terminate(context.Background(), rc, device, map[string]any{}); err == nil {
		t.Error("Terminate with no instance_id: got nil error, want one")
	}
}

func TestTerminate_NoOpWhenAbsent(t *testing.T) {
	h := newHarness(t)
	result, err := ec2mod.Terminate(context.Background(), h.rc, h.device, map[string]any{"instance_id": "i-0000000000000dead"})
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if result.Changed {
		t.Error("Terminate on an unknown instance id: Changed = true, want false")
	}
}

func TestTerminate_LaunchedInstance(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	name := uniqueName(t)

	if _, err := ec2mod.Create(ctx, h.rc, h.device, map[string]any{
		"name": name, "image_id": "ami-12345678", "instance_type": "t2.micro",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	instanceID, _ := h.rc.stats["instance_id"].(string)
	if instanceID == "" {
		t.Fatal("Create did not record an instance_id")
	}

	h.rc.stats = map[string]any{}
	result, err := ec2mod.Terminate(ctx, h.rc, h.device, map[string]any{"instance_id": instanceID})
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !result.Changed {
		t.Error("Terminate on a live instance: Changed = false, want true")
	}

	// A second terminate against the same, now-terminated instance must
	// be a no-op rather than erroring.
	h.rc.stats = map[string]any{}
	again, err := ec2mod.Terminate(ctx, h.rc, h.device, map[string]any{"instance_id": instanceID})
	if err != nil {
		t.Fatalf("Terminate (again): %v", err)
	}
	if again.Changed {
		t.Error("Terminate on an already-terminated instance: Changed = true, want false")
	}
}

func TestTerminate_DiffFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "diff"
	_, err := ec2mod.Terminate(context.Background(), h.rc, h.device, map[string]any{"instance_id": "i-0000000000000dead"})
	if err == nil {
		t.Error("Terminate with SetStat(\"diff\") injected to fail: got nil error, want one")
	}
}

func TestTerminate_ConnectionFailure(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
	device := &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   "us-east-1",
		endpoint: "http://127.0.0.1:1",
	}
	if _, err := ec2mod.Terminate(context.Background(), rc, device, map[string]any{"instance_id": "i-whatever"}); err == nil {
		t.Error("Terminate against an unreachable endpoint: got nil error, want one")
	}
}

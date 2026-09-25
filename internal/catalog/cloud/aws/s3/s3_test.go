package s3_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	s3mod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/cloud/aws/s3"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// These run against a real LocalStack container (no fake shell script is
// possible here: cloud.aws.s3.* addresses the AWS HTTP API directly, not
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
		tb.Skip("set LOCALSTACK_AUTH_TOKEN to run cloud.aws.s3's real-LocalStack-backed tests")
	}
	sharedContainerOnce.Do(func() {
		ctx := context.Background()
		ctr, err := localstack.Run(ctx, testsupport.LocalStackImage,
			testcontainers.WithEnv(map[string]string{"LOCALSTACK_AUTH_TOKEN": token}),
			testsupport.LocalStackReady(),
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
// a context stub.
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

// uniqueBucket returns a bucket name derived from the test name. S3
// bucket names must be lowercase with no underscores, unlike a Go test
// name, so this lowercases and replaces "_" with "-".
func uniqueBucket(t *testing.T) string {
	b := []byte("pleiades-test-" + t.Name())
	out := make([]byte, 0, len(b))
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c == '_' || c == '/':
			out = append(out, '-')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

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
	for _, fqcn := range []string{"cloud.aws.s3.create_bucket", "cloud.aws.s3.delete_bucket"} {
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
	if !lookup(t, "cloud.aws.s3.create_bucket").Manifest.Reversibility.Reversible {
		t.Error("cloud.aws.s3.create_bucket: expected Reversible: true")
	}
	if lookup(t, "cloud.aws.s3.delete_bucket").Manifest.Reversibility.Reversible {
		t.Error("cloud.aws.s3.delete_bucket: expected Reversible: false")
	}
}

// ---------- CreateBucket ----------

func TestCreateBucket_NoDeviceCapability(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := s3mod.CreateBucket(context.Background(), rc, noAWSDevice(), map[string]any{"bucket": "x"})
	if err == nil {
		t.Error("CreateBucket against a device lacking AWSAPICapable: got nil error, want one")
	}
}

func TestCreateBucket_MissingRequiredParam(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	device := &awsAccount{Stub: &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}}, region: "us-east-1"}
	if _, err := s3mod.CreateBucket(context.Background(), rc, device, map[string]any{}); err == nil {
		t.Error("CreateBucket with no bucket param: got nil error, want one")
	}
}

func TestCreateBucket_CreatesWhenAbsent(t *testing.T) {
	h := newHarness(t)
	bucket := uniqueBucket(t)

	result, err := s3mod.CreateBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": bucket})
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if !result.Changed {
		t.Error("CreateBucket creating a fresh bucket: Changed = false, want true")
	}
	if h.rc.stats["bucket"] != bucket {
		t.Errorf("CreateBucket: bucket stat = %v, want %q", h.rc.stats["bucket"], bucket)
	}
	if _, ok := h.rc.stats["inverse"]; !ok {
		t.Error("CreateBucket that created the bucket: no inverse recorded")
	}
}

func TestCreateBucket_IdempotentWhenPresent(t *testing.T) {
	h := newHarness(t)
	bucket := uniqueBucket(t)
	ctx := context.Background()

	if _, err := s3mod.CreateBucket(ctx, h.rc, h.device, map[string]any{"bucket": bucket}); err != nil {
		t.Fatalf("CreateBucket (first): %v", err)
	}

	h.rc.stats = map[string]any{}
	result, err := s3mod.CreateBucket(ctx, h.rc, h.device, map[string]any{"bucket": bucket})
	if err != nil {
		t.Fatalf("CreateBucket (second): %v", err)
	}
	if result.Changed {
		t.Error("CreateBucket against an already-present bucket: Changed = true, want false")
	}
	if _, ok := h.rc.stats["inverse"]; ok {
		t.Error("CreateBucket that changed nothing: an inverse was recorded, want none")
	}
}

// TestCreateBucket_InvalidBucketNameFails exercises the real S3
// error path that follows a successful (false) BucketExists check: an
// invalid bucket name reports absent (HeadBucket answers 404 for
// anything unowned, valid name or not) but real CreateBucket then
// refuses it, distinct from a connection failure.
func TestCreateBucket_InvalidBucketNameFails(t *testing.T) {
	h := newHarness(t)
	if _, err := s3mod.CreateBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": "Invalid_Bucket_Name_UPPER"}); err == nil {
		t.Error("CreateBucket with an S3-invalid bucket name: got nil error, want one")
	}
}

func TestCreateBucket_BucketStatFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "bucket"
	_, err := s3mod.CreateBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": uniqueBucket(t)})
	if err == nil {
		t.Error("CreateBucket with SetStat(\"bucket\") injected to fail: got nil error, want one")
	}
}

func TestCreateBucket_DiffFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "diff"
	_, err := s3mod.CreateBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": uniqueBucket(t)})
	if err == nil {
		t.Error("CreateBucket with SetStat(\"diff\") injected to fail: got nil error, want one")
	}
}

func TestCreateBucket_InverseFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "inverse"
	_, err := s3mod.CreateBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": uniqueBucket(t)})
	if err == nil {
		t.Error("CreateBucket with SetStat(\"inverse\") injected to fail: got nil error, want one")
	}
}

func TestCreateBucket_ConnectionFailure(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
	device := &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   "us-east-1",
		endpoint: "http://127.0.0.1:1",
	}
	if _, err := s3mod.CreateBucket(context.Background(), rc, device, map[string]any{"bucket": "whatever"}); err == nil {
		t.Error("CreateBucket against an unreachable endpoint: got nil error, want one")
	}
}

func TestCreateBucket_NonDefaultRegion(t *testing.T) {
	endpoint := requireLocalStack(t)
	rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
	device := &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   "eu-west-1",
		endpoint: endpoint,
	}
	result, err := s3mod.CreateBucket(context.Background(), rc, device, map[string]any{"bucket": uniqueBucket(t)})
	if err != nil {
		t.Fatalf("CreateBucket in a non-us-east-1 region: %v", err)
	}
	if !result.Changed {
		t.Error("CreateBucket: Changed = false, want true")
	}
}

// ---------- DeleteBucket ----------

func TestDeleteBucket_NoDeviceCapability(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := s3mod.DeleteBucket(context.Background(), rc, noAWSDevice(), map[string]any{"bucket": "x"})
	if err == nil {
		t.Error("DeleteBucket against a device lacking AWSAPICapable: got nil error, want one")
	}
}

func TestDeleteBucket_MissingRequiredParam(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	device := &awsAccount{Stub: &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}}, region: "us-east-1"}
	if _, err := s3mod.DeleteBucket(context.Background(), rc, device, map[string]any{}); err == nil {
		t.Error("DeleteBucket with no bucket param: got nil error, want one")
	}
}

func TestDeleteBucket_NoOpWhenAbsent(t *testing.T) {
	h := newHarness(t)
	result, err := s3mod.DeleteBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": uniqueBucket(t)})
	if err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if result.Changed {
		t.Error("DeleteBucket on a bucket that was never created: Changed = true, want false")
	}
}

func TestDeleteBucket_DeletesWhenPresent(t *testing.T) {
	h := newHarness(t)
	bucket := uniqueBucket(t)
	ctx := context.Background()

	if _, err := s3mod.CreateBucket(ctx, h.rc, h.device, map[string]any{"bucket": bucket}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	h.rc.stats = map[string]any{}
	result, err := s3mod.DeleteBucket(ctx, h.rc, h.device, map[string]any{"bucket": bucket})
	if err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if !result.Changed {
		t.Error("DeleteBucket on an existing bucket: Changed = false, want true")
	}

	// A second delete against the same, now-absent bucket must be a
	// no-op rather than erroring.
	h.rc.stats = map[string]any{}
	again, err := s3mod.DeleteBucket(ctx, h.rc, h.device, map[string]any{"bucket": bucket})
	if err != nil {
		t.Fatalf("DeleteBucket (again): %v", err)
	}
	if again.Changed {
		t.Error("DeleteBucket on an already-deleted bucket: Changed = true, want false")
	}
}

func TestDeleteBucket_DiffFailure(t *testing.T) {
	h := newHarness(t)
	h.rc.failOnKey = "diff"
	_, err := s3mod.DeleteBucket(context.Background(), h.rc, h.device, map[string]any{"bucket": uniqueBucket(t)})
	if err == nil {
		t.Error("DeleteBucket with SetStat(\"diff\") injected to fail: got nil error, want one")
	}
}

func TestDeleteBucket_ConnectionFailure(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
	device := &awsAccount{
		Stub:     &inventorytest.Stub{StubName: "acct", Caps: []capability.Name{capability.NameAWSAPI}},
		region:   "us-east-1",
		endpoint: "http://127.0.0.1:1",
	}
	if _, err := s3mod.DeleteBucket(context.Background(), rc, device, map[string]any{"bucket": "whatever"}); err == nil {
		t.Error("DeleteBucket against an unreachable endpoint: got nil error, want one")
	}
}

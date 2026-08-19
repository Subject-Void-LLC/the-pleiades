package aws

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// White-box tests of instanceIterator's own branches that the public
// Connect/Discover path cannot reach without a real, already-connected
// client: an error mid-iteration and the empty-page case both need direct
// control over what ListInstancesPage returns, which only a
// pkg/awscloud.Client pointed at an address that answers deterministically
// gives.

func unreachableIteratorClient(t *testing.T) *awscloud.Client {
	t.Helper()
	c, err := awscloud.New("us-east-1", "test", "test", "", awscloud.WithEndpoint("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("awscloud.New: %v", err)
	}
	return c
}

func TestInstanceIterator_NextShortCircuitsAfterError(t *testing.T) {
	it := newInstanceIterator(unreachableIteratorClient(t), "us-east-1", "", 5)
	ctx := context.Background()

	if !it.Next(ctx) {
		t.Fatal("first Next(): want true (the synthetic account record), got false")
	}
	if it.Next(ctx) {
		t.Fatal("second Next() (the first real upstream call): want false, the unreachable endpoint must fail")
	}
	if it.Error() == nil {
		t.Fatal("Error() after a failed page fetch: want non-nil")
	}
	if it.Next(ctx) {
		t.Error("Next() called again after an error: want it to keep returning false without retrying")
	}
}

func TestInstanceIterator_RecordOutOfBounds(t *testing.T) {
	it := newInstanceIterator(unreachableIteratorClient(t), "us-east-1", "", 5)
	if got := it.Record(); got.ID != "" || got.Name != "" {
		t.Errorf("Record() before any Next(): got %+v, want the zero Record", got)
	}
}

// TestDiscover_ClampsSmallPageSize proves Discover's minPageSize clamp
// (aws.go) actually reaches the iterator it builds, not just that the
// clamp constant exists. This needs no live connection: Discover only
// constructs the iterator, it does not call the upstream API, so a
// placeholder client that is never dialed is enough.
func TestDiscover_ClampsSmallPageSize(t *testing.T) {
	placeholder, err := awscloud.New("us-east-1", "test", "test", "")
	if err != nil {
		t.Fatalf("awscloud.New: %v", err)
	}
	p := &Aws{client: placeholder, region: "us-east-1", cfg: syncplugin.Config{PageSize: 1}}

	it, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	concrete, ok := it.(*instanceIterator)
	if !ok {
		t.Fatalf("Discover returned %T, want *instanceIterator", it)
	}
	if concrete.pageSize != minPageSize {
		t.Errorf("pageSize = %d, want the clamped floor %d", concrete.pageSize, minPageSize)
	}
}

func TestClampPageSize(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int32
	}{
		{name: "below the floor", n: 1, want: minPageSize},
		{name: "zero (EffectivePageSize's own unset sentinel never reaches here, but zero is still below the floor)", n: 0, want: minPageSize},
		{name: "within range", n: 200, want: 200},
		{name: "above the ceiling", n: 5000, want: maxPageSize},
		{name: "larger than int32 can hold at all", n: 1 << 40, want: maxPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampPageSize(tt.n); got != tt.want {
				t.Errorf("clampPageSize(%d) = %d, want %d", tt.n, got, tt.want)
			}
		})
	}
}

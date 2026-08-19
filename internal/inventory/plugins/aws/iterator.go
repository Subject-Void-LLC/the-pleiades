package aws

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// instanceIterator streams the account/region record followed by every
// discovered EC2 instance, fetching one upstream page at a time.
//
// It pulls rather than pushes, and holds at most one page at a time,
// mirroring catalystcenter/iterator.go's deviceIterator exactly (see that
// type's own doc comment for the full reasoning: no producer goroutine to
// leak, memory stays flat regardless of fleet size). The one real
// difference is the pagination shape itself: EC2's own DescribeInstances
// API pages by opaque token, not by offset, so this iterator carries a
// token forward rather than an integer. That is EC2's contract, not a
// design choice made here.
type instanceIterator struct {
	client   *awscloud.Client
	region   string
	pageSize int32
	// endpoint is the connection's own AWS API endpoint override (empty
	// for real AWS), carried into the synthetic account record so a
	// device synced from a LocalStack-backed connection stays pointed at
	// that same backend.
	endpoint string

	// page is the current buffered page and pos the cursor within it.
	page []record.Record
	pos  int

	// token is the next upstream page token to request; "" both starts
	// the first page and, once exhausted becomes true, means nothing.
	token     string
	exhausted bool

	// accountEmitted tracks whether the synthetic account record has been
	// yielded yet. It is emitted first, before any upstream call, so an
	// account with zero instances still lands in inventory.
	accountEmitted bool

	err error
}

func newInstanceIterator(client *awscloud.Client, region, endpoint string, pageSize int32) *instanceIterator {
	return &instanceIterator{client: client, region: region, endpoint: endpoint, pageSize: pageSize}
}

// Next advances to the next Record, fetching an upstream page when the
// buffered one runs out.
func (it *instanceIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}

	if !it.accountEmitted {
		it.accountEmitted = true
		it.page = []record.Record{accountRecord(it.region, it.endpoint)}
		it.pos = 0
		return true
	}

	if it.pos+1 < len(it.page) {
		it.pos++
		return true
	}
	if it.exhausted {
		return false
	}

	instances, next, err := it.client.ListInstancesPage(ctx, it.token, it.pageSize)
	if err != nil {
		it.err = err
		return false
	}
	it.token = next
	if next == "" {
		// An empty NextToken is how DescribeInstances says "last page";
		// there is no separate has-more flag to read.
		it.exhausted = true
	}
	if len(instances) == 0 {
		return false
	}

	it.page = make([]record.Record, 0, len(instances))
	for _, inst := range instances {
		it.page = append(it.page, instanceRecord(inst))
	}
	it.pos = 0
	return true
}

// Record returns the Record at the cursor.
func (it *instanceIterator) Record() record.Record {
	if it.pos < 0 || it.pos >= len(it.page) {
		return record.Record{}
	}
	return it.page[it.pos]
}

// Error returns whatever ended iteration early, or nil if the stream
// simply ran out.
func (it *instanceIterator) Error() error { return it.err }

// Close releases the buffered page. There is no cursor or connection to
// release: each page was a self-contained API request already consumed.
func (it *instanceIterator) Close() error {
	it.page = nil
	return nil
}

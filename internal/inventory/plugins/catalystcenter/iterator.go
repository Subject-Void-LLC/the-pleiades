package catalystcenter

import (
	"context"
	"net/url"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/catalystcenter"
)

// deviceIterator streams the controller record followed by every managed
// device, fetching one upstream page at a time.
//
// It pulls rather than pushes. pkg/catalystcenter also offers EachDevice,
// which is push-shaped and would have to be bridged to this pull-shaped
// port by a goroutine and a channel. That bridge would need its own
// cancellation and shutdown handling, and a Close that happens before the
// stream is drained would leak the producer goroutine, which the
// integration suite's goleak check would then catch as a test failure
// rather than as the design problem it is. Fetching a page inside Next
// avoids the concurrency entirely: there is nothing to leak because
// nothing runs concurrently.
//
// Memory stays flat regardless of fleet size, since at most one page is
// held at a time. That is the same property internal/inventory's keyset
// iterator proves for the database side.
type deviceIterator struct {
	client   *catalystcenter.Client
	pageSize int

	// page is the current buffered page and pos the cursor within it.
	page []record.Record
	pos  int

	// offset is the next upstream offset to request. Catalyst Center's
	// paging is one-based.
	offset int

	// exhausted is set once a short page proves there is nothing more to
	// fetch, so Next stops issuing requests that would return nothing.
	exhausted bool

	// controllerEmitted tracks whether the synthetic controller record has
	// been yielded yet. It is emitted first, before any upstream call, so a
	// controller with zero managed devices still lands in inventory.
	controllerEmitted bool

	err error
}

// newDeviceIterator creates an iterator over client's managed devices.
func newDeviceIterator(client *catalystcenter.Client, pageSize int) *deviceIterator {
	return &deviceIterator{client: client, pageSize: pageSize, offset: 1}
}

// Next advances to the next Record, fetching an upstream page when the
// buffered one runs out.
func (it *deviceIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}

	if !it.controllerEmitted {
		it.controllerEmitted = true
		it.page = []record.Record{controllerRecord(it.client.BaseURL())}
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

	devices, err := it.client.ListDevices(ctx, it.offset, it.pageSize)
	if err != nil {
		it.err = err
		return false
	}
	if len(devices) < it.pageSize {
		// A short page is how this API says "last page"; there is no total
		// or next-cursor field to read.
		it.exhausted = true
	}
	if len(devices) == 0 {
		return false
	}

	it.page = make([]record.Record, 0, len(devices))
	for _, d := range devices {
		it.page = append(it.page, deviceRecord(d))
	}
	it.pos = 0
	it.offset += len(devices)
	return true
}

// Record returns the Record at the cursor.
func (it *deviceIterator) Record() record.Record {
	if it.pos < 0 || it.pos >= len(it.page) {
		return record.Record{}
	}
	return it.page[it.pos]
}

// Error returns whatever ended iteration early, or nil if the stream simply
// ran out.
func (it *deviceIterator) Error() error { return it.err }

// Close releases the buffered page. There is no cursor or connection to
// release: each page was a self-contained HTTP request whose body was
// already consumed and closed.
func (it *deviceIterator) Close() error {
	it.page = nil
	return nil
}

// controllerName derives the inventory name for a controller from its base
// URL, so a project syncing two controllers gets two distinguishable
// devices rather than a collision on a fixed name.
func controllerName(baseURL string) string {
	host := hostFromURL(baseURL)
	if host == "" {
		return "catalyst-center"
	}
	return host
}

// hostFromURL extracts the hostname from a base URL, falling back to the
// raw string when it does not parse. A record carrying a slightly odd host
// value is more useful than one carrying none.
func hostFromURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return strings.TrimSpace(baseURL)
	}
	return parsed.Hostname()
}

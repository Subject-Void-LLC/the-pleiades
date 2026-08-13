package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This file is the conformance evidence for Selector's keyset paging.
//
// Paging is exactly the kind of behavior that looks fine against whichever
// adapter it was written for and diverges silently against the other. The
// ent adapter reaches it through a WHERE device_id > cursor pushed into
// SQL; the file adapter reaches it by sorting a slice it read from YAML.
// Those are not the same code, so the port's promise that both "behave
// identically to callers" is only worth what a shared suite proves.
//
// Adding a third adapter behind this port means adding one entry to
// repositoryBackends(), never editing a test function here.

// seedPagingDevices creates n additional devices through the port itself,
// so the seeding path is backend-agnostic, and returns the full ordered
// sequence a caller sees with no paging at all. Every assertion below is
// written against that sequence rather than against literal names: the
// backends' pre-seeded fixture host carries a different DeviceID on each,
// so its position in the order is a backend detail no test should encode.
func seedPagingDevices(t *testing.T, repo inventory.Repository, n int) []pkginventory.DeviceID {
	t.Helper()
	ctx := context.Background()

	for i := range n {
		item := newConformanceItem(t, fmt.Sprintf("paging-host-%02d", i), map[string]interface{}{
			"host": fmt.Sprintf("10.0.1.%d", i),
		})
		if err := repo.Create(ctx, item); err != nil {
			t.Fatalf("Create paging host %d: %v", i, err)
		}
	}

	return collectIDs(t, repo, pkginventory.Selector{})
}

// collectIDs drains a GetGroup stream into the DeviceIDs it yielded.
func collectIDs(t *testing.T, repo inventory.Repository, sel pkginventory.Selector) []pkginventory.DeviceID {
	t.Helper()
	ctx := context.Background()

	it, err := repo.GetGroup(ctx, sel)
	if err != nil {
		t.Fatalf("GetGroup(%+v): %v", sel, err)
	}
	defer func() { _ = it.Close() }()

	var ids []pkginventory.DeviceID
	for it.Next(ctx) {
		ids = append(ids, it.Item().ID())
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterating GetGroup(%+v): %v", sel, err)
	}
	return ids
}

// TestRepositoryConformance_GetGroupStreamsInDeviceIDOrder pins the
// ordering both adapters must agree on. Without a total order a cursor
// means nothing, so this is the precondition every other test here rests
// on. The file adapter used to yield hosts.yaml's own line order, which
// changed when someone reordered the file.
func TestRepositoryConformance_GetGroupStreamsInDeviceIDOrder(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ids := seedPagingDevices(t, repo, 5)

			if len(ids) < 6 {
				t.Fatalf("got %d devices, want the seeded fixture plus 5", len(ids))
			}
			for i := 1; i < len(ids); i++ {
				if ids[i-1] >= ids[i] {
					t.Fatalf("stream is not strictly ascending by DeviceID: %v", ids)
				}
			}
		})
	}
}

// TestRepositoryConformance_GetGroupPagesWithoutGapsOrRepeats is the real
// assertion: walking the stream two at a time reproduces exactly the same
// sequence as one unbounded read.
//
// Gaps and repeats are the specific failures offset pagination produces
// against a table being written to, and on an inventory list they mean a
// device silently missing from a page a human is reading while another is
// shown twice. Asserting sequence equality rather than merely counting
// rows is what makes that detectable.
func TestRepositoryConformance_GetGroupPagesWithoutGapsOrRepeats(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			want := seedPagingDevices(t, repo, 5)

			const pageSize = 2
			var got []pkginventory.DeviceID
			var cursor pkginventory.DeviceID

			for range len(want) + 1 {
				page := collectIDs(t, repo, pkginventory.Selector{After: cursor, Limit: pageSize})
				if len(page) == 0 {
					break
				}
				if len(page) > pageSize {
					t.Fatalf("page returned %d devices, want at most %d", len(page), pageSize)
				}
				got = append(got, page...)
				cursor = page[len(page)-1]
			}

			if len(got) != len(want) {
				t.Fatalf("paged walk yielded %d devices, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("paged walk diverged at %d: got %q, want %q\nfull: %v", i, got[i], want[i], got)
				}
			}
		})
	}
}

func TestRepositoryConformance_GetGroupLimitBoundsTheStream(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			all := seedPagingDevices(t, repo, 5)

			for _, limit := range []int{1, 3} {
				got := collectIDs(t, repo, pkginventory.Selector{Limit: limit})
				if len(got) != limit {
					t.Errorf("Limit %d yielded %d devices, want %d", limit, len(got), limit)
				}
				for i := range got {
					if got[i] != all[i] {
						t.Errorf("Limit %d item %d = %q, want %q", limit, i, got[i], all[i])
					}
				}
			}
		})
	}
}

// TestRepositoryConformance_GetGroupZeroLimitIsUnbounded protects every
// caller that predates paging. Zero is the value a dispatch fan-out
// passes, and it has to mean "every device in the group" -- reading it as
// "none" would make a launch silently reach nothing while reporting
// success.
func TestRepositoryConformance_GetGroupZeroLimitIsUnbounded(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			all := seedPagingDevices(t, repo, 5)

			got := collectIDs(t, repo, pkginventory.Selector{Limit: 0})
			if len(got) != len(all) {
				t.Fatalf("Limit 0 yielded %d devices, want all %d", len(got), len(all))
			}
		})
	}
}

func TestRepositoryConformance_GetGroupAfterSkipsPrecedingDevices(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			all := seedPagingDevices(t, repo, 5)

			// Resuming after the second device yields precisely the tail.
			got := collectIDs(t, repo, pkginventory.Selector{After: all[1]})
			want := all[2:]

			if len(got) != len(want) {
				t.Fatalf("After %q yielded %d devices, want %d", all[1], len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("After %q item %d = %q, want %q", all[1], i, got[i], want[i])
				}
			}

			// A cursor past the end is an empty page, not an error: that is
			// how a caller learns it has reached the end.
			if got := collectIDs(t, repo, pkginventory.Selector{After: all[len(all)-1]}); len(got) != 0 {
				t.Errorf("After the last device yielded %d devices, want 0", len(got))
			}
		})
	}
}

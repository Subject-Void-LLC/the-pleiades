// Tests of waiting for a capture's file to be whole, against cmltest's
// controller serving a file it is still writing, as a real one does for
// minutes after a capture stops: the file grows, holds still short of its
// packets, ends partway through a record, changes during a fetch, and is
// cut off; each is waited out, and a wait that ends first says how far the
// file had got.
package cml

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// records is a little-endian pcap file of n records of 100 bytes each.
func records(n int) []byte {
	b := bytes.NewBuffer([]byte{0xd4, 0xc3, 0xb2, 0xa1, 2, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 1, 0, 0, 0})
	for i := range n {
		hdr := make([]byte, 16)
		binary.LittleEndian.PutUint32(hdr[8:], 100)
		binary.LittleEndian.PutUint32(hdr[12:], 100)
		b.Write(hdr)
		b.Write(bytes.Repeat([]byte{byte(i)}, 100))
	}
	return b.Bytes()
}

// quickPolls makes AwaitPCAP poll every millisecond, for the test's length.
func quickPolls(t *testing.T) {
	old := awaitPoll
	awaitPoll = time.Millisecond
	t.Cleanup(func() { awaitPoll = old })
}

// writing is a controller serving, at its n'th /pcap request, the file
// steps[n-1] names (the last step from then on): a file being written.
func writing(t *testing.T, steps ...[]byte) (*cmltest.Controller, *Client) {
	t.Helper()
	quickPolls(t)
	ctl, c := capturing(t, true, nil)
	ctl.Mu.Lock()
	ctl.PCAPAt = func(n int) []byte { return steps[min(n, len(steps))-1] }
	ctl.Mu.Unlock()
	return ctl, c
}

// TestAwaitPCAP_WaitsForEveryPacket: a file growing to its 100 packets,
// with a step that ends partway through a record, is returned only whole.
func TestAwaitPCAP_WaitsForEveryPacket(t *testing.T) {
	full := records(100)
	ctl, c := writing(t, records(20), records(40), full[:len(records(60))+50], records(80), full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 100)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
	if r := requests(ctl); len(r) < 7 {
		t.Errorf("returned after %d requests: before the length held still", len(r))
	}
}

// TestAwaitPCAP_AStillLengthShortOfThePacketsKeepsWaiting: a file that
// holds still at 50 of the 100 packets the capture took (a writer pausing)
// is waited on until it holds them all.
func TestAwaitPCAP_AStillLengthShortOfThePacketsKeepsWaiting(t *testing.T) {
	half, full := records(50), records(100)
	_, c := writing(t, half, half, half, half, half, half, full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 100)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_WithNoCountAStillWholeFileDecides: with the count not known
// (a capture stopped before the task), a file ending partway through a
// record is not whole however still it holds, and a still whole file is.
func TestAwaitPCAP_WithNoCountAStillWholeFileDecides(t *testing.T) {
	full := records(30)
	cut := full[:len(full)-10]
	_, c := writing(t, records(10), cut, cut, cut, cut, full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 0)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_AFileThatChangesOrIsCutDuringAFetchIsWaitedOn: a fetch the
// controller resets on every attempt, and one whose second attempt finds
// the file longer, are both a file still being written, not a failure.
func TestAwaitPCAP_AFileThatChangesOrIsCutDuringAFetchIsWaitedOn(t *testing.T) {
	quickRetries(t)
	a, b := records(40), records(60)
	quickPolls(t)
	ctl, c := capturing(t, true, nil)
	ctl.Mu.Lock()
	ctl.PCAPAt = func(n int) []byte {
		switch {
		case n <= 2:
			return a
		case n == 3:
			// The fetch's first attempt is cut, and its second finds more.
			ctl.PCAPCuts = 1
			return a
		case n <= 6:
			return b
		case n == 7:
			// The next fetch is cut on every attempt.
			ctl.PCAPCuts = fetchAttempts
			return b
		}
		return b
	}
	ctl.Mu.Unlock()
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 60)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_AWaitThatEndsSaysHowFarTheFileGot: a file still short of
// its packets when the wait ends is refused, naming its length, the
// packets it held and the packets taken.
func TestAwaitPCAP_AWaitThatEndsSaysHowFarTheFileGot(t *testing.T) {
	_, c := writing(t, records(10))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := c.AwaitPCAP(ctx, "k1", 16<<20, 100)
	for _, want := range []string{"had not finished writing the capture file", "it stated 1184 bytes", "held 10 packets", "of the 100 the capture took", "deadline exceeded"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the error %v does not say %q", err, want)
		}
	}
}

// TestAwaitPCAP_AnUnstatedLengthIsReadByFetching: a controller stating no
// length has the file fetched each poll, and two fetches of one length
// end the wait.
func TestAwaitPCAP_AnUnstatedLengthIsReadByFetching(t *testing.T) {
	quickPolls(t)
	ctl, c := capturing(t, false, nil)
	steps := [][]byte{records(5), records(7), records(9), records(9), records(9), records(9)}
	n := pcapHandler(ctl, func(n int, w http.ResponseWriter, _ *http.Request) {
		data := steps[min(n, len(steps))-1]
		// Written in two flushed pieces, so HTTP/1.1 sends it chunked and
		// states no length.
		_, _ = w.Write(data[:10])
		_ = http.NewResponseController(w).Flush()
		_, _ = w.Write(data[10:])
	})
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 0)
	if err != nil || !bytes.Equal(got, records(9)) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
	if *n != 6 {
		t.Errorf("%d requests, not a probe and a fetch on each of three polls", *n)
	}
}

// TestAwaitPCAP_RefusalsEndTheWait: a length over the bound, an answer
// outside 2xx and a client not logged in end the wait at once.
func TestAwaitPCAP_RefusalsEndTheWait(t *testing.T) {
	ctl, c := writing(t, records(100))
	if _, err := c.AwaitPCAP(context.Background(), "k1", 1000, 100); err == nil || !strings.Contains(err.Error(), "the capture is 11624 bytes, over the 1000") {
		t.Errorf("over the bound: %v", err)
	}
	ctl.Mu.Lock()
	ctl.Fail = map[string]bool{"/pcap/k1": true}
	ctl.Mu.Unlock()
	if _, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 100); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing controller: %v", err)
	}
	ctl.Mu.Lock()
	ctl.Fail = nil
	ctl.PCAPAt = func(int) []byte { return []byte("<html>not a capture</html>") }
	ctl.Mu.Unlock()
	if _, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, 0); err == nil || !strings.Contains(err.Error(), "not a pcap") {
		t.Errorf("an answer that is no capture: %v", err)
	}
	if _, err := (&Client{}).AwaitPCAP(context.Background(), "k1", 16<<20, 0); err == nil || !strings.Contains(err.Error(), "Connect first") {
		t.Errorf("not logged in: %v", err)
	}
}

// TestCountRecords: records are counted in pcap of either byte order and
// in pcapng's packet blocks, and a file ending partway through one is not
// whole.
func TestCountRecords(t *testing.T) {
	le := records(3)
	be := append([]byte{0xa1, 0xb2, 0xc3, 0xd4}, make([]byte, 20)...)
	for range 2 {
		hdr := make([]byte, 16)
		binary.BigEndian.PutUint32(hdr[8:], 4)
		be = append(append(be, hdr...), 1, 2, 3, 4)
	}
	block := func(order binary.ByteOrder, kind uint32, body []byte) []byte {
		b := make([]byte, 8, 12+len(body))
		order.PutUint32(b, kind)
		order.PutUint32(b[4:], uint32(12+len(body)))
		b = append(b, body...)
		tail := make([]byte, 4)
		order.PutUint32(tail, uint32(12+len(body)))
		return append(b, tail...)
	}
	shb := func(order binary.ByteOrder) []byte {
		body := make([]byte, 16)
		order.PutUint32(body, 0x1a2b3c4d)
		return block(order, 0x0a0d0d0a, body)
	}
	ng := append(shb(binary.LittleEndian), block(binary.LittleEndian, 1, make([]byte, 8))...)
	ng = append(ng, block(binary.LittleEndian, 6, make([]byte, 24))...)
	ng = append(ng, block(binary.LittleEndian, 3, make([]byte, 8))...)
	ng = append(ng, shb(binary.BigEndian)...)
	ng = append(ng, block(binary.BigEndian, 2, make([]byte, 24))...)
	for name, tc := range map[string]struct {
		data  []byte
		n     int
		whole bool
	}{
		"pcap little endian":            {le, 3, true},
		"pcap cut in a record":          {le[:len(le)-1], 2, false},
		"pcap cut in a record's header": {le[:24+116+5], 1, false},
		"pcap big endian":               {be, 2, true},
		"pcapng, two sections":          {ng, 3, true},
		"pcapng cut in a block":         {ng[:len(ng)-3], 2, false},
		"pcapng cut in a block header":  {ng[:len(ng)-len(block(binary.BigEndian, 2, make([]byte, 24)))+4], 2, false},
		"too short for a header":        {le[:10], 0, false},
	} {
		if n, whole := countRecords(tc.data); n != tc.n || whole != tc.whole {
			t.Errorf("%s: countRecords = %d, %v; want %d, %v", name, n, whole, tc.n, tc.whole)
		}
	}
}

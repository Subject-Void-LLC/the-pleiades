// Tests of waiting for a capture's file to be whole, against cmltest's
// controller serving a file it is still writing, as a real one does for
// minutes after a capture stops: the file grows, holds still short of its
// packets, pauses, ends partway through a record, changes during a fetch,
// and is cut off; each is waited out, its length is read without fetching
// it where the controller allows, and a wait that ends says how far the
// file had got. Every wait that ends early ends at a known point (a poll's
// end, or a request the controller is answering), never on a deadline.
package cml

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"testing"

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

// quick waits a millisecond between polls, for packets.
func quick(packets int) Await { return Await{Packets: packets, Poll: 1} }

// writing is a controller serving, at its n'th GET of the file, steps[n-1]
// (the last step from then on): a file being written. HEAD is refused, as
// CML refuses it.
func writing(t *testing.T, steps ...[]byte) (*cmltest.Controller, *Client) {
	t.Helper()
	ctl, c := capturing(t, true, nil)
	ctl.Mu.Lock()
	ctl.PCAPAt = func(n int) []byte { return steps[min(n, len(steps))-1] }
	ctl.Mu.Unlock()
	return ctl, c
}

// endAtPoll ends ctx at the end of poll n, through afterPoll.
func endAtPoll(t *testing.T, n int) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	afterPoll = func(poll int) {
		if poll == n {
			cancel()
		}
	}
	t.Cleanup(func() { afterPoll = nil; cancel() })
	return ctx
}

// fetches counts the requests that fetched the whole file: GETs that asked
// for no range.
func fetches(ctl *cmltest.Controller) int {
	n := 0
	for _, r := range requests(ctl) {
		if strings.HasPrefix(r, "GET ") && strings.HasSuffix(r, " ") {
			n++
		}
	}
	return n
}

// TestAwaitPCAP_WaitsForEveryPacket: a file growing to its 100 packets,
// through a step that ends partway through a record, is returned only
// whole, and only once its length held still.
func TestAwaitPCAP_WaitsForEveryPacket(t *testing.T) {
	full := records(100)
	_, c := writing(t, records(20), records(40), full[:len(records(60))+50], records(80), full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(100))
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_AStillLengthShortOfThePacketsKeepsWaiting: a file that
// holds still at 50 of the 100 packets the capture took (a writer pausing)
// is waited on until it holds them all.
func TestAwaitPCAP_AStillLengthShortOfThePacketsKeepsWaiting(t *testing.T) {
	half, full := records(50), records(100)
	_, c := writing(t, half, half, half, half, half, half, half, full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(100))
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_ALengthIsReadAsARangeAndTheFileFetchedOnce: on a
// controller serving ranges, every poll reads the length as bytes=0-0, and
// a file short of its packets at a length that holds still is fetched once
// until the wait ends.
func TestAwaitPCAP_ALengthIsReadAsARangeAndTheFileFetchedOnce(t *testing.T) {
	ctl, c := writing(t, records(50))
	ctl.Mu.Lock()
	ctl.PCAPRanges = true
	ctl.Mu.Unlock()
	_, err := c.AwaitPCAP(endAtPoll(t, 6), "k1", 16<<20, quick(100))
	if err == nil || !strings.Contains(err.Error(), "held 50 packets of the 100 the capture took") {
		t.Fatalf("AwaitPCAP: %v", err)
	}
	r := requests(ctl)
	if fetches(ctl) != 1 || r[0] != "HEAD HTTP/2.0 " || r[1] != "GET HTTP/2.0 bytes=0-0" {
		t.Fatalf("requests %q: not HEAD refused, then ranges and one fetch", r)
	}
}

// TestAwaitPCAP_HEADIsUsedWhereItIsAnswered: a controller answering HEAD
// has the length read that way on every poll.
func TestAwaitPCAP_HEADIsUsedWhereItIsAnswered(t *testing.T) {
	ctl, c := writing(t, records(5))
	ctl.Mu.Lock()
	ctl.PCAPHead = true
	ctl.Mu.Unlock()
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(5))
	if err != nil || !bytes.Equal(got, records(5)) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
	if r := requests(ctl); strings.Join(r, "|") != "HEAD HTTP/2.0 |HEAD HTTP/2.0 |GET HTTP/2.0 " {
		t.Fatalf("requests %q", r)
	}
}

// TestAwaitPCAP_WithNoCountAPauseIsNotTheEnd: with the count not known (a
// capture stopped at its own bound), a length holding still for two polls
// is a writer pausing, not one done: the file is taken only after
// steadyPolls in a row, and so whole.
func TestAwaitPCAP_WithNoCountAPauseIsNotTheEnd(t *testing.T) {
	part, full := records(10), records(20)
	// Four GETs of part (three polls' lengths, then what a fetch after two
	// still polls would take), then the whole file.
	_, c := writing(t, part, part, part, part, full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(0))
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_WithNoCountAPartialRecordIsNotWhole: a file ending partway
// through a record is not whole however still it holds.
func TestAwaitPCAP_WithNoCountAPartialRecordIsNotWhole(t *testing.T) {
	full := records(30)
	cut := full[:len(full)-10]
	_, c := writing(t, cut, cut, cut, cut, cut, cut, full)
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(0))
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_AFileThatChangesOrIsCutDuringAFetchIsWaitedOn: a fetch
// whose second attempt finds the file longer, and one the controller
// resets on every attempt, are both a file still being written.
func TestAwaitPCAP_AFileThatChangesOrIsCutDuringAFetchIsWaitedOn(t *testing.T) {
	a, b := records(40), records(60)
	ctl, c := capturing(t, true, nil)
	ctl.Mu.Lock()
	ctl.PCAPAt = func(n int) []byte {
		switch {
		case n <= 2:
			return a
		case n == 3:
			// The first fetch is cut, and its second attempt finds more.
			ctl.PCAPCuts = 1
			return a
		case n == 7:
			// The next fetch is cut on every attempt.
			ctl.PCAPCuts = fetchAttempts
		}
		return b
	}
	ctl.Mu.Unlock()
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(60))
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
}

// TestAwaitPCAP_AWaitThatEndsSaysHowFarTheFileGot: a file still short of
// its packets when the wait ends (at the end of its third poll) is
// refused, naming its length, the packets it held and the packets taken.
func TestAwaitPCAP_AWaitThatEndsSaysHowFarTheFileGot(t *testing.T) {
	_, c := writing(t, records(10))
	_, err := c.AwaitPCAP(endAtPoll(t, 3), "k1", 16<<20, quick(100))
	want := "the controller had not finished writing the capture file (it stated 1184 bytes and the last fetch held 10 packets of the 100 the capture took): context canceled"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("AwaitPCAP: %v", err)
	}
}

// TestAwaitPCAP_AResetOnEveryFetchIsNamed: a wait that ends while every
// fetch is reset names the reset, so it is not taken for a slow writer.
func TestAwaitPCAP_AResetOnEveryFetchIsNamed(t *testing.T) {
	quickRetries(t)
	ctl, c := writing(t, records(5))
	ctl.Mu.Lock()
	ctl.PCAPCuts = 1000
	ctl.Mu.Unlock()
	_, err := c.AwaitPCAP(endAtPoll(t, 2), "k1", 16<<20, quick(5))
	if err == nil || !strings.Contains(err.Error(), "the last fetch ended: cml: GET /api/v0/pcap: reading the file") || !strings.Contains(err.Error(), "INTERNAL_ERROR") {
		t.Fatalf("AwaitPCAP: %v", err)
	}
}

// answering is a controller whose /pcap answers come from answer, given
// each GET's number; HEAD is refused, as CML refuses it.
func answering(t *testing.T, h2 bool, answer func(n int, w http.ResponseWriter, r *http.Request)) (*cmltest.Controller, *Client) {
	t.Helper()
	ctl, c := capturing(t, h2, nil)
	gets := 0
	pcapHandler(ctl, func(_ int, w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gets++
		answer(gets, w, r)
	})
	return ctl, c
}

// TestAwaitPCAP_AWaitEndedDuringARequestIsTheWaitsEnd: a wait ended while
// the controller is answering the length's read, or the fetch, is reported
// as the wait's end, not as a failed request.
func TestAwaitPCAP_AWaitEndedDuringARequestIsTheWaitsEnd(t *testing.T) {
	for name, at := range map[string]int{"the length": 1, "the fetch": 3} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, c := answering(t, true, func(n int, w http.ResponseWriter, r *http.Request) {
				if n == at {
					// Ended while this request is answered, which it never is.
					cancel()
					<-r.Context().Done()
					return
				}
				data := records(5)
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				_, _ = w.Write(data)
			})
			_, err := c.AwaitPCAP(ctx, "k1", 16<<20, quick(5))
			if err == nil || !strings.HasPrefix(err.Error(), "cml: the controller had not finished writing the capture file") || !strings.HasSuffix(err.Error(), "context canceled") {
				t.Fatalf("AwaitPCAP: %v", err)
			}
		})
	}
}

// TestAwaitPCAP_AnUnstatedLengthIsReadByFetching: a controller stating no
// length has the file fetched on each poll, and steadyPolls fetches of one
// length in a row end the wait.
func TestAwaitPCAP_AnUnstatedLengthIsReadByFetching(t *testing.T) {
	steps := [][]byte{records(5), records(7), records(9)}
	gets := 0
	_, c := answering(t, false, func(n int, w http.ResponseWriter, _ *http.Request) {
		gets = n
		// Each poll is a read of the length, then a fetch: the n'th fetch
		// is GET 2n.
		data := steps[min((n+1)/2, len(steps))-1]
		// Written in two flushed pieces, so HTTP/1.1 sends it chunked and
		// states no length.
		_, _ = w.Write(data[:10])
		_ = http.NewResponseController(w).Flush()
		_, _ = w.Write(data[10:])
	})
	got, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(0))
	if err != nil || !bytes.Equal(got, records(9)) {
		t.Fatalf("AwaitPCAP = %d bytes, %v", len(got), err)
	}
	// Polls 3 to 6 see 9 records, the last three still: six polls of a
	// read and a fetch each.
	if gets != 12 {
		t.Fatalf("%d GETs, not 12", gets)
	}
}

// TestAwaitPCAP_RefusalsEndTheWait: a length over the bound, an answer
// outside 2xx, an answer that is no capture, a range answer naming no
// length, and a client not logged in end the wait at once.
func TestAwaitPCAP_RefusalsEndTheWait(t *testing.T) {
	ctl, c := writing(t, records(100))
	if _, err := c.AwaitPCAP(context.Background(), "k1", 1000, quick(100)); err == nil || !strings.Contains(err.Error(), "the capture is 11624 bytes, over the 1000") {
		t.Errorf("over the bound: %v", err)
	}
	ctl.Mu.Lock()
	ctl.Fail = map[string]bool{"/pcap/k1": true}
	ctl.Mu.Unlock()
	if _, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(100)); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing controller: %v", err)
	}
	ctl.Mu.Lock()
	ctl.Fail = nil
	ctl.PCAPAt = func(int) []byte { return []byte("<html>not a capture</html>") }
	ctl.Mu.Unlock()
	if _, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(0)); err == nil || !strings.Contains(err.Error(), "not a pcap") {
		t.Errorf("an answer that is no capture: %v", err)
	}
	if _, err := (&Client{}).AwaitPCAP(context.Background(), "k1", 16<<20, quick(0)); err == nil || !strings.Contains(err.Error(), "Connect first") {
		t.Errorf("not logged in: %v", err)
	}
	_, c = answering(t, true, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-0/*")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte{0xd4})
	})
	if _, err := c.AwaitPCAP(context.Background(), "k1", 16<<20, quick(0)); err == nil || !strings.Contains(err.Error(), `Content-Range "bytes 0-0/*", which names no length`) {
		t.Errorf("a range naming no length: %v", err)
	}
}

// TestAwaitPCAP_TheDefaultPoll: a wait asked for with no interval waits
// DefaultAwaitPoll, and a file whole at once is still read twice first.
func TestAwaitPCAP_TheDefaultPoll(t *testing.T) {
	if (Await{}).Poll != 0 || DefaultAwaitPoll.Seconds() != 5 {
		t.Fatal("the default poll is not five seconds")
	}
	_, c := writing(t, records(1))
	// Ended at the first poll's end, before any wait for the second.
	if _, err := c.AwaitPCAP(endAtPoll(t, 1), "k1", 16<<20, Await{Packets: 1}); err == nil || !strings.Contains(err.Error(), "it stated 140 bytes") {
		t.Fatalf("AwaitPCAP: %v", err)
	}
}

// pcapRecordHeader is a 16-byte pcap record header claiming size bytes.
func pcapRecordHeader(order binary.ByteOrder, size uint32) []byte {
	hdr := make([]byte, 16)
	order.PutUint32(hdr[8:], size)
	return hdr
}

// TestCountRecords: records are counted in pcap of either byte order and
// in pcapng's packet blocks; a file ending partway through one is not
// whole; and a length of 2^31 or more is a record that does not fit, never
// one that turns negative and passes.
func TestCountRecords(t *testing.T) {
	le := records(3)
	be := append([]byte{0xa1, 0xb2, 0xc3, 0xd4}, make([]byte, 20)...)
	for range 2 {
		be = append(append(be, pcapRecordHeader(binary.BigEndian, 4)...), 1, 2, 3, 4)
	}
	block := func(order binary.ByteOrder, kind, size uint32, body []byte) []byte {
		b := make([]byte, 8, 12+len(body))
		order.PutUint32(b, kind)
		order.PutUint32(b[4:], size)
		b = append(b, body...)
		tail := make([]byte, 4)
		order.PutUint32(tail, size)
		return append(b, tail...)
	}
	full := func(order binary.ByteOrder, kind uint32, body []byte) []byte {
		return block(order, kind, uint32(12+len(body)), body)
	}
	shb := func(order binary.ByteOrder) []byte {
		body := make([]byte, 16)
		order.PutUint32(body, 0x1a2b3c4d)
		return full(order, 0x0a0d0d0a, body)
	}
	ng := append(shb(binary.LittleEndian), full(binary.LittleEndian, 1, make([]byte, 8))...)
	ng = append(ng, full(binary.LittleEndian, 6, make([]byte, 24))...)
	ng = append(ng, full(binary.LittleEndian, 3, make([]byte, 8))...)
	ng = append(ng, shb(binary.BigEndian)...)
	ng = append(ng, full(binary.BigEndian, 2, make([]byte, 24))...)
	huge := func(size uint32) []byte {
		return append(append([]byte(nil), le...), pcapRecordHeader(binary.LittleEndian, size)...)
	}
	hugeBlock := func(size uint32) []byte {
		return append(append([]byte(nil), shb(binary.LittleEndian)...), block(binary.LittleEndian, 6, size, make([]byte, 24))...)
	}
	for name, tc := range map[string]struct {
		data  []byte
		n     int
		whole bool
	}{
		"pcap little endian":              {le, 3, true},
		"pcap cut in a record":            {le[:len(le)-1], 2, false},
		"pcap cut in a record's header":   {le[:24+116+5], 1, false},
		"pcap big endian":                 {be, 2, true},
		"pcapng, two sections":            {ng, 3, true},
		"pcapng cut in a block":           {ng[:len(ng)-3], 2, false},
		"pcapng cut in a block header":    {ng[:len(ng)-len(full(binary.BigEndian, 2, make([]byte, 24)))+4], 2, false},
		"too short for a header":          {le[:10], 0, false},
		"a record of 2^31 bytes":          {huge(0x80000000), 3, false},
		"a record of 2^32-1 bytes":        {huge(0xffffffff), 3, false},
		"a block of 2^31 bytes":           {hugeBlock(0x80000000), 0, false},
		"a block of 2^32-1 bytes":         {hugeBlock(0xffffffff), 0, false},
		"a block shorter than its header": {hugeBlock(8), 0, false},
	} {
		if n, whole := countRecords(tc.data); n != tc.n || whole != tc.whole {
			t.Errorf("%s: countRecords = %d, %v; want %d, %v", name, n, whole, tc.n, tc.whole)
		}
	}
}

// FuzzCountRecords: no input panics, and the count never exceeds what the
// bytes could hold.
func FuzzCountRecords(f *testing.F) {
	f.Add(records(2))
	f.Add(append(records(1), pcapRecordHeader(binary.LittleEndian, 0x80000000)...))
	f.Add([]byte{0x0a, 0x0d, 0x0d, 0x0a, 0x1c, 0, 0, 0, 0x4d, 0x3c, 0x2b, 0x1a, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		n, _ := countRecords(data)
		if n < 0 || n > len(data)/12 {
			t.Fatalf("countRecords = %d for %d bytes", n, len(data))
		}
	})
}

// Package cml: waiting for a stopped capture's file to be whole. CML 2.7
// writes a capture's file behind the capture itself. Measured on a real
// controller (2026-11-17): a link capture stopped at 25,593 packets was
// served five seconds later as 12,461 packets (4,455,644 bytes) and four
// minutes later as all 25,593 (9,147,421 bytes), growing at about 20 KB a
// second in between; a fetch while it grew came back short, or was reset
// partway (INTERNAL_ERROR) when it grew during the download, or changed
// length between two attempts.
package cml

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// awaitPoll is how long AwaitPCAP waits between two reads of the file's
// length. It is a variable only so a test can prove the wait quickly.
var awaitPoll = 5 * time.Second

// AwaitPCAP waits until the controller has written the capture file key
// names whole, then returns it, at most max bytes. It is whole when the
// length the controller states for it held still across one poll, its
// last record is complete, and it holds at least packets records: how
// many the capture had taken when it was stopped (0 when that is not
// known), which a whole file holds exactly (measured). Until its length
// holds still it is not downloaded, only its length read, so a growing
// file is not fetched over and over. ctx bounds the wait; when it ends
// first, the error says how far the file had got.
func (c *Client) AwaitPCAP(ctx context.Context, key string, max int64, packets int) ([]byte, error) {
	var last, fetched int64 = -1, -1
	count := 0
	for {
		size, err := c.pcapLength(ctx, key, max)
		if err != nil {
			if ctx.Err() != nil {
				// The wait ended during the read, which is no refusal.
				return nil, awaitError(ctx, last, count, packets)
			}
			return nil, err
		}
		// A length the answer does not state is read by fetching the file.
		if size < 0 || size == last {
			data, err := c.FetchPCAP(ctx, key, max)
			switch {
			case err == nil:
				n, whole := countRecords(data)
				count = n
				held := int64(len(data))
				if whole && n >= packets && (size == held || (size < 0 && held == fetched)) {
					return data, nil
				}
				fetched = held
			case errors.Is(err, ErrCaptureChanged) || cutOff(err):
				// Still being written while it was fetched.
			case ctx.Err() != nil:
				return nil, awaitError(ctx, size, count, packets)
			default:
				return nil, err
			}
		}
		last = size
		select {
		case <-ctx.Done():
			return nil, awaitError(ctx, size, count, packets)
		case <-time.After(awaitPoll):
		}
	}
}

// awaitError says how far the file had got when ctx ended a wait for it.
func awaitError(ctx context.Context, size int64, count, packets int) error {
	got := "no length read yet"
	if size >= 0 {
		got = fmt.Sprintf("it stated %d bytes", size)
	}
	if count > 0 {
		got += fmt.Sprintf(" and the last fetch held %d packets", count)
	}
	if packets > 0 {
		got += fmt.Sprintf(" of the %d the capture took", packets)
	}
	return fmt.Errorf("cml: the controller had not finished writing the capture file (%s): %w", got, ctx.Err())
}

// pcapLength is the length the controller states for the file key names
// (-1 when it states none), read from the answer's headers alone: its body
// is never read. A length over max is refused as a fetch refuses it.
func (c *Client) pcapLength(ctx context.Context, key string, max int64) (int64, error) {
	if c.token == "" {
		return 0, errors.New("cml: Connect first")
	}
	u, err := c.endpoint("/pcap/" + url.PathEscape(key))
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cml: GET /api/v0/pcap: %w", err)
	}
	// Closing the body unread ends the download: over HTTP/2 a reset of
	// this stream alone, over HTTP/1.1 the connection.
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return 0, &StatusError{Method: http.MethodGet, Path: "/pcap/<key>", Status: res.StatusCode, Body: string(body)}
	}
	if res.ContentLength > max {
		return 0, fmt.Errorf("cml: GET /api/v0/pcap: %w", (&pcapFetch{max: max, total: res.ContentLength}).overBound())
	}
	return res.ContentLength, nil
}

// countRecords counts the packet records of a pcap or pcapng file and
// reports whether its last one is complete (a file still being written
// can end partway through one).
func countRecords(data []byte) (int, bool) {
	if len(data) < 24 {
		return 0, false
	}
	switch {
	case data[0] == 0x0a && data[1] == 0x0d && data[2] == 0x0d && data[3] == 0x0a:
		return countBlocks(data)
	case data[0] == 0xd4 || data[0] == 0x4d:
		return countPCAP(data, binary.LittleEndian)
	default:
		return countPCAP(data, binary.BigEndian)
	}
}

// countPCAP counts a pcap file's records: each a 16-byte header whose
// third word is the bytes captured, then those bytes.
func countPCAP(data []byte, order binary.ByteOrder) (int, bool) {
	n := 0
	for off := 24; off < len(data); n++ {
		if len(data)-off < 16 {
			return n, false
		}
		size := int(order.Uint32(data[off+8:]))
		if size > len(data)-off-16 {
			return n, false
		}
		off += 16 + size
	}
	return n, true
}

// countBlocks counts a pcapng file's packet blocks (enhanced, simple and
// the obsolete packet block), each section in the byte order its section
// header block names.
func countBlocks(data []byte) (int, bool) {
	var order binary.ByteOrder = binary.LittleEndian
	n := 0
	for off := 0; off < len(data); {
		if len(data)-off < 12 {
			return n, false
		}
		if binary.LittleEndian.Uint32(data[off:]) == 0x0a0d0d0a {
			// A section header: its byte-order magic sets the order.
			if binary.BigEndian.Uint32(data[off+8:]) == 0x1a2b3c4d {
				order = binary.BigEndian
			} else {
				order = binary.LittleEndian
			}
		}
		kind, size := order.Uint32(data[off:]), int(order.Uint32(data[off+4:]))
		if size < 12 || size > len(data)-off {
			return n, false
		}
		if kind == 2 || kind == 3 || kind == 6 {
			n++
		}
		off += size
	}
	return n, true
}

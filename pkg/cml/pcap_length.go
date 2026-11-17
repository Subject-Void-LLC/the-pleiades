// Package cml: a capture file's length, read without downloading it where
// the controller allows, and its packet records counted. HEAD is asked
// first, then the one-byte range bytes=0-0, whose answer names the whole
// length in Content-Range; a controller that serves neither answers the
// range with the whole file, and then the length is its Content-Length,
// read from the headers with the body closed unread, which starts a
// download and cuts it off. CML 2.7 is that controller: it refuses HEAD on
// /pcap/{key} (405, "allow: GET") and answers bytes=0-0 with the whole
// file (200), both measured on 2026-11-17, so on CML every poll's read of
// the length starts a download that is cut off after its headers.
package cml

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// How a lengthProbe reads the length, once it has learned which works.
const (
	probeUnknown = iota
	probeHead
	probeRange
	probeGet
)

// lengthProbe reads the length of one capture file, remembering which
// request the controller answers so later polls send only that one.
type lengthProbe struct {
	c   *Client
	key string
	max int64
	how int
}

// length is the file's length, -1 when the controller states none. A
// length over max is refused as a fetch refuses it.
func (p *lengthProbe) length(ctx context.Context) (int64, error) {
	if p.c.token == "" {
		return 0, errors.New("cml: Connect first")
	}
	if p.how == probeUnknown || p.how == probeHead {
		res, err := p.send(ctx, http.MethodHead, "")
		if err != nil {
			return 0, err
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusOK && res.ContentLength >= 0 {
			p.how = probeHead
			return p.bounded(res.ContentLength)
		}
		// No HEAD here (CML answers 405): ask for a range instead.
		p.how = probeRange
	}
	rng := ""
	if p.how == probeRange {
		rng = "bytes=0-0"
	}
	res, err := p.send(ctx, http.MethodGet, rng)
	if err != nil {
		return 0, err
	}
	// Closing the body unread ends a whole file's download: over HTTP/2 a
	// reset of this stream alone, over HTTP/1.1 the connection.
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusPartialContent:
		total, err := rangeTotal(res.Header.Get("Content-Range"))
		if err != nil {
			return 0, err
		}
		return p.bounded(total)
	case res.StatusCode == http.StatusOK:
		// No ranges served: the whole file's answer, its length stated.
		p.how = probeGet
		return p.bounded(res.ContentLength)
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	return 0, &StatusError{Method: http.MethodGet, Path: "/pcap/<key>", Status: res.StatusCode, Body: string(body)}
}

// send sends one request for the file, with a Range header when rng is
// set.
func (p *lengthProbe) send(ctx context.Context, method, rng string) (*http.Response, error) {
	u, err := p.c.endpoint("/pcap/" + url.PathEscape(p.key))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.c.token)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	res, err := p.c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cml: %s /api/v0/pcap: %w", method, err)
	}
	return res, nil
}

// bounded is n, or the refusal of a file over the bound.
func (p *lengthProbe) bounded(n int64) (int64, error) {
	if n > p.max {
		return 0, fmt.Errorf("cml: GET /api/v0/pcap: %w", (&pcapFetch{max: p.max, total: n}).overBound())
	}
	return n, nil
}

// rangeTotal reads the whole length a Content-Range header ("bytes
// 0-0/1234") names.
func rangeTotal(header string) (int64, error) {
	_, total, ok := strings.Cut(strings.TrimSpace(header), "/")
	n, err := strconv.ParseInt(total, 10, 64)
	if !ok || !strings.HasPrefix(header, "bytes ") || err != nil || n < 0 {
		return 0, fmt.Errorf("cml: the controller answered a range with Content-Range %q, which names no length", header)
	}
	return n, nil
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
// third word is the bytes captured, then those bytes. Each length is
// compared as an int64, so one of 2^31 or more cannot turn negative where
// int is 32 bits and pass as fitting.
func countPCAP(data []byte, order binary.ByteOrder) (int, bool) {
	n := 0
	for off := 24; off < len(data); n++ {
		if len(data)-off < 16 {
			return n, false
		}
		size := int64(order.Uint32(data[off+8:]))
		if size > int64(len(data)-off-16) {
			return n, false
		}
		off += 16 + int(size)
	}
	return n, true
}

// countBlocks counts a pcapng file's packet blocks (enhanced, simple and
// the obsolete packet block), each section in the byte order its section
// header block names. Each block's length is compared as an int64, as
// countPCAP compares a record's.
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
		kind, size := order.Uint32(data[off:]), int64(order.Uint32(data[off+4:]))
		if size < 12 || size > int64(len(data)-off) {
			return n, false
		}
		if kind == 2 || kind == 3 || kind == 6 {
			n++
		}
		off += int(size)
	}
	return n, true
}

// Package cmltest: a capture's file, served by its key as a whole file or
// in byte ranges, and cut off halfway when a test asks, as a real
// controller's front end reset the stream of a large capture's download
// (2026-11-17).
package cmltest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// pcapTime is the modification time a ranged capture file is served with.
var pcapTime = time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)

// servePCAP answers a capture's file by its key.
func (c *Controller) servePCAP(w http.ResponseWriter, r *http.Request, key string) {
	c.PCAPRequests = append(c.PCAPRequests, r.Proto+" "+r.Header.Get("Range"))
	for _, lab := range c.Labs {
		for _, l := range lab.Links {
			if l.Capture == nil || l.Capture.Key != key {
				continue
			}
			data := c.PCAP
			if c.PCAPAt != nil {
				// A file the controller is still writing.
				data = c.PCAPAt(len(c.PCAPRequests))
			}
			if data == nil {
				data = emptyPCAP
			}
			w.Header().Set("Content-Type", "application/cap")
			out := http.ResponseWriter(w)
			if c.PCAPCuts > 0 {
				c.PCAPCuts--
				out = &cutWriter{ResponseWriter: w, left: len(data) / 4}
			}
			if c.PCAPRanges {
				sum := sha256.Sum256(data)
				w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
				http.ServeContent(out, r, "capture.pcap", pcapTime, bytes.NewReader(data))
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = out.Write(data)
			return
		}
	}
	fail(w, http.StatusNotFound, "no capture with key %s", key)
}

// cutWriter passes left bytes of the body on, sends them, then aborts the
// answer: over HTTP/2 the server resets the stream with INTERNAL_ERROR,
// over HTTP/1.1 it closes the connection. The abort panics with
// http.ErrAbortHandler, which the server takes as a deliberate abort and
// does not log.
type cutWriter struct {
	http.ResponseWriter
	left int
}

// Write implements io.Writer, cutting the body off at left bytes.
func (c *cutWriter) Write(p []byte) (int, error) {
	if len(p) <= c.left {
		c.left -= len(p)
		return c.ResponseWriter.Write(p)
	}
	_, _ = c.ResponseWriter.Write(p[:c.left])
	_ = http.NewResponseController(c.ResponseWriter).Flush()
	panic(http.ErrAbortHandler)
}

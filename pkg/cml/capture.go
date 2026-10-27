// Package cml: packet captures on a lab's links, started and stopped through
// the controller and fetched as a pcap file. Measured against CML 2.7.2's
// OpenAPI document and a real controller (2026-10-25): PUT
// /labs/{lab}/links/{link}/capture/start takes maxpackets or maxtime (one of
// them required) and an optional bpfilter; capture/stop ends it;
// capture/status answers {config, packetscaptured, starttime} while it runs
// and {} once it stops; capture/key names its key, a UUID; and GET
// /pcap/{key} returns the file as application/cap.
package cml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The bounds CML's API sets on a capture.
const (
	// MaxCapturePackets is the most packets one capture may take.
	MaxCapturePackets = 1000000
	// MaxCaptureSeconds is the longest one capture may run.
	MaxCaptureSeconds = 86400
	// MaxBPFilter is the longest Berkeley packet filter a capture takes.
	MaxBPFilter = 128
)

// CaptureSpec bounds a capture: at least one of Packets and Seconds, and an
// optional Berkeley packet filter.
type CaptureSpec struct {
	Packets int
	Seconds int
	Filter  string
}

// Validate refuses a capture the controller would refuse, or one with no
// bound.
func (s CaptureSpec) Validate() error {
	switch {
	case s.Packets == 0 && s.Seconds == 0:
		return errors.New("cml: a capture needs a bound: packets, seconds or both")
	case s.Packets < 0 || s.Packets > MaxCapturePackets:
		return fmt.Errorf("cml: a capture takes 1 to %d packets", MaxCapturePackets)
	case s.Seconds < 0 || s.Seconds > MaxCaptureSeconds:
		return fmt.Errorf("cml: a capture runs 1 to %d seconds", MaxCaptureSeconds)
	case len(s.Filter) > MaxBPFilter:
		return fmt.Errorf("cml: a capture filter is at most %d characters", MaxBPFilter)
	case strings.ContainsAny(s.Filter, "\r\n\x00"):
		return errors.New("cml: a capture filter is one line")
	}
	return nil
}

// StartCapture starts a capture on link id of lab, bounded by spec.
func (c *Client) StartCapture(ctx context.Context, lab, id string, spec CaptureSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	body := map[string]any{}
	if spec.Packets > 0 {
		body["maxpackets"] = spec.Packets
	}
	if spec.Seconds > 0 {
		body["maxtime"] = spec.Seconds
	}
	if spec.Filter != "" {
		body["bpfilter"] = spec.Filter
	}
	return c.send(ctx, http.MethodPut, linkPath(lab, id)+"/capture/start", body, nil)
}

// StopCapture stops the capture on link id of lab; the file stays to fetch.
func (c *Client) StopCapture(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodPut, linkPath(lab, id)+"/capture/stop", nil, nil)
}

// CaptureState is a link's capture as the controller reports it: while one
// runs, the bounds it was started with, how many packets it has taken and
// when it began; once it stops (or before one starts), nothing, which
// CML 2.7.2 answers as {} (measured 2026-10-25).
type CaptureState struct {
	Running bool
	Packets int
	Started string
	Spec    CaptureSpec
}

// CaptureStatus reads the capture on link id of lab.
func (c *Client) CaptureStatus(ctx context.Context, lab, id string) (CaptureState, error) {
	var raw struct {
		Config *struct {
			BPFilter   string `json:"bpfilter"`
			MaxPackets int    `json:"maxpackets"`
			MaxTime    int    `json:"maxtime"`
		} `json:"config"`
		PacketsCaptured int    `json:"packetscaptured"`
		StartTime       string `json:"starttime"`
	}
	if err := c.get(ctx, linkPath(lab, id)+"/capture/status", &raw); err != nil {
		return CaptureState{}, err
	}
	if raw.Config == nil {
		return CaptureState{}, nil
	}
	return CaptureState{
		Running: true, Packets: raw.PacketsCaptured, Started: raw.StartTime,
		Spec: CaptureSpec{Packets: raw.Config.MaxPackets, Seconds: raw.Config.MaxTime, Filter: raw.Config.BPFilter},
	}, nil
}

// CaptureKey is the key the capture on link id of lab is fetched by.
func (c *Client) CaptureKey(ctx context.Context, lab, id string) (string, error) {
	var key string
	if err := c.get(ctx, linkPath(lab, id)+"/capture/key", &key); err != nil {
		return "", err
	}
	if key == "" || len(key) > 64 || strings.ContainsAny(key, "/?#\r\n ") {
		return "", fmt.Errorf("cml: the controller gave a capture key this client will not put in a path")
	}
	return key, nil
}

// FetchPCAP returns the capture file the key names, refusing one over max
// bytes rather than reading it all.
func (c *Client) FetchPCAP(ctx context.Context, key string, max int64) ([]byte, error) {
	if c.token == "" {
		return nil, errors.New("cml: Connect first")
	}
	path := "/pcap/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+"/api/v0"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cml: GET /api/v0/pcap: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("cml: GET /api/v0/pcap: reading the file: %w", err)
	}
	if res.StatusCode/100 != 2 {
		return nil, &StatusError{Method: http.MethodGet, Path: "/pcap/<key>", Status: res.StatusCode, Body: string(data)}
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("cml: the capture is over %d bytes: bound it with fewer packets or less time", max)
	}
	if !isCapture(data) {
		return nil, errors.New("cml: the controller's answer is not a pcap or pcapng file")
	}
	return data, nil
}

// The magic numbers a capture file begins with: pcap in either byte order,
// with microsecond or nanosecond times, and pcapng's section header.
var captureMagics = [][]byte{
	{0xd4, 0xc3, 0xb2, 0xa1}, {0xa1, 0xb2, 0xc3, 0xd4},
	{0x4d, 0x3c, 0xb2, 0xa1}, {0xa1, 0xb2, 0x3c, 0x4d},
	{0x0a, 0x0d, 0x0d, 0x0a},
}

// isCapture reports whether data begins as a pcap or pcapng file does.
func isCapture(data []byte) bool {
	for _, m := range captureMagics {
		if bytes.HasPrefix(data, m) {
			return true
		}
	}
	return false
}

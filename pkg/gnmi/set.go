// Package gnmi: Set, the one write gNMI has: deletes, then replaces, then
// updates, applied by the device as one transaction (gNMI specification
// section 3.4.3), so either all of a request takes effect or none of it.
package gnmi

import (
	"context"
	"errors"
	"fmt"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetValue is one replace or update: a node and the JSON value to give it.
type SetValue struct {
	// Path is the node, relative to the request's prefix; it must name one
	// node, with no wildcard.
	Path Path
	// JSON is the value, one JSON document; CanonicalJSON's bounds hold.
	JSON []byte
	// Encoding is how it is sent: EncodingJSONIETF (RFC 7951, the default
	// a runbook takes) or EncodingJSON.
	Encoding Encoding
}

// SetRequest is one Set.
type SetRequest struct {
	// Prefix is prepended to every path, the zero Path for none.
	Prefix Path
	// Delete are nodes to remove.
	Delete []Path
	// Replace are nodes to replace whole: what is there and not in the
	// value is removed.
	Replace []SetValue
	// Update are nodes to merge a value into.
	Update []SetValue
	// PrefixTarget is the prefix's target (ValidatePrefixTarget); empty
	// takes the client's (Options.PrefixTarget).
	PrefixTarget string
}

// SetOperation is one operation a Set's answer reports.
type SetOperation struct {
	// Path is the node, relative to the answer's prefix.
	Path Path
	// Op is delete, replace, update or union_replace.
	Op string
}

// SetResult is what a Set's answer said.
type SetResult struct {
	// Timestamp is when the device applied the change, nanoseconds since
	// the Unix epoch.
	Timestamp int64
	// Prefix is the answer's prefix.
	Prefix Path
	// Operations are the operations the device reports, in its order.
	Operations []SetOperation
}

// Set sends req once, over a connection that came up (so a failure past
// that point is a lost answer, never a refusal of something unsent). An
// empty request, a wildcard, a value that is not one JSON document within the
// bounds, and a request past MaxSendBytes are refused before anything is
// sent. The device's refusal is an *Error, its own words masked; a lost
// answer an *AnswerLostError, and so is an OK answer this client refuses to
// read, with Applied set: the device applied the Set, so a caller reading the
// failure as a refusal would take a change made for one never made.
func (c *Client) Set(ctx context.Context, req SetRequest) (SetResult, error) {
	if err := c.refused(); err != nil {
		return SetResult{}, err
	}
	wire, err := req.proto(c.targetOf(req.PrefixTarget))
	if err != nil {
		return SetResult{}, err
	}
	// One bound covers the connection coming up and the answer, so a server
	// that never answers ends the Set as a lost answer at the call timeout.
	call, cancel := c.call(ctx)
	defer cancel()
	// A server refusing the client certificate is met here, never below: a
	// connection is ready only once the server's HTTP/2 preface has been read
	// (grpc-go's transport waits for it), and a TLS 1.3 server sends its
	// refusal before any data, so a Set sent over a ready connection went to a
	// server that accepted the certificate.
	if err := c.ready(call); err != nil {
		return SetResult{}, err
	}
	resp, err := c.api.Set(call, wire)
	if err != nil {
		if lost(err) {
			return SetResult{}, &AnswerLostError{Err: c.fail(call, "Set", err)}
		}
		return SetResult{}, c.fail(call, "Set", err)
	}
	result, err := readSetResponse(resp)
	if err != nil {
		// An OK status is gNMI's word that the whole Set was applied (section
		// 3.4.3); only the report of it is unreadable.
		return SetResult{}, &AnswerLostError{Err: err, Applied: true}
	}
	return result, nil
}

// lost reports whether a Set's failure leaves its outcome unknown: the
// caller's deadline or cancellation, or the connection breaking, after the
// request went out. A status the server chose (InvalidArgument, a refused
// login and the rest) is its answer, and Unavailable is a lost answer only
// because ready proved the connection was up when the request was sent.
func lost(err error) bool {
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable:
		return true
	}
	return false
}

// proto checks req and returns it on the wire, its prefix naming target.
func (req SetRequest) proto(target string) (*gpb.SetRequest, error) {
	if len(req.Delete)+len(req.Replace)+len(req.Update) == 0 {
		return nil, errors.New("the Set names nothing to delete, replace or update")
	}
	if err := req.Prefix.Validate(false); err != nil {
		return nil, fmt.Errorf("prefix: %w", err)
	}
	if err := ValidatePrefixTarget(target); err != nil {
		return nil, err
	}
	out := &gpb.SetRequest{Prefix: prefixProto(req.Prefix, target)}
	size := 0
	for _, p := range req.Delete {
		if err := p.Validate(false); err != nil {
			return nil, fmt.Errorf("delete %s: %w", quoted(p.String()), err)
		}
		out.Delete = append(out.Delete, p.proto())
	}
	values := func(kind string, in []SetValue) ([]*gpb.Update, error) {
		var updates []*gpb.Update
		for _, v := range in {
			if err := v.Path.Validate(false); err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, quoted(v.Path.String()), err)
			}
			raw, err := CanonicalJSON(v.JSON)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, shown(v.Path), err)
			}
			size += len(raw)
			typed, err := typedJSON(raw, v.Encoding)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, shown(v.Path), err)
			}
			updates = append(updates, &gpb.Update{Path: v.Path.proto(), Val: typed})
		}
		return updates, nil
	}
	var err error
	if out.Replace, err = values("replace", req.Replace); err != nil {
		return nil, err
	}
	if out.Update, err = values("update", req.Update); err != nil {
		return nil, err
	}
	if size > MaxSendBytes {
		return nil, fmt.Errorf("the Set's values are %d bytes, more than %d", size, MaxSendBytes)
	}
	return out, nil
}

// readSetResponse decodes a Set's answer.
func readSetResponse(resp *gpb.SetResponse) (SetResult, error) {
	prefix, err := pathFromProto(resp.GetPrefix())
	if err != nil {
		return SetResult{}, fmt.Errorf("the Set answer's prefix: %w", err)
	}
	out := SetResult{Timestamp: resp.GetTimestamp(), Prefix: prefix}
	if len(resp.GetResponse()) > MaxUpdates {
		return SetResult{}, fmt.Errorf("the Set answer reports %d operations, more than %d", len(resp.GetResponse()), MaxUpdates)
	}
	for _, r := range resp.GetResponse() {
		path, err := pathFromProto(r.GetPath())
		if err != nil {
			return SetResult{}, fmt.Errorf("an operation's path in the Set answer: %w", err)
		}
		op := "unknown"
		switch r.GetOp() {
		case gpb.UpdateResult_DELETE:
			op = "delete"
		case gpb.UpdateResult_REPLACE:
			op = "replace"
		case gpb.UpdateResult_UPDATE:
			op = "update"
		case gpb.UpdateResult_UNION_REPLACE:
			op = "union_replace"
		}
		out.Operations = append(out.Operations, SetOperation{Path: path, Op: op})
	}
	return out, nil
}

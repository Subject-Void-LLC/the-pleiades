// Package gnmi: Get, one read of the paths named, and the notifications it
// and Subscribe answer with, decoded and held to their bounds: an answer
// breaking one is refused whole, never cut, since a read cut short would
// look like a device holding less than it does.
package gnmi

import (
	"context"
	"errors"
	"fmt"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// MaxUpdates bounds the updates one answer (a Get's, or a subscription's
// round up to its sync) may carry, deletes included.
const MaxUpdates = 1 << 20

// Update is one value a device answered, at its path.
type Update struct {
	// Path is the node, relative to its notification's prefix.
	Path Path
	// Value is its value.
	Value Value
	// Duplicates counts the updates a target coalesced into this one.
	Duplicates uint32
}

// Notification is one set of updates a device answered with.
type Notification struct {
	// Timestamp is when the values were read, nanoseconds since the Unix
	// epoch, as the device wrote it.
	Timestamp int64
	// Prefix is prepended to every update's and delete's path.
	Prefix Path
	// Updates are the values.
	Updates []Update
	// Deletes are the paths the device says no longer exist.
	Deletes []Path
	// Atomic is true when the updates are one indivisible set.
	Atomic bool
}

// GetRequest is one Get.
type GetRequest struct {
	// Prefix is prepended to every path, the zero Path for none.
	Prefix Path
	// Paths are the nodes to read; wildcards are allowed. None reads the
	// whole tree under Prefix.
	Paths []Path
	// Encoding is how values come back.
	Encoding Encoding
	// Type is which data is read.
	Type DataType
	// PrefixTarget is the prefix's target (ValidatePrefixTarget); empty
	// takes the client's (Options.PrefixTarget), and NoPrefixTarget none.
	PrefixTarget string
}

// Get reads the paths req names. A path that does not exist is the server's
// own answer, an *Error whose Code is NotFound as gNMI specifies.
func (c *Client) Get(ctx context.Context, req GetRequest) ([]Notification, error) {
	if err := c.refused(); err != nil {
		return nil, err
	}
	wire, err := req.proto(c.targetOf(req.PrefixTarget))
	if err != nil {
		return nil, err
	}
	call, cancel := c.call(ctx)
	defer cancel()
	resp, err := c.api.Get(call, wire)
	if err != nil {
		return nil, c.fail(call, "Get", err)
	}
	return decodeNotifications(resp.GetNotification())
}

// proto checks req and returns it on the wire, its prefix naming target.
func (req GetRequest) proto(target string) (*gpb.GetRequest, error) {
	if req.Type > DataOperational {
		return nil, fmt.Errorf("data type %d is not one gNMI names", req.Type)
	}
	if int(req.Encoding) >= len(encodingNames) {
		return nil, fmt.Errorf("encoding %d is not one gNMI names", req.Encoding)
	}
	out := &gpb.GetRequest{Encoding: req.Encoding.proto(), Type: gpb.GetRequest_DataType(req.Type)}
	if err := req.Prefix.Validate(false); err != nil {
		return nil, fmt.Errorf("prefix: %w", err)
	}
	if err := ValidatePrefixTarget(target); err != nil {
		return nil, err
	}
	out.Prefix = prefixProto(req.Prefix, target)
	for _, p := range req.Paths {
		if err := p.Validate(true); err != nil {
			return nil, fmt.Errorf("path %s: %w", quoted(p.String()), err)
		}
		out.Path = append(out.Path, p.proto())
	}
	return out, nil
}

// decodeNotifications decodes an answer's notifications, refusing the whole
// answer past MaxUpdates or at its first path or value that breaks a rule.
func decodeNotifications(in []*gpb.Notification) ([]Notification, error) {
	out := make([]Notification, 0, len(in))
	total := 0
	for _, n := range in {
		total += len(n.GetUpdate()) + len(n.GetDelete())
		if total > MaxUpdates {
			return nil, fmt.Errorf("the device answered more than %d updates", MaxUpdates)
		}
		decoded, err := decodeNotification(n)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

// decodeNotification decodes one notification.
func decodeNotification(n *gpb.Notification) (Notification, error) {
	if n == nil {
		return Notification{}, errors.New("the device sent an empty notification")
	}
	prefix, err := pathFromProto(n.GetPrefix())
	if err != nil {
		return Notification{}, fmt.Errorf("a notification's prefix: %w", err)
	}
	out := Notification{Timestamp: n.GetTimestamp(), Prefix: prefix, Atomic: n.GetAtomic()}
	if len(n.GetUpdate()) > 0 {
		out.Updates = make([]Update, 0, len(n.GetUpdate()))
	}
	for _, u := range n.GetUpdate() {
		path, err := pathFromProto(u.GetPath())
		if err != nil {
			return Notification{}, fmt.Errorf("an update's path: %w", err)
		}
		if u.GetVal() == nil && u.GetValue() != nil {
			// gNMI 0.4.0 replaced the value field with val; its bytes carry
			// an encoding of the target's choosing, never read here.
			return Notification{}, fmt.Errorf("the value at %s came in the value field gNMI 0.4.0 deprecated, which this client does not read", shown(Join(prefix, path)))
		}
		value, err := decodeValue(u.GetVal())
		if err != nil {
			return Notification{}, fmt.Errorf("the value at %s: %w", shown(Join(prefix, path)), err)
		}
		out.Updates = append(out.Updates, Update{Path: path, Value: value, Duplicates: u.GetDuplicates()})
	}
	for _, d := range n.GetDelete() {
		path, err := pathFromProto(d)
		if err != nil {
			return Notification{}, fmt.Errorf("a deleted path: %w", err)
		}
		out.Deletes = append(out.Deletes, path)
	}
	return out, nil
}

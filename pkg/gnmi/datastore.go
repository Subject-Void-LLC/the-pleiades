// Package gnmi: the client as pkg/datastore's shared port, so a caller
// written against the port reaches a gNMI device the way it reaches a NETCONF
// one. GetConfig is a Get of the configuration in JSON_IETF; SetConfig is one
// Set whose operation is the port's (merge as update, replace, delete). gNMI
// qualifies a node by its module's name in the element ("module:node"), never
// by a namespace, so a path element naming a namespace is refused rather
// than guessed at.
package gnmi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// The client implements the port.
var _ datastore.Store = (*Client)(nil)

// GetConfig implements datastore.Store: the configuration p names, read in
// JSON_IETF, as one JSON object mapping each node's full path to its value
// (empty when the device holds nothing there).
func (c *Client) GetConfig(ctx context.Context, p datastore.Path) (datastore.Payload, error) {
	path, err := fromDatastore(p)
	if err != nil {
		return datastore.Payload{}, err
	}
	ns, err := c.Get(ctx, GetRequest{Paths: []Path{path}, Encoding: EncodingJSONIETF, Type: DataConfig})
	// A device holding nothing at p answers NOT_FOUND, as the gNMI
	// specification says, or, as Nokia SR Linux does, a notification with no
	// update: both are the empty object, as a NETCONF get-config of nothing
	// is empty data.
	if err != nil && Code(err) != codes.NotFound {
		return datastore.Payload{}, err
	}
	nodes := map[string]json.RawMessage{}
	for _, n := range ns {
		for _, u := range n.Updates {
			if u.Value.Type != TypeNone {
				nodes[Join(n.Prefix, u.Path).String()] = u.Value.JSON
			}
		}
	}
	doc, err := json.Marshal(nodes)
	if err != nil {
		return datastore.Payload{}, err
	}
	return datastore.Payload{Encoding: datastore.EncodingJSONIETF, Bytes: doc}, nil
}

// SetConfig implements datastore.Store: one Set of payload at p with op,
// payload's encoding JSON_IETF or JSON (gNMI's own two JSON encodings); a
// delete takes no payload, and is refused one.
func (c *Client) SetConfig(ctx context.Context, p datastore.Path, payload datastore.Payload, op datastore.Operation) error {
	if !op.Valid() {
		return fmt.Errorf("operation %d is not one the port names", op)
	}
	path, err := fromDatastore(p)
	if err != nil {
		return err
	}
	if op == datastore.OperationDelete {
		if !payload.IsEmpty() {
			return errors.New("a delete takes no payload, and one was given")
		}
		_, err := c.Set(ctx, SetRequest{Delete: []Path{path}})
		return err
	}
	var enc Encoding
	switch payload.Encoding {
	case datastore.EncodingJSONIETF:
		enc = EncodingJSONIETF
	case datastore.EncodingJSON:
		enc = EncodingJSON
	default:
		return fmt.Errorf("gNMI sends configuration as json_ietf or json, not %s", payload.Encoding)
	}
	value := []SetValue{{Path: path, JSON: payload.Bytes, Encoding: enc}}
	req := SetRequest{Update: value}
	if op == datastore.OperationReplace {
		req = SetRequest{Replace: value}
	}
	_, err = c.Set(ctx, req)
	return err
}

// fromDatastore is a port path as a gNMI path, refusing a namespace.
func fromDatastore(p datastore.Path) (Path, error) {
	var out Path
	for _, e := range p.Elem {
		if e.Namespace != "" {
			return Path{}, fmt.Errorf("element %s names the namespace %s; gNMI names a module by prefix in the element (module:name), not by namespace", quoted(e.Name), quoted(e.Namespace))
		}
		elem := Elem{Name: e.Name}
		if len(e.Keys) > 0 {
			elem.Keys = maps.Clone(e.Keys)
		}
		out.Elems = append(out.Elems, elem)
	}
	if err := out.Validate(false); err != nil {
		return Path{}, err
	}
	return out, nil
}

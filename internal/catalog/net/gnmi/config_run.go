// Package gnmi: net.gnmi.config's run. Every refusal is made before anything
// is written: the parameters, every path, every value, and the encoding
// against what the device's Capabilities said. The secret references are
// resolved just before the Set, every path is read before and after it, and
// the undo is recorded only where it is exact.
package gnmi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// maxOperations bounds the paths one task may write.
const maxOperations = 1024

// value is one update or replace: its full path and its value as the task
// wrote it, secret references unresolved.
type value struct {
	path gnmi.Path
	val  any
}

// writeRequest is one checked call.
type writeRequest struct {
	prefix   gnmi.Path
	deletes  []gnmi.Path
	replaces []value
	updates  []value
	encoding gnmi.Encoding
	backup   bool
}

// Config implements the "net.gnmi.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := configRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if device == nil {
		return collection.Result{}, errors.New(configFQCN + ": the task has no target device")
	}
	client, warnings, err := gnmi.ClientFor(device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", configFQCN, device.Name(), err)
	}
	defer func() { _ = client.Close() }()
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w; nothing was written", configFQCN, device.Name(), err)
	}
	found, err := recordCapabilities(rc, client.Verified(), caps)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if len(caps.Encodings) > 0 && !caps.Supports(req.encoding) {
		return collection.Result{}, fmt.Errorf("%s: device %q answers in %v and not in %s; nothing was sent", configFQCN, device.Name(), caps.Encodings, req.encoding)
	}
	res, notes, err := write(ctx, rc, client, req)
	found = append(found, notes...)
	if len(found) > 0 {
		if werr := sdk.RecordWarnings(rc, append(append([]string(nil), warnings...), found...)); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return res, fmt.Errorf("%s: device %q: %w", configFQCN, device.Name(), err)
	}
	return res, nil
}

// configRequest reads and checks the parameters, refusing every malformed
// one before anything is reached.
func configRequest(params map[string]any) (writeRequest, error) {
	var req writeRequest
	rawPrefix, err := sdk.OptionalStringParam(params, paramPrefix)
	if err != nil {
		return req, err
	}
	if rawPrefix != "" {
		if req.prefix, err = gnmi.ParsePath(rawPrefix); err != nil {
			return req, fmt.Errorf("%s: %w", paramPrefix, err)
		}
	}
	deletes, _, err := sdk.StringSlice(params, paramDelete)
	if err != nil {
		return req, err
	}
	for i, raw := range deletes {
		p, err := fullPath(req.prefix, raw)
		if err != nil {
			return req, fmt.Errorf("%s[%d]: %w", paramDelete, i, err)
		}
		req.deletes = append(req.deletes, p)
	}
	if req.replaces, err = values(params, paramReplace, req.prefix); err != nil {
		return req, err
	}
	if req.updates, err = values(params, paramUpdate, req.prefix); err != nil {
		return req, err
	}
	switch n := len(req.deletes) + len(req.replaces) + len(req.updates); {
	case n == 0:
		return req, fmt.Errorf("the task names nothing to %s, %s or %s", paramUpdate, paramReplace, paramDelete)
	case n > maxOperations:
		return req, fmt.Errorf("the task names %d paths, more than %d", n, maxOperations)
	}
	encoding, err := sdk.OptionalStringParam(params, paramEncoding)
	if err != nil {
		return req, err
	}
	switch strings.ToLower(encoding) {
	case "", "json_ietf":
		req.encoding = gnmi.EncodingJSONIETF
	case "json":
		req.encoding = gnmi.EncodingJSON
	default:
		return req, fmt.Errorf("%s must be json_ietf or json", paramEncoding)
	}
	if req.backup, err = sdk.BoolParamOr(params, paramBackup, false); err != nil {
		return req, err
	}
	return req, nil
}

// fullPath parses raw, a path relative to prefix, and joins it, refusing a
// wildcard: a write names one node.
func fullPath(prefix gnmi.Path, raw string) (gnmi.Path, error) {
	p, err := gnmi.ParsePath(raw)
	if err != nil {
		return gnmi.Path{}, err
	}
	full := gnmi.Join(prefix, p)
	if err := full.Validate(false); err != nil {
		return gnmi.Path{}, err
	}
	return full, nil
}

// values reads an update or replace list: each entry exactly {path, val}, its
// path parsed and joined under prefix, its value one JSON document holding no
// masked placeholder and only well formed secret references.
func values(params map[string]any, key string, prefix gnmi.Path) ([]value, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of {%s, %s}", key, entryPath, entryValue)
	}
	out := make([]value, 0, len(items))
	for i, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a mapping of %s and %s", key, i, entryPath, entryValue)
		}
		for k := range entry {
			if k != entryPath && k != entryValue {
				return nil, fmt.Errorf("%s[%d] holds %q; an entry holds %s and %s only", key, i, k, entryPath, entryValue)
			}
		}
		rawPath, _ := entry[entryPath].(string)
		if rawPath == "" {
			return nil, fmt.Errorf("%s[%d].%s is required, as text", key, i, entryPath)
		}
		p, err := fullPath(prefix, rawPath)
		if err != nil {
			return nil, fmt.Errorf("%s[%d].%s: %w", key, i, entryPath, err)
		}
		val, present := entry[entryValue]
		if !present || val == nil {
			return nil, fmt.Errorf("%s[%d].%s is required", key, i, entryValue)
		}
		doc, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("%s[%d].%s is not a value JSON can hold: %w", key, i, entryValue, err)
		}
		if _, err := gnmi.CanonicalJSON(doc); err != nil {
			return nil, fmt.Errorf("%s[%d].%s: %w", key, i, entryValue, err)
		}
		if err := netcfg.RefuseMasked(fmt.Sprintf("%s[%d].%s", key, i, entryValue), string(doc)); err != nil {
			return nil, err
		}
		if err := checkRefs(val); err != nil {
			return nil, fmt.Errorf("%s[%d].%s: %w", key, i, entryValue, err)
		}
		out = append(out, value{path: p, val: val})
	}
	return out, nil
}

// checkRefs refuses a string in v holding a malformed secret reference, or a
// reference beside any other template expression, and a map key holding one.
func checkRefs(v any) error {
	switch t := v.(type) {
	case string:
		if problems := secretref.Problems(t); len(problems) > 0 {
			return errors.New(strings.Join(problems, "; "))
		}
	case []any:
		for _, e := range t {
			if err := checkRefs(e); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, e := range t {
			if secretref.Has(k) {
				return errors.New("a key may not hold a secret reference; only a string value may")
			}
			if err := checkRefs(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve returns v with each secret reference in its strings resolved from
// the run's named secrets, each value substituted joining the client's and
// the run's masking (sdk.ResolveSecretRefs), as the JSON document to send.
func resolve(rc sdk.RunbookContext, client *gnmi.Client, v any) ([]byte, error) {
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch t := v.(type) {
		case string:
			out, substituted, err := sdk.ResolveSecretRefs(rc, t)
			if err != nil {
				return nil, err
			}
			client.MaskAlso(substituted...)
			return out, nil
		case []any:
			out := make([]any, len(t))
			for i, e := range t {
				resolved, err := walk(e)
				if err != nil {
					return nil, err
				}
				out[i] = resolved
			}
			return out, nil
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, e := range t {
				resolved, err := walk(e)
				if err != nil {
					return nil, err
				}
				out[k] = resolved
			}
			return out, nil
		}
		return v, nil
	}
	resolved, err := walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resolved)
}

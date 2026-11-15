// Package restconf: reading a resource path a person wrote, RFC 8040 section
// 3.5.3's api-path (module:node/list=key1,key2/...), into the segments the
// path builder rebuilds. Nothing written is passed on as it is: each node is
// held to the identifier grammar and each key is decoded exactly once, then
// encoded again by EscapeKey, so a path either parses into nodes and keys or
// is refused before anything is sent.
package restconf

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// maxPathTextBytes bounds a written path.
const maxPathTextBytes = 16 << 10

// ParsePath reads s, an api-path as RFC 8040 writes it, with or without one
// leading "/": "ietf-interfaces:interfaces/interface=GigabitEthernet0%2F1".
// A key may be percent-encoded and is decoded once; a "/", "," or "=" inside a
// key must be (RFC 8040 section 3.5.3.1), and so must anything but RFC 3986's
// unreserved characters and the sub-delimiters other than ",". An empty path,
// an empty node ("a//b"), a query, a fragment, a dot segment, a node that is
// not a YANG identifier, an unqualified first node and a key that is not
// UTF-8 or holds a control character are each refused.
func ParsePath(s string) (Path, error) {
	if len(s) > maxPathTextBytes {
		return nil, fmt.Errorf("the path is longer than %d bytes", maxPathTextBytes)
	}
	if strings.ContainsAny(s, "?#") {
		return nil, errors.New("the path carries a query or a fragment, which a resource path does not")
	}
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return nil, errors.New("the path names no resource")
	}
	var p Path
	for i, raw := range strings.Split(s, "/") {
		seg, err := parseSegment(raw)
		if err != nil {
			return nil, fmt.Errorf("path node %d: %w", i+1, err)
		}
		p = append(p, seg)
	}
	// Rebuilding it holds the result to every rule the builder holds.
	if _, err := p.Escaped(); err != nil {
		return nil, err
	}
	return p, nil
}

// parseSegment reads one api-identifier or list-instance.
func parseSegment(raw string) (Segment, error) {
	if raw == "" {
		return Segment{}, errors.New("the node is empty (two slashes together, or one at the end)")
	}
	name, keyText, entry := strings.Cut(raw, "=")
	var seg Segment
	if module, node, qualified := strings.Cut(name, ":"); qualified {
		seg.Module, seg.Name = module, node
	} else {
		seg.Name = name
	}
	if !entry {
		return seg, nil
	}
	keys := []string{}
	for _, k := range strings.Split(keyText, ",") {
		decoded, err := decodeKey(k)
		if err != nil {
			return Segment{}, fmt.Errorf("a key of %s: %w", name, err)
		}
		keys = append(keys, decoded)
	}
	seg.Keys = keys
	return seg, nil
}

// decodeKey is one written key value, decoded once. Each byte must be RFC
// 3986 unreserved, a sub-delimiter other than "," (which separates keys) or
// "=", or part of a percent-encoding.
func decodeKey(k string) (string, error) {
	for i := 0; i < len(k); i++ {
		c := k[i]
		if unreserved(c) || c == '%' || strings.IndexByte("!$&'()*+;:@", c) >= 0 {
			continue
		}
		return "", fmt.Errorf("%q must be percent-encoded in a key", string(c))
	}
	decoded, err := url.PathUnescape(k)
	if err != nil {
		return "", errors.New("the key holds a percent sign that begins no escape")
	}
	return decoded, nil
}

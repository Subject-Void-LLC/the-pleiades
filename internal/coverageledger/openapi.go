// Package coverageledger: the operations of an OpenAPI or Swagger 2 document.
package coverageledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// openAPIMethods are the HTTP methods an OpenAPI path item may define an
// operation under, in the order operations are listed for one path.
var openAPIMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// pathItemOther are the keys of an OpenAPI path item that are not operations.
var pathItemOther = map[string]bool{"parameters": true, "servers": true, "summary": true, "description": true}

// ExtractOpenAPI returns every operation an OpenAPI 3 or Swagger 2 document
// defines, as "METHOD /path" with the operation's summary, ordered by path
// and then method. A Swagger 2 basePath is part of each path. A key it cannot
// read as an operation or as one of a path item's other keys (an upper-case
// method, a $ref to a path item defined elsewhere) is refused rather than
// skipped, since a skipped one would be an operation the ledger never counts.
//
// Keys are matched exactly and a repeated key is refused: encoding/json would
// otherwise read "Paths" as "paths" and keep only the last of two "/a" items,
// counting fewer operations than the file holds.
func ExtractOpenAPI(doc []byte) ([]Op, error) {
	if err := refuseRepeatedKeys(doc); err != nil {
		return nil, fmt.Errorf("reading the OpenAPI document: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(doc, &top); err != nil {
		return nil, fmt.Errorf("reading the OpenAPI document: %w", err)
	}
	for k := range top {
		for _, want := range []string{"paths", "basePath"} {
			if k != want && strings.EqualFold(k, want) {
				return nil, fmt.Errorf("the document has key %q, which is not %q: refused rather than guessed at", k, want)
			}
		}
	}
	var parsed struct {
		BasePath string
		Paths    map[string]map[string]json.RawMessage
	}
	if raw, ok := top["basePath"]; ok {
		if err := json.Unmarshal(raw, &parsed.BasePath); err != nil {
			return nil, fmt.Errorf("reading basePath: %w", err)
		}
	}
	if raw, ok := top["paths"]; ok {
		if err := json.Unmarshal(raw, &parsed.Paths); err != nil {
			return nil, fmt.Errorf("reading paths: %w", err)
		}
	}
	if len(parsed.Paths) == 0 {
		return nil, fmt.Errorf("the OpenAPI document defines no paths")
	}
	paths := make([]string, 0, len(parsed.Paths))
	for p := range parsed.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	base := strings.TrimSuffix(parsed.BasePath, "/")
	known := map[string]bool{}
	for _, m := range openAPIMethods {
		known[m] = true
	}
	var ops []Op
	for _, p := range paths {
		item := parsed.Paths[p]
		for key := range item {
			if !known[key] && !pathItemOther[key] && !strings.HasPrefix(key, "x-") {
				return nil, fmt.Errorf("path %s has key %q, which is neither an operation nor a path item field this reads", p, key)
			}
		}
		for _, method := range openAPIMethods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			var op struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("reading %s %s: %w", strings.ToUpper(method), p, err)
			}
			ops = append(ops, Op{ID: strings.ToUpper(method) + " " + base + p, Summary: strings.TrimSpace(op.Summary)})
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("the OpenAPI document defines no operations")
	}
	return ops, nil
}

// refuseRepeatedKeys reports a JSON object anywhere in doc that names one key
// twice.
func refuseRepeatedKeys(doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	// Each open object's keys seen so far; nil marks an open array.
	var stack []map[string]bool
	expectKey := func() bool { return len(stack) > 0 && stack[len(stack)-1] != nil }
	key := true // whether the next string in an object is a key
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, map[string]bool{})
				key = true
				continue
			case '[':
				stack = append(stack, nil)
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
		case string:
			if expectKey() && key {
				if stack[len(stack)-1][t] {
					return fmt.Errorf("key %q appears twice in one object", t)
				}
				stack[len(stack)-1][t] = true
				key = false
				continue
			}
		}
		// A value ended: in an object, a key comes next.
		key = expectKey()
	}
}

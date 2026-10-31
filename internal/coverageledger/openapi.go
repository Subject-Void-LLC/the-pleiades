// Package coverageledger: the operations of an OpenAPI or Swagger 2 document.
package coverageledger

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// openAPIMethods are the HTTP methods an OpenAPI path item may define an
// operation under, in the order operations are listed for one path.
var openAPIMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// ExtractOpenAPI returns every operation an OpenAPI 3 or Swagger 2 document
// defines, as "METHOD /path" with the operation's summary, ordered by path
// and then method. A path item's other keys (parameters, servers, a $ref) are
// not operations and are skipped.
func ExtractOpenAPI(doc []byte) ([]Op, error) {
	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return nil, fmt.Errorf("reading the OpenAPI document: %w", err)
	}
	if len(parsed.Paths) == 0 {
		return nil, fmt.Errorf("the OpenAPI document defines no paths")
	}
	paths := make([]string, 0, len(parsed.Paths))
	for p := range parsed.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var ops []Op
	for _, p := range paths {
		item := parsed.Paths[p]
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
			ops = append(ops, Op{ID: strings.ToUpper(method) + " " + p, Summary: strings.TrimSpace(op.Summary)})
		}
	}
	return ops, nil
}

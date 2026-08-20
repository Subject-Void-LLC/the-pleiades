package filters

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
	yaml "go.yaml.in/yaml/v3"
)

// MaxStructuredInputBytes is the longest raw JSON/YAML/XML document, or
// CSV line, any function in this file will parse or produce. It is
// deliberately larger than MaxInputBytes: a document-shaped filter's
// input is not a flat scalar (an FQDN, a MAC address) but a structured
// payload such as an Ansible fact blob or a device configuration
// excerpt, routinely tens of kilobytes. 1 MiB leaves generous headroom
// over that while still refusing a pathological, multi-hundred-megabyte
// string before it ever reaches encoding/json, go.yaml.in/yaml/v3 or
// encoding/xml, matching MaxInputBytes' own doc comment's own
// anticipation that a document-shaped filter family would need its own
// separate, larger, separately justified bound rather than raising the
// flat-scalar one.
const MaxStructuredInputBytes = 1 << 20 // 1 MiB

// maxStructuredDepth bounds recursion for every function in this file
// that walks a nested map/list/document: Flatten, Unflatten, DeepMerge,
// and the tree each of YAMLToJSON/JSONToYAML/XMLToJSON decodes before
// re-encoding it. It exists for the same reason MaxInputBytes does (the
// expression engine's own per-call cost limit cannot see how deep a
// value is nested, only that a call happened), but bounds shape rather
// than length: a legitimate document in this project's own domain
// (Ansible facts, device configuration, catalog metadata) does not nest
// more than a handful of levels deep, so 32 leaves an order of magnitude
// of headroom while still refusing a pathological or maliciously
// constructed document nested thousands of levels deep, which would
// otherwise recurse until the goroutine's stack overflows.
const maxStructuredDepth = 32

// Flatten flattens a nested map/list structure into a single-level map
// with dot-notation keys: {"a":{"b":1},"c":[1,2]} becomes
// {"a.b":1,"c.0":1,"c.1":2}. A list element's key segment is its
// zero-based index, so a flattened key may name either a map field or a
// list element depending on what produced it; Unflatten reverses this
// exact convention, including its one real ambiguity (see Unflatten's
// own doc comment).
//
// A branch nested past maxStructuredDepth is not descended into
// further: its value is copied at its current key, still nested, rather
// than silently dropped or recursing unboundedly. m is never mutated;
// Flatten always returns a new map.
func Flatten(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		flattenInto(out, k, v, 1)
	}
	return out
}

func flattenInto(out map[string]any, key string, v any, depth int) {
	if depth >= maxStructuredDepth {
		out[key] = v
		return
	}
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			out[key] = t
			return
		}
		for k, val := range t {
			flattenInto(out, key+"."+k, val, depth+1)
		}
	case []any:
		if len(t) == 0 {
			out[key] = t
			return
		}
		for i, val := range t {
			flattenInto(out, key+"."+strconv.Itoa(i), val, depth+1)
		}
	default:
		out[key] = v
	}
}

// Unflatten reconstructs a nested map/list structure from a flat map
// with dot-notation keys, the inverse of Flatten: {"a.b":1,"c.0":1} goes
// back to {"a":{"b":1},"c":[1]}. A key segment made entirely of digits
// is treated as a list index; a container whose every key is a
// contiguous run of digits starting at "0" becomes a JSON-shaped list
// rather than a map. m is never mutated; Unflatten always returns a new
// map. A dot-separated key nested past maxStructuredDepth stops
// descending at that depth (the remaining segments join back into one
// literal key instead of being silently dropped).
//
// The list-vs-map decision above is a real, inherent ambiguity in a
// dot-notation flatten/unflatten scheme, not an oversight: a map whose
// own keys genuinely are "0", "1", "2" (never built from Flatten's own
// list handling) round-trips back as a list, because nothing in the flat
// representation records which one it started as. This is the same
// limitation every popular flatten/unflatten library in this class
// carries; it is why Flatten/Unflatten's own round-trip test picks a
// representative structure whose only numeric-looking keys came from a
// real list.
func Unflatten(m map[string]any) map[string]any {
	tree := map[string]any{}
	for k, v := range m {
		setPath(tree, strings.Split(k, "."), v, 0)
	}
	result, _ := listify(tree, 0).(map[string]any)
	if result == nil {
		result = map[string]any{}
	}
	return result
}

// setPath is never called with an empty segs: its only call sites are
// Unflatten's own strings.Split(k, ".") (which, for any string including
// "", always returns at least one element) and its own recursive call
// below, guarded by the len(segs) == 1 check just above it.
func setPath(node map[string]any, segs []string, v any, depth int) {
	if depth >= maxStructuredDepth {
		node[strings.Join(segs, ".")] = v
		return
	}
	seg := segs[0]
	if len(segs) == 1 {
		node[seg] = v
		return
	}
	next, ok := node[seg].(map[string]any)
	if !ok {
		next = map[string]any{}
		node[seg] = next
	}
	setPath(next, segs[1:], v, depth+1)
}

// listify walks a tree built entirely out of setPath's map[string]any
// nodes and converts any node whose keys are exactly "0".."n-1" into a
// []any of length n, recursively, bottom-up so a converted child is
// itself eligible to be recognized as a list element.
func listify(v any, depth int) any {
	m, ok := v.(map[string]any)
	if !ok || depth >= maxStructuredDepth {
		return v
	}
	converted := make(map[string]any, len(m))
	for k, val := range m {
		converted[k] = listify(val, depth+1)
	}
	if !isIndexSequence(converted) {
		return converted
	}
	list := make([]any, len(converted))
	for k, val := range converted {
		i, _ := strconv.Atoi(k) // isIndexSequence already proved this succeeds.
		list[i] = val
	}
	return list
}

// isIndexSequence reports whether m's keys are exactly the base-10
// strings "0" through "n-1" for n = len(m), with no gaps and no extra
// keys.
func isIndexSequence(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for i := 0; i < len(m); i++ {
		if _, ok := m[strconv.Itoa(i)]; !ok {
			return false
		}
	}
	return true
}

// DeepMerge recursively merges b into a: a key present as a map in both
// a and b is merged key by key (recursively), a key present as a list
// in both is appended (a's elements first, then b's), and any other key
// present in both takes b's value, overwriting a's. A key present in
// only one of a or b is carried through unchanged, so no key from
// either input is ever dropped. Neither a nor b is mutated; DeepMerge
// always returns a new map. Recursion stops descending at
// maxStructuredDepth (a and b's own values at that depth are compared
// no further and b's simply overwrites a's, the same as any other
// non-map, non-list pair).
func DeepMerge(a, b map[string]any) map[string]any {
	return deepMerge(a, b, 0)
}

func deepMerge(a, b map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, bv := range b {
		av, exists := out[k]
		if exists && depth < maxStructuredDepth {
			if avM, ok := av.(map[string]any); ok {
				if bvM, ok := bv.(map[string]any); ok {
					out[k] = deepMerge(avM, bvM, depth+1)
					continue
				}
			}
			if avL, ok := av.([]any); ok {
				if bvL, ok := bv.([]any); ok {
					merged := make([]any, 0, len(avL)+len(bvL))
					merged = append(merged, avL...)
					merged = append(merged, bvL...)
					out[k] = merged
					continue
				}
			}
		}
		out[k] = bv
	}
	return out
}

// ShallowMerge merges b into a at the top level only: a key present in
// both is overwritten by b's value, with no recursion into a nested map
// or list; DeepMerge is the recursive counterpart. Neither a nor b is
// mutated; ShallowMerge always returns a new map.
func ShallowMerge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// CSVToList parses one CSV line into a list of fields, using
// encoding/csv for correct quote handling (an embedded comma or an
// escaped quote inside a quoted field) rather than a naive split on
// comma. Returns nil, the same sentinel every list-returning function in
// this package uses for malformed input, when line exceeds
// MaxStructuredInputBytes, does not parse as a well-formed CSV record,
// or holds more than one record (an unquoted embedded newline starts a
// second record as far as encoding/csv is concerned; CSVToList's own
// contract is one line in, one record out, so that case is refused
// rather than silently truncated to the first record).
func CSVToList(line string) []string {
	if len(line) > MaxStructuredInputBytes {
		return nil
	}
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	fields, err := r.Read()
	if err != nil {
		return nil
	}
	if _, err := r.Read(); err != io.EOF {
		return nil
	}
	return fields
}

// ListToCSV encodes a list of fields as one CSV line (no trailing
// newline), the inverse of CSVToList, quoting a field only when
// encoding/csv's own writer determines it needs to. Returns "" if the
// encoded line would exceed MaxStructuredInputBytes; that bound is
// checked against list's own field lengths before encoding, not the
// encoded output, so a pathologically large list is refused before the
// (comparatively expensive) quoting work runs at all.
func ListToCSV(list []string) string {
	total := 0
	for _, f := range list {
		total += len(f) + 1
		if total > MaxStructuredInputBytes {
			return ""
		}
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	// Both errors below are checked, per Go convention, rather than
	// ignored, but are provably unreachable through this function: a
	// strings.Builder's own Write never returns an error, and Writer's
	// only other failure mode (a FieldsPerRecord mismatch) cannot
	// trigger from a single Write call with FieldsPerRecord left at its
	// zero value (inferred from, and so always matching, that one call).
	if err := w.Write(list); err != nil {
		return ""
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// Pluck extracts one key's value from each map in list, in order,
// skipping (not padding with a placeholder for) a map that does not
// have the key: the result is only as long as the number of maps that
// had it.
func Pluck(list []map[string]any, key string) []any {
	out := make([]any, 0, len(list))
	for _, m := range list {
		if v, ok := m[key]; ok {
			out = append(out, v)
		}
	}
	return out
}

// YAMLToJSON converts a YAML document to its equivalent JSON text,
// round-tripping through interface{} via go.yaml.in/yaml/v3 (which
// decodes a YAML mapping to map[string]any directly, not the
// map[interface{}]interface{} an older yaml.v2-shaped library would,
// verified directly since encoding/json.Marshal cannot encode the
// latter's non-string map keys). Returns "" if yamlText exceeds
// MaxStructuredInputBytes, does not parse as YAML, or decodes into a
// structure nested past maxStructuredDepth.
func YAMLToJSON(yamlText string) string {
	if len(yamlText) > MaxStructuredInputBytes {
		return ""
	}
	var v any
	if err := yaml.Unmarshal([]byte(yamlText), &v); err != nil {
		return ""
	}
	if !withinStructuredDepth(v, 0) {
		return ""
	}
	// Checked, not ignored, but provably unreachable: every Go type
	// yaml.Unmarshal can produce into an interface{} (nil, bool, string,
	// int, float64, time.Time, map[string]any, []any -- verified
	// directly, including the less obvious ones: a YAML !!timestamp
	// decodes to time.Time, which implements json.Marshaler itself, and
	// !!binary decodes to a plain string, not []byte) is one
	// encoding/json.Marshal already knows how to encode without error.
	out, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(out)
}

// JSONToYAML converts a JSON document to its equivalent YAML text, the
// inverse of YAMLToJSON. Returns "" under the same conditions
// YAMLToJSON does: an oversized input, one that fails to parse, or one
// nested past maxStructuredDepth.
func JSONToYAML(jsonText string) string {
	if len(jsonText) > MaxStructuredInputBytes {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		return ""
	}
	if !withinStructuredDepth(v, 0) {
		return ""
	}
	// Checked, not ignored, but provably unreachable for the same reason
	// YAMLToJSON's own json.Marshal call is: every type
	// encoding/json.Unmarshal can produce into an interface{} (nil,
	// bool, string, float64, map[string]any, []any) is one
	// go.yaml.in/yaml/v3's Marshal already knows how to encode without
	// error.
	out, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(out)
}

// withinStructuredDepth reports whether a decoded JSON/YAML value nests
// no deeper than maxStructuredDepth, shared by YAMLToJSON and
// JSONToYAML (both decode through the same map[string]any/[]any shape).
func withinStructuredDepth(v any, depth int) bool {
	if depth > maxStructuredDepth {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		for _, val := range t {
			if !withinStructuredDepth(val, depth+1) {
				return false
			}
		}
	case []any:
		for _, val := range t {
			if !withinStructuredDepth(val, depth+1) {
				return false
			}
		}
	}
	return true
}

// GenerateUUIDv4 generates a random version-4 (random) UUID, using
// github.com/google/uuid's default generator (crypto/rand-backed, never
// math/rand). Unlike every other function in this package,
// GenerateUUIDv4 takes no arguments and returns a different value on
// every call: this phase's own checklist names it as a filter anyway
// (an id-generation helper a runbook author may want inline), so it is
// documented here as this package's one deliberate exception to "pure,
// deterministic transform of its arguments" rather than left looking
// like an oversight.
func GenerateUUIDv4() string {
	return uuid.New().String()
}

// XMLToJSON converts an XML document to JSON text using one documented,
// opinionated element/attribute mapping, since XML (attributes, mixed
// content, namespaces, ordered children) has no canonical JSON shape the
// way JSON and YAML already share one:
//
//   - The document's single top-level JSON key is the root element's own
//     tag name (its XML namespace, if any, is discarded: only the local
//     name is kept); its value follows the same rules as any other
//     element below. A namespace declaration itself (xmlns="..." or
//     xmlns:ns="...") is dropped everywhere it appears, not carried
//     through as an "@xmlns" attribute: it is XML plumbing, not domain
//     data.
//   - An element with neither an attribute nor a child element decodes
//     to its trimmed text content as a JSON string (an empty element
//     decodes to "").
//   - An element with an attribute or a child element decodes to a JSON
//     object instead. Each attribute becomes an "@name" key. Trimmed,
//     non-empty text content becomes a "#text" key alongside them. Each
//     distinct child tag name becomes a key holding the single decoded
//     child if that tag occurs once, or a JSON array of decoded
//     children, in document order, if it occurs more than once. (JSON
//     object key order itself is never significant, in this convention
//     or any other; only the order of elements within a same-tag array
//     is preserved.)
//
// Returns "" if xmlText exceeds MaxStructuredInputBytes, is not
// well-formed XML, or nests past maxStructuredDepth.
func XMLToJSON(xmlText string) string {
	if len(xmlText) > MaxStructuredInputBytes {
		return ""
	}
	dec := xml.NewDecoder(strings.NewReader(xmlText))
	root, err := nextStartElement(dec)
	if err != nil {
		return ""
	}
	value, err := decodeXMLElement(dec, root, 0)
	if err != nil {
		return ""
	}
	// Checked, not ignored, but provably unreachable: buildXMLValue only
	// ever produces a string, or a map[string]any built entirely from
	// strings and (recursively) more such maps and []any lists, per
	// XMLToJSON's own doc comment's mapping -- every one of those is
	// json.Marshal-safe.
	out, err := json.Marshal(map[string]any{root.Name.Local: value})
	if err != nil {
		return ""
	}
	return string(out)
}

// nextStartElement advances dec past any prolog (an XML declaration, a
// comment, a processing instruction) up to and including the document's
// first StartElement token.
func nextStartElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start, nil
		}
	}
}

// decodeXMLElement decodes start's own attributes plus its content --
// dec is positioned immediately after start's StartElement token -- up
// to and including start's matching EndElement token, per XMLToJSON's
// own doc comment's element/attribute mapping. A child element nested
// past maxStructuredDepth aborts the whole parse with an error, matching
// YAMLToJSON/JSONToYAML's own "reject entirely" choice rather than
// truncating a document silently.
func decodeXMLElement(dec *xml.Decoder, start xml.StartElement, depth int) (any, error) {
	if depth > maxStructuredDepth {
		return nil, xmlDepthErr
	}

	attrs := map[string]any{}
	for _, a := range start.Attr {
		// A namespace declaration itself (xmlns="..." or xmlns:ns="...")
		// is XML plumbing, not domain data: encoding/xml still hands it
		// back as an ordinary Attr (Name.Local == "xmlns" for the
		// default form, Name.Space == "xmlns" for a prefixed one), so it
		// has to be filtered explicitly or it would leak into the JSON
		// output as a meaningless "@xmlns"/"@ns" key.
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		attrs["@"+a.Name.Local] = a.Value
	}

	children := map[string]any{}
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			childVal, err := decodeXMLElement(dec, t, depth+1)
			if err != nil {
				return nil, err
			}
			name := t.Name.Local
			if existing, ok := children[name]; ok {
				if list, isList := existing.([]any); isList {
					children[name] = append(list, childVal)
				} else {
					children[name] = []any{existing, childVal}
				}
			} else {
				children[name] = childVal
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			return buildXMLValue(attrs, children, strings.TrimSpace(text.String())), nil
		}
	}
}

// xmlDepthErr is decodeXMLElement's sentinel error for a document nested
// past maxStructuredDepth; its text is never shown to a caller (XMLToJSON
// collapses every parse failure to ""), so it carries no more detail
// than that.
var xmlDepthErr = errors.New("filters: XMLToJSON: nested past maxStructuredDepth")

// buildXMLValue applies XMLToJSON's own doc comment's element/attribute
// mapping to one already-decoded element's attributes, children and
// trimmed text.
func buildXMLValue(attrs, children map[string]any, text string) any {
	if len(attrs) == 0 && len(children) == 0 {
		return text
	}
	out := make(map[string]any, len(attrs)+len(children)+1)
	for k, v := range attrs {
		out[k] = v
	}
	for k, v := range children {
		out[k] = v
	}
	if text != "" {
		out["#text"] = text
	}
	return out
}

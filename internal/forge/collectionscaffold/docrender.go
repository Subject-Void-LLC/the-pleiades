package collectionscaffold

import (
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// renderDoc returns the Go source for a generated Manifest's Doc field,
// including the trailing "Doc:" key and comma, or the empty string when
// doc carries nothing worth emitting.
//
// It exists because a Doc is the one part of a Manifest that cannot be
// described by flags. Everything else `forge new-collection` emits is a
// name, a bool or a short list; a real Doc is a page of prose with
// nested Param, ReturnField and Example records, and the generator used
// to emit only Summary. That left a gap with teeth: internal/forge/
// catalogdata carries the full Doc, internal/archtest's
// TestCatalogDataDocsMatchTheRegistry demands the registered manifest
// match it exactly, and the generator could not produce a file that
// passed. Every scaffolded method failed that guard until a human
// retyped the documentation into the generated source by hand, which is
// both the slowest possible way to copy a struct literal and the one
// most likely to introduce a difference nobody notices.
//
// The output is deliberately unindented and unaligned: it is spliced
// into a template whose result goes through go/format, which owns
// layout. Trying to pre-indent here would mean guessing the nesting
// depth of the call site.
//
// Zero-valued fields are omitted rather than emitted empty, matching how
// catalogdata's own literals are written and how Doc's JSON tags already
// treat them. One consequence is worth stating plainly: an empty
// non-nil slice and an absent one are the same thing by the time they
// reach here, so a catalogdata entry written with Params: []Param{}
// would generate Params: nil and fail the equality guard. Every entry
// today uses nil, which is the shape to keep using.
func renderDoc(doc collection.Doc) string {
	var b strings.Builder
	b.WriteString("Doc: collection.Doc{\n")
	before := b.Len()

	writeStringField(&b, "Summary", doc.Summary)
	writeStringField(&b, "Description", doc.Description)
	writeStringField(&b, "SinceVersion", doc.SinceVersion)
	writeStringField(&b, "Deprecated", doc.Deprecated)

	if len(doc.Params) > 0 {
		b.WriteString("Params: []collection.Param{\n")
		for _, p := range doc.Params {
			b.WriteString("{")
			var fields []string
			fields = appendStringField(fields, "Name", p.Name)
			fields = appendStringField(fields, "Type", p.Type)
			if p.Required {
				fields = append(fields, "Required: true")
			}
			fields = appendStringField(fields, "Default", p.Default)
			fields = appendStringSliceField(fields, "Choices", p.Choices)
			fields = appendStringField(fields, "Description", p.Description)
			b.WriteString(strings.Join(fields, ", "))
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
	}

	writeStringSliceField(&b, "Fragments", doc.Fragments)

	if len(doc.Returns) > 0 {
		b.WriteString("Returns: []collection.ReturnField{\n")
		for _, r := range doc.Returns {
			b.WriteString("{")
			var fields []string
			fields = appendStringField(fields, "Name", r.Name)
			fields = appendStringField(fields, "Type", r.Type)
			fields = appendStringField(fields, "Returned", r.Returned)
			fields = appendStringField(fields, "Sample", r.Sample)
			fields = appendStringField(fields, "Description", r.Description)
			b.WriteString(strings.Join(fields, ", "))
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
	}

	if len(doc.Examples) > 0 {
		b.WriteString("Examples: []collection.Example{\n")
		for _, e := range doc.Examples {
			b.WriteString("{")
			var fields []string
			fields = appendStringField(fields, "Name", e.Name)
			fields = appendStringField(fields, "RunbookYAML", e.RunbookYAML)
			b.WriteString(strings.Join(fields, ", "))
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
	}

	writeStringSliceField(&b, "SeeAlso", doc.SeeAlso)

	if b.Len() == before {
		return ""
	}
	b.WriteString("},\n")
	return b.String()
}

// writeStringField writes one `Key: "value",` line, or nothing when the
// value is empty.
func writeStringField(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(strconv.Quote(value))
	b.WriteString(",\n")
}

// writeStringSliceField writes one `Key: []string{...},` line, or
// nothing when the slice is empty.
func writeStringSliceField(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(renderStringSlice(values))
	b.WriteString(",\n")
}

// appendStringField appends a `Key: "value"` fragment for a record
// rendered on a single line, skipping an empty value.
func appendStringField(fields []string, key, value string) []string {
	if value == "" {
		return fields
	}
	return append(fields, key+": "+strconv.Quote(value))
}

// appendStringSliceField appends a `Key: []string{...}` fragment for a
// record rendered on a single line, skipping an empty slice.
func appendStringSliceField(fields []string, key string, values []string) []string {
	if len(values) == 0 {
		return fields
	}
	return append(fields, key+": "+renderStringSlice(values))
}

// renderStringSlice renders a []string composite literal.
func renderStringSlice(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

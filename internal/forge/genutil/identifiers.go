// Package genutil provides the identifier and path-segment safety checks
// shared by the Forge's code generators (internal/inventory/devicescaffold,
// internal/forge/collectionscaffold). Both generators turn user-supplied
// strings (a vendor name, a device type key, a namespaced collection name)
// into Go package names, exported identifiers, and filesystem directory
// components, so one shared, narrow validator closes the path-traversal and
// invalid-identifier risk for both rather than each reimplementing it
// slightly differently (Phase 33's own Fuzz/Stress checklist item names
// this risk directly: "a vendor name containing .. or a Go keyword must not
// produce an unsafe path or unbuildable source").
package genutil

import (
	"fmt"
	"go/token"
	"regexp"
	"strings"
)

// segmentPattern is the legal shape of one generated path/identifier
// segment: a lowercase letter, then any number of lowercase letters,
// digits, or underscores. This is deliberately stricter than
// internal/classification's own ^[a-z0-9_]+$ segment pattern: that
// package only ever uses a validated segment as a map key, where a
// leading digit is perfectly legal. Here a segment becomes a Go package,
// type, or function name, where a leading digit is a syntax error, so the
// pattern requires a leading letter.
var segmentPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxSegments bounds how many segments a generated name (a device vendor
// path, a device type key, or a collection's namespaced name) may have.
const maxSegments = 16

// maxSegmentLength bounds each individual segment. The number matches
// internal/classification's maxPathSegments purely for a familiar bound;
// it guards a different axis (segment length, not segment count) because,
// unlike classification's in-memory lookup key, a segment here is also
// joined into a real filesystem path.
const maxSegmentLength = 64

// ValidateSegment reports whether s is safe to use, simultaneously, as a Go
// package name, the raw material for an exported Go identifier, and a
// single filesystem path component. It fails closed: an empty string, an
// over-length string, any character outside [a-z0-9_], a leading digit, or
// a Go reserved keyword is rejected. Because segmentPattern permits no `.`,
// `/`, `\`, or `..`, an accepted segment can never escape its intended
// parent directory once joined into a path with filepath.Join.
func ValidateSegment(s string) error {
	if s == "" {
		return fmt.Errorf("genutil: segment is empty")
	}
	if len(s) > maxSegmentLength {
		return fmt.Errorf("genutil: segment %q exceeds the maximum length of %d", s, maxSegmentLength)
	}
	if !segmentPattern.MatchString(s) {
		return fmt.Errorf("genutil: invalid segment %q: must match %s", s, segmentPattern.String())
	}
	if token.IsKeyword(s) {
		return fmt.Errorf("genutil: segment %q is a Go reserved keyword", s)
	}
	return nil
}

// ValidateIdentSegment reports whether s is safe to use as the raw material
// for an exported Go identifier and as a single filesystem path component,
// but not necessarily as a Go package name. It applies every check
// ValidateSegment does except the reserved-keyword rejection.
//
// The distinction is narrow and real. A segment that becomes a package name
// must not be a keyword, because `package switch` does not compile. A
// segment that only ever becomes an exported identifier may be one, because
// ToExportedIdent capitalizes it first and `type Switch struct{}` is
// perfectly ordinary Go. Rejecting the second case too costs real names:
// a network switch is the obvious example, and "range", "map", "type",
// "import", and "return" are all plausible words in a device or method
// name.
//
// Path safety is unaffected. That comes entirely from segmentPattern, which
// permits no `.`, `/`, `\`, or `..`, so an accepted segment still cannot
// escape its parent directory once joined with filepath.Join. This function
// relaxes a Go-syntax check, never a path check.
func ValidateIdentSegment(s string) error {
	if s == "" {
		return fmt.Errorf("genutil: segment is empty")
	}
	if len(s) > maxSegmentLength {
		return fmt.Errorf("genutil: segment %q exceeds the maximum length of %d", s, maxSegmentLength)
	}
	if !segmentPattern.MatchString(s) {
		return fmt.Errorf("genutil: invalid segment %q: must match %s", s, segmentPattern.String())
	}
	return nil
}

// ValidateSegments checks that segments has between one and maxSegments
// elements and that every element passes ValidateSegment.
func ValidateSegments(segments []string) error {
	if len(segments) == 0 {
		return fmt.Errorf("genutil: no segments given")
	}
	if len(segments) > maxSegments {
		return fmt.Errorf("genutil: %d segments exceeds the maximum of %d", len(segments), maxSegments)
	}
	for _, s := range segments {
		if err := ValidateSegment(s); err != nil {
			return err
		}
	}
	return nil
}

// ToExportedIdent converts a snake_case segment into an exported Go
// identifier in PascalCase (for example "daemon_reload" becomes
// "DaemonReload"). Callers must pass a string that already satisfies
// ValidateSegment: this function does no validation of its own and simply
// title-cases each underscore-delimited part.
func ToExportedIdent(snake string) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

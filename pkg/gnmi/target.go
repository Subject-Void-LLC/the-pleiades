// Package gnmi: a request prefix's target, the name of the data tree a
// request addresses (gNMI specification section 2.2.2.1, Path.target). A
// server serving more than one tree may refuse a request naming none, as
// SONiC's does ("Empty target data not supported"). The target travels in
// the prefix's own field, never as an element or an origin, and a target a
// device echoes in an answer is not read back into a path.
package gnmi

import (
	"fmt"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// maxPrefixTargetBytes bounds a request prefix's target.
const maxPrefixTargetBytes = 128

// NoPrefixTarget is a request's PrefixTarget that sends no target even when
// the client names one (Options.PrefixTarget), as an undo of a write made
// with none must. It is not a target any device can name: a target starts
// with a letter or a digit.
const NoPrefixTarget = "-"

// ValidatePrefixTarget checks target, such as SONiC's OC-YANG or CONFIG_DB:
// empty for none, else a plain identifier of letters, digits, "_", "." and
// "-", starting with a letter or a digit, at most 128 bytes. It is never a
// path: nothing in it can become an element.
func ValidatePrefixTarget(target string) error {
	if target == "" {
		return nil
	}
	if len(target) > maxPrefixTargetBytes || !originPattern.MatchString(target) {
		return fmt.Errorf("the gNMI target %s is not a plain identifier (letters, digits, _, . and -, at most %d bytes)", quoted(target), maxPrefixTargetBytes)
	}
	return nil
}

// prefixProto is a request's prefix on the wire: prefix's origin and
// elements, and target in Path.target, the one field of a path the gNMI
// specification reads only in a prefix; nil when it names none of the
// three. prefix and target must already be valid.
func prefixProto(prefix Path, target string) *gpb.Path {
	if prefix.Origin == "" && len(prefix.Elems) == 0 && target == "" {
		return nil
	}
	out := prefix.proto()
	out.Target = target
	return out
}

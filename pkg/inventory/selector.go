package inventory

// Selector expresses which devices a Repository.GetGroup stream should
// include. Its zero value selects every device, the same behavior every
// caller gets today.
//
// This is a narrow, real Specification-shaped value object, not yet the
// composable AND/OR/NOT predicate tree PLAN.md Section 22.3's future
// Virtual Groups will eventually need ("tags contains 'core' AND
// facts.os_version < '17.3'"). CODE_SCAFFOLD.md's aspirational storage
// sketch warns that a Selector must not be "just a group name string,
// which cannot express Section 3 overlapping groups": that warning is
// about the eventual full form, not this one. A device can still belong
// to any number of overlapping groups (the underlying Group edge is
// many-to-many); what Selector does not yet do is let a caller express a
// boolean combination of more than one condition. Widening it to a real
// predicate tree is future work with no caller yet, not a gap this type
// pretends does not exist.
type Selector struct {
	// GroupName restricts the stream to devices that belong to the named
	// Group. Empty means no restriction: every device is selected, which
	// is the behavior every existing caller already gets.
	GroupName string
}

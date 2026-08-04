// Package classification implements PLAN.md Section 6d's hierarchical
// classification rule tree: a device's raw discovered facts (represented
// here as a path, e.g. ["linux_server", "debian_family", "ubuntu"], root
// first) are classified against a RuleSet whose rules inherit down the
// tree, most specific wins on conflicts. This is the Section 25 shared
// hierarchical policy resolver's (pkg/policy) first real consumer.
//
// Deliberately out of scope for this package, per a settled design
// decision recorded in this project's own memory system: a data-driven
// Capabilities field, sourced by walking this same rule tree, belongs to
// Phase 32 once record.Record grows the corresponding field. Growing this
// package's Rule to carry capabilities today, with no caller anywhere to
// consume them, would be exactly the "port with no callers is a
// decoration, not an implemented pattern" failure this project's own
// Adversarial Pattern Justification standard rejects. Rule below carries
// only the fields a real caller (internal/inventory's HostSpec resolution)
// actually consumes: Type and ConnectionMode, plus Onboard for the Section
// 6b onboarding action name, which is exercised by this package's own
// tests even though no onboarding pipeline consumes it yet (Section 6b is
// unbuilt).
//
// Also deliberately out of scope: loading a RuleSet from a real on-disk
// directory of _rule.yaml files (Section 6d's own classification_rules/
// layout). DefaultRuleSet below is the only RuleSet this phase ships,
// baked in for the Walk tier's "built-in classification rules for common
// OS families" promise (PLAN.md Section 7). A filesystem loader with no
// caller (no CLI flag or config surface references a custom rule
// directory yet) would be the same decoration failure mode; building one
// is deferred to whichever phase first needs to consume it.
package classification

import (
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/policy"
)

// segmentPattern is the legal shape of one classification path segment,
// matching AGENTS.md's own YAML convention (lowercase keys with
// underscores). Rejecting anything else, in particular a literal ".", is
// what keeps a dotted-string rule key collision-free: Classify(["a.b"])
// and Classify(["a", "b"]) would otherwise both produce the lookup key
// "a.b" and silently resolve identically despite meaning different things.
var segmentPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// maxPathSegments bounds how deep a classification path may go. PLAN.md
// Section 6d's own worked tree is 3-4 levels deep; 64 is generous headroom
// over any realistic classification hierarchy while still bounding the
// cost of Classify against a path sourced from user-editable input
// (HostSpec.Classify, `add-host --classify`). An adversarial review of
// this phase found that an earlier version of Classify was O(n^2) in path
// length with no such bound, making a several-hundred-thousand-segment
// path (a few hundred KB of YAML or a single CLI flag value, both well
// within ordinary size limits) a real, cheap resource-exhaustion vector;
// this cap closes it independently of the O(n) fix below, in case a
// future change to Classify's algorithm ever reintroduces
// worse-than-linear scaling.
const maxPathSegments = 64

// Rule is one level's classification data. Every field is a pointer so a
// level can leave a field unset (nil) and let a less specific level's
// value continue inheriting, rather than every level being forced to
// restate every field. See combineRule for the merge semantics.
type Rule struct {
	// Type is the ItemFactory registry key this rule assigns (e.g.
	// "linux_server", "cisco_router").
	Type *string
	// ConnectionMode is Section 6e's agent/agentless distinction.
	ConnectionMode *string
	// Onboard names the Section 6b provisioning action for a device
	// matching this rule (e.g. "configure_polling", "install_agent").
	Onboard *string
}

// combineRule is Rule's Section 25 combine function: a more specific
// layer's non-nil fields replace the accumulated value; a nil field
// leaves whatever a less specific layer already set untouched. This is
// Override semantics applied per field, the shape pkg/policy's own doc
// comment names as the reason Resolve takes a caller-supplied combine
// rather than interpreting Mode itself.
func combineRule(acc, next Rule) Rule {
	if next.Type != nil {
		acc.Type = next.Type
	}
	if next.ConnectionMode != nil {
		acc.ConnectionMode = next.ConnectionMode
	}
	if next.Onboard != nil {
		acc.Onboard = next.Onboard
	}
	return acc
}

// RuleSet is an immutable collection of Rules keyed by classification
// path. The zero value is not usable; construct one with NewRuleSet or
// DefaultRuleSet.
type RuleSet struct {
	rules map[string]Rule
}

// NewRuleSet builds a RuleSet from rules, keyed by dotted path (e.g.
// "linux_server.debian_family"). It copies rules so later mutation of the
// caller's map never reaches back into the returned RuleSet, and validates
// every key's segments up front so a malformed built-in rule set fails at
// construction time rather than silently never matching later.
func NewRuleSet(rules map[string]Rule) (*RuleSet, error) {
	for key := range rules {
		if err := validateSegments(strings.Split(key, ".")); err != nil {
			return nil, fmt.Errorf("classification: rule set key %q: %w", key, err)
		}
	}
	return &RuleSet{rules: maps.Clone(rules)}, nil
}

// validateSegments checks that path has between one and maxPathSegments
// elements and that every element matches segmentPattern.
func validateSegments(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("path must have at least one segment")
	}
	if len(path) > maxPathSegments {
		return fmt.Errorf("path has %d segments, exceeding the maximum of %d", len(path), maxPathSegments)
	}
	for _, s := range path {
		if !segmentPattern.MatchString(s) {
			return fmt.Errorf("invalid path segment %q: must match %s", s, segmentPattern.String())
		}
	}
	return nil
}

// pathKey joins a validated path into the dotted string RuleSet is keyed
// on. Because every segment is validated against segmentPattern (which
// forbids "."), this join is unambiguous: no two distinct paths can ever
// produce the same key.
func pathKey(path []string) string {
	return strings.Join(path, ".")
}

// Classify resolves path (root first, e.g. ["linux_server",
// "debian_family", "ubuntu"]) against rs, folding every prefix of path
// that has a registered rule through pkg/policy.Resolve in root-to-leaf
// order, most specific last. A prefix with no registered rule is skipped,
// not an error: PLAN.md Section 6d's own worked tree shows not every node
// needs a _rule.yaml. Classify errors if path is malformed (an empty,
// invalid, or excessively long segment list, see maxPathSegments) or if
// zero prefixes matched anywhere (Section 6g's quarantine trigger; this
// package surfaces it as a plain error rather than a lifecycle-state
// transition, since the onboarding pipeline that owns that transition,
// Section 6b, is not built yet).
//
// The key for each prefix is built incrementally with a strings.Builder,
// one segment appended per iteration, rather than by calling pathKey
// (strings.Join) fresh on each growing prefix. An adversarial review of
// this phase caught the earlier, simpler version's real cost: rejoining
// the whole prefix from scratch on every iteration made this loop O(n^2)
// in len(path), measured at ~40s for a 100,000-segment path. Builder's
// String method returns its accumulated bytes without copying, and Go
// strings are immutable, so each intermediate prefix string captured into
// a Layer.Name below stays valid and correct even as later iterations
// keep appending to the same Builder; the loop is O(n) total.
func (rs *RuleSet) Classify(path []string) (policy.Result[Rule], error) {
	if err := validateSegments(path); err != nil {
		return policy.Result[Rule]{}, fmt.Errorf("classification: %w", err)
	}

	var layers []policy.Layer[Rule]
	var key strings.Builder
	for i, segment := range path {
		if i > 0 {
			key.WriteByte('.')
		}
		key.WriteString(segment)
		prefix := key.String()
		if rule, ok := rs.rules[prefix]; ok {
			layers = append(layers, policy.Layer[Rule]{Name: prefix, Value: rule})
		}
	}

	if len(layers) == 0 {
		return policy.Result[Rule]{}, fmt.Errorf("classification: no rule matched any level of %q", pathKey(path))
	}

	return policy.Resolve(policy.ModeOverride, Rule{}, layers, combineRule), nil
}

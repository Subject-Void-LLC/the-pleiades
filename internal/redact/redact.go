// Package redact provides the one secret-masking ruleset this platform has,
// and the one engine that applies it.
//
// PLAN.md Section 25 lists the "Secret masking ruleset (data, applied in Go
// and Python)" among the Build Once Contracts, with four call sites: the
// native adapter, legacy stdout, credential handling, and the Ansible
// callback bridge. It is owed by Phase 22, and Section 25 states the cost of
// deferring it plainly: the ruleset would have to be retrofitted across a
// process and a language boundary, and anything already leaked is durable.
// That irreversibility is why this exists before the thing it protects.
//
// # Three channels, not one
//
// A secret reaches a log through three different shapes, and no single
// mechanism catches all three:
//
//   - By VALUE. The process knows the exact bytes, because it just injected
//     them into a job. Handled by Literals plus the substring scrub in
//     literal.go. This is the strongest channel and the only one that can
//     catch a secret a remote command echoed back at us.
//   - By KEY. A structured log attribute is named "password", so its value
//     is a secret whether or not this process knows what it is. Handled by
//     key rules, and applicable only where the attribute name is still
//     visible. See the ordering constraint below.
//   - By SHAPE. A PEM block or a JWT is secret by construction, from any
//     source, known or not. Handled by pattern rules.
//
// # The ordering constraint on the logging seam
//
// The key channel is the reason this package exposes Attr rather than a
// wrapping slog.Handler, and the reason that choice is recorded rather than
// left to preference. A wrapping handler cannot do this job:
//
//   - It never sees attributes added through Logger.With. The standard
//     library's commonHandler.withAttrs pre-formats those into a byte buffer
//     at WithAttrs time, so by the time a wrapper's Handle runs they are no
//     longer attributes it can inspect.
//   - It sees Record.Message only as an opaque string, and the message is
//     the sole channel for bridged log.Print output.
//
// ReplaceAttr has neither problem. The standard library calls it from
// appendAttr after Value.Resolve, so no LogValuer can route around it, it
// recurses into group leaves, and its documented contract passes the
// built-in time, level, source and msg attributes through it.
//
// The corollary matters as much as the rule: every terminal writer must
// carry the same ruleset, including any os.Stderr fallback, or the failure
// path emits unmasked exactly when things are going wrong. Writer exists
// for that, and internal/archtest enforces both halves.
//
// # What this cannot do
//
// A Go panic traceback is written to file descriptor 2 by the runtime
// itself. No io.Writer decorator and no slog handler can intercept it. That
// is a real residual rather than an oversight, and it belongs in the
// production documentation rather than being quietly omitted here.
package redact

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Marker is what a redacted value reads as.
//
// It is the same string internal/launch already used for a redacted saved
// launch answer, moved here so there is one definition rather than two
// spellings of the same idea in two packages. AWX uses this exact token for
// the same purpose, so an API client written against AWX recognizes it.
const Marker = "$encrypted$"

// MinLiteralLength is the shortest value that may be added to a Literals
// set.
//
// A substring scrub has no minimum of its own: it replaces whatever it is
// given, wherever it appears. A short or common value (a bool stringified
// to "true", a one-digit exit code) added to the set would scrub that
// substring out of every later message for the rest of the process,
// corrupting unrelated output, which is worse than not masking at all.
//
// Eight is a policy choice rather than a derived number. It matches the
// placeholder's own width, since a value shorter than what would replace it
// hides nothing meaningful, and it matches a conventional minimum password
// length. A genuinely short real secret, a four-digit PIN say, cannot be
// safely substring-masked by this mechanism at all, ever, regardless of
// this guard. That limitation is inherent to substring masking rather than
// introduced by the check.
const MinLiteralLength = 8

// RuleKind is how one rule decides what to mask.
type RuleKind string

const (
	// KindKey masks the VALUE of an attribute whose NAME matches. It is
	// applicable only where the name is still visible, which is inside
	// slog.HandlerOptions.ReplaceAttr and nowhere else.
	KindKey RuleKind = "key"

	// KindPattern masks text matching a shape that is secret by
	// construction, such as a PEM private key block, from any source.
	KindPattern RuleKind = "pattern"
)

// Rule is one entry in the ruleset.
type Rule struct {
	// Name identifies the rule in an error and in the Python half. It is
	// unique across the ruleset.
	Name string `json:"name"`

	// Kind decides which of the two fields below is meaningful.
	Kind RuleKind `json:"kind"`

	// Keys are the attribute names a KindKey rule matches, compared
	// without regard to case.
	Keys []string `json:"keys,omitempty"`

	// Pattern is the RE2 source a KindPattern rule matches.
	Pattern string `json:"pattern,omitempty"`

	// Prefilter is a set of lowercase substrings, at least one of which
	// every match of Pattern must contain. Text lowercases the input once
	// and skips any rule whose prefilter it fails, which is what keeps
	// seven regular expressions off the critical path of every log line
	// that contains no secret at all, and that is almost all of them.
	//
	// A wrong prefilter silently disables its own rule, which is the worst
	// failure this file has available. Samples is what stops that.
	Prefilter []string `json:"prefilter,omitempty"`

	// Samples are strings this rule must mask. They are data rather than
	// test code so the rule and its evidence travel together, and so the
	// Python half can assert the same thing against the same examples.
	Samples []string `json:"samples,omitempty"`

	// Description says why the rule exists. It is required, for the same
	// reason gosec-waivers.json requires an individually written reason
	// per finding: a rule nobody can justify is a rule nobody can safely
	// remove later.
	Description string `json:"description"`
}

// Ruleset is the whole ruleset.
type Ruleset struct {
	// Comment is the human-readable preamble in rules.json. It is decoded
	// rather than ignored so that a round trip through this type does not
	// silently drop it.
	Comment []string `json:"_comment,omitempty"`

	// Rules are the rules, in file order.
	Rules []Rule `json:"rules"`
}

// rulesJSON is the one copy of the ruleset. The same file is copied into
// the legacy Ansible runner image for the Phase 25 callback plugin, so a
// second copy anywhere in this repository is a defect. TestRulesetHasExactlyOneCopy
// enforces that.
//
//go:embed rules.json
var rulesJSON []byte

// defaultOnce parses the embedded ruleset exactly once per process.
var defaultOnce = sync.OnceValues(func() (Ruleset, error) {
	return Parse(rulesJSON)
})

// DefaultRuleset returns the embedded ruleset.
//
// It panics if the embedded file cannot be parsed or validated. That is
// deliberate and follows pkg/registry's own panic-at-init convention: the
// file is compiled into the binary, so a failure here is a build defect
// that every process would hit identically at startup, not a runtime
// condition a caller could handle. TestDefaultRulesetParses catches it in
// CI, long before a binary ships.
func DefaultRuleset() Ruleset {
	rs, err := defaultOnce()
	if err != nil {
		panic("redact: the embedded ruleset is invalid: " + err.Error())
	}
	return rs
}

// DefaultJSON returns the raw bytes of the embedded ruleset.
//
// This exists for the tooling that copies the ruleset across the language
// boundary, so that side reads the same bytes Go compiled in rather than
// re-reading a path that could have drifted.
func DefaultJSON() []byte {
	out := make([]byte, len(rulesJSON))
	copy(out, rulesJSON)
	return out
}

// Parse decodes and validates a ruleset.
func Parse(data []byte) (Ruleset, error) {
	var rs Ruleset

	dec := json.NewDecoder(bytes.NewReader(data))
	// An unknown field is a typo in a security control, which is exactly
	// the place a silently ignored key does the most damage: a rule
	// written as "key" instead of "keys" would parse clean and mask
	// nothing.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rs); err != nil {
		return Ruleset{}, fmt.Errorf("redact: cannot parse the ruleset: %w", err)
	}

	if err := rs.Validate(); err != nil {
		return Ruleset{}, err
	}
	return rs, nil
}

// Validate reports whether the ruleset is well formed.
func (rs Ruleset) Validate() error {
	if len(rs.Rules) == 0 {
		return fmt.Errorf("redact: the ruleset has no rules")
	}

	seen := make(map[string]struct{}, len(rs.Rules))
	for i, r := range rs.Rules {
		if r.Name == "" {
			return fmt.Errorf("redact: rule %d has no name", i)
		}
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("redact: two rules are both named %q", r.Name)
		}
		seen[r.Name] = struct{}{}

		if r.Description == "" {
			return fmt.Errorf("redact: rule %q has no description, and a rule nobody can justify is a rule nobody can safely remove", r.Name)
		}

		switch r.Kind {
		case KindKey:
			if len(r.Keys) == 0 {
				return fmt.Errorf("redact: key rule %q names no keys", r.Name)
			}
			if r.Pattern != "" {
				return fmt.Errorf("redact: key rule %q also carries a pattern, and only one of the two can apply", r.Name)
			}
			if len(r.Prefilter) > 0 || len(r.Samples) > 0 {
				return fmt.Errorf("redact: key rule %q carries a prefilter or samples, which only apply to a pattern rule", r.Name)
			}
			for _, k := range r.Keys {
				if k == "" {
					return fmt.Errorf("redact: key rule %q has an empty key, which would match every attribute", r.Name)
				}
			}

		case KindPattern:
			if r.Pattern == "" {
				return fmt.Errorf("redact: pattern rule %q has no pattern", r.Name)
			}
			if len(r.Keys) > 0 {
				return fmt.Errorf("redact: pattern rule %q also carries keys, and only one of the two can apply", r.Name)
			}
			if len(r.Samples) == 0 {
				return fmt.Errorf("redact: pattern rule %q has no samples, so nothing proves it still matches what it claims to", r.Name)
			}
			for _, pf := range r.Prefilter {
				if pf == "" {
					return fmt.Errorf("redact: pattern rule %q has an empty prefilter entry, which would make the prefilter a no-op", r.Name)
				}
				if pf != strings.ToLower(pf) {
					return fmt.Errorf("redact: pattern rule %q has prefilter %q, which is not lowercase, so it can never match the lowercased input", r.Name, pf)
				}
			}

		default:
			return fmt.Errorf("redact: rule %q has kind %q, which is not %q or %q", r.Name, r.Kind, KindKey, KindPattern)
		}
	}

	return nil
}

package redact

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Masker applies one Ruleset. It is the only masking engine in this
// codebase, and it is safe for concurrent use.
//
// Construct it once per process in the composition root and hand the same
// instance to everything that logs, so every terminal writer carries the
// same rules. internal/archtest fails the build if any cmd/ binary builds a
// slog handler without one.
type Masker struct {
	// keys are the KindKey names, lowercased once at construction so the
	// per-attribute comparison is a map lookup rather than a scan.
	keys map[string]struct{}

	// patterns are the compiled KindPattern rules, in ruleset order, each
	// with the cheap substring test that decides whether to run it.
	patterns []compiledPattern

	// literals is the process-wide by-value channel.
	literals *Literals
}

// NewMasker compiles a ruleset into an engine.
//
// It returns an error rather than panicking because a Ruleset can come from
// somewhere other than the embedded file, and a composition root should
// refuse to start on a bad one rather than crash mid-flight.
func NewMasker(rs Ruleset) (*Masker, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}

	m := &Masker{
		keys:     make(map[string]struct{}),
		literals: newLiterals(),
	}

	for _, r := range rs.Rules {
		switch r.Kind {
		case KindKey:
			for _, k := range r.Keys {
				m.keys[strings.ToLower(k)] = struct{}{}
			}
		case KindPattern:
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("redact: rule %q has an invalid pattern: %w", r.Name, err)
			}
			m.patterns = append(m.patterns, compiledPattern{
				name:      r.Name,
				re:        re,
				prefilter: r.Prefilter,
			})
		}
	}

	return m, nil
}

// Literals returns the process-wide set of exact secret values.
//
// Whatever injects a secret registers it here, and every masked line
// afterwards scrubs it. Handing out the set rather than taking it at
// construction means a composition root wires one object instead of two,
// and there is no way to build a Masker whose literal channel points
// somewhere other than the one its Attr method reads.
func (m *Masker) Literals() *Literals { return m.literals }

// Text masks free text.
//
// extra is masked alongside the process-wide literal set, for the caller
// that knows a secret this line might contain and has not registered it
// globally: a per-dispatch credential whose life is one Execute call, for
// example. Passing nil is normal.
//
// Key rules do not apply here, because free text has no attribute name to
// match. Pattern rules do.
func (m *Masker) Text(extra []string, text string) string {
	if text == "" {
		return text
	}

	// Literals first. A pattern rule replaces its match with the
	// placeholder, so running patterns first could destroy the exact bytes
	// a literal was going to match and leave a partially masked value.
	literals := m.literals.snapshotShared()
	if len(extra) > 0 {
		// The cached slice is shared, so this must not append into it.
		// Combining the two sources into one list before masking is what
		// keeps the longest-first rule applying ACROSS them: masking them
		// separately would let a caller-supplied password that is a prefix
		// of a registered passphrase carve the passphrase in half and leave
		// its tail exposed.
		combined := make([]string, 0, len(literals)+len(extra))
		combined = append(combined, literals...)
		combined = append(combined, extra...)
		literals = combined
	}
	out := maskLiterals(literals, text)

	// The prefilter pass. Lowercasing once and testing cheap substrings is
	// dramatically faster than running every regular expression on every
	// line, and almost every line contains no secret shape at all. The
	// lowercase copy is built lazily, so a process with no pattern rules
	// never pays for it.
	var lowered string
	for i := range m.patterns {
		p := &m.patterns[i]
		if len(p.prefilter) > 0 {
			if lowered == "" {
				lowered = strings.ToLower(out)
			}
			if !containsAny(lowered, p.prefilter) {
				continue
			}
		}
		before := out
		out = p.re.ReplaceAllString(out, maskPlaceholder)
		if out != before {
			// A replacement changed the text, so the lowercase copy is
			// stale for every rule after this one. Rebuilding is correct
			// and rare: it only happens on a line that actually contained
			// a secret.
			lowered = ""
		}
	}
	return out
}

// compiledPattern is one pattern rule ready to run.
type compiledPattern struct {
	// name identifies the rule in a failure. It is unused on the hot path
	// and exists so a future diagnostic can say which rule fired.
	name string

	re *regexp.Regexp

	// prefilter is the rule's cheap substring test, lowercase. Empty means
	// the pattern always runs.
	prefilter []string
}

// containsAny reports whether lowered contains at least one of subs.
func containsAny(lowered string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(lowered, sub) {
			return true
		}
	}
	return false
}

// Attr is a slog.HandlerOptions.ReplaceAttr function.
//
// It is the whole reason this package exposes a function rather than a
// wrapping slog.Handler. See the package comment for why a wrapper cannot
// do this job: it cannot see attributes added through Logger.With, and it
// sees the message only as an opaque string.
//
// Three things happen here, in order:
//
//  1. A group is returned untouched. The standard library recurses into a
//     group's leaves and calls this again for each one, so masking the
//     group itself would be both wrong and redundant.
//  2. An attribute whose NAME matches a key rule has its value replaced by
//     Marker outright, whatever the value is. This is the channel that
//     catches a secret this process does not know the bytes of.
//  3. Any remaining string value, including the built-in msg, is run
//     through Text.
//
// The attribute's key is never changed. Returning an Attr with an empty
// key tells the standard library to drop the attribute entirely, which
// would silently delete log lines rather than mask them.
func (m *Masker) Attr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		return a
	}

	if _, secret := m.keys[strings.ToLower(a.Key)]; secret {
		return slog.String(a.Key, Marker)
	}

	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, m.Text(nil, a.Value.String()))
	}

	return a
}

// HandlerOptions returns slog handler options already carrying Attr.
//
// This exists so a caller cannot build a handler that holds the ruleset and
// forgot to install it. Every cmd/ binary constructs its handler from this
// rather than from a literal &slog.HandlerOptions{}, and
// internal/archtest's TestEverySlogHandlerCarriesTheMaskingRuleset fails
// the build when one does not.
func (m *Masker) HandlerOptions(level slog.Leveler) *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: m.Attr,
	}
}

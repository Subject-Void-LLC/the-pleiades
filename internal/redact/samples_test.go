package redact_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// The guard that makes the prefilter optimization safe to have.
//
// Masker.Text skips a pattern rule entirely when the line contains none of
// that rule's prefilter substrings. That is what keeps masking off the
// critical path of every log line, and it is also the most dangerous thing
// in this package: a prefilter that does not actually hold for every match
// of its pattern silently disables its own rule. Nothing would fail. The
// rule would simply stop firing, and the first evidence would be a secret
// in a log.
//
// So each pattern rule carries samples in rules.json, and these tests hold
// the prefilter and the pattern against each other using them. The samples
// are data rather than test code so the rule and its evidence live in the
// same file and travel together to the Python half.

// TestEveryPatternSamplePassesItsOwnPrefilter is the direct guard. If a
// sample fails its rule's prefilter, Masker.Text would skip the rule for
// that input and never run the regular expression at all.
func TestEveryPatternSamplePassesItsOwnPrefilter(t *testing.T) {
	t.Parallel()

	for _, rule := range redact.DefaultRuleset().Rules {
		if rule.Kind != redact.KindPattern || len(rule.Prefilter) == 0 {
			continue
		}

		t.Run(rule.Name, func(t *testing.T) {
			t.Parallel()

			for _, sample := range rule.Samples {
				lowered := strings.ToLower(sample)

				var passed bool
				for _, pf := range rule.Prefilter {
					if strings.Contains(lowered, pf) {
						passed = true
						break
					}
				}
				if !passed {
					t.Errorf(
						"sample %q passes none of rule %q's prefilters %v, so Masker.Text would skip this rule "+
							"and the sample would never be masked",
						sample, rule.Name, rule.Prefilter)
				}
			}
		})
	}
}

// TestEveryPatternSampleMatchesItsOwnPattern is the other half. A sample
// that its own regular expression does not match is a sample that proves
// nothing about the prefilter, so this stops the guard above from passing
// vacuously.
func TestEveryPatternSampleMatchesItsOwnPattern(t *testing.T) {
	t.Parallel()

	for _, rule := range redact.DefaultRuleset().Rules {
		if rule.Kind != redact.KindPattern {
			continue
		}

		t.Run(rule.Name, func(t *testing.T) {
			t.Parallel()

			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				t.Fatalf("rule %q has an uncompilable pattern: %v", rule.Name, err)
			}
			for _, sample := range rule.Samples {
				if !re.MatchString(sample) {
					t.Errorf("rule %q does not match its own sample %q", rule.Name, sample)
				}
			}
		})
	}
}

// TestEveryPatternSampleIsActuallyMasked is the end-to-end statement, and
// the one that would catch a failure the two tests above could both miss:
// a prefilter that holds, a pattern that matches, and some third thing in
// Masker.Text (ordering, a stale lowercase cache, an early return) that
// stops the rule from firing anyway.
//
// This runs each sample through the real path a log line takes.
func TestEveryPatternSampleIsActuallyMasked(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	for _, rule := range redact.DefaultRuleset().Rules {
		if rule.Kind != redact.KindPattern {
			continue
		}

		t.Run(rule.Name, func(t *testing.T) {
			t.Parallel()

			for _, sample := range rule.Samples {
				// Embedded in a realistic line rather than masked alone, so
				// a rule that only works on a bare value fails here.
				line := "dispatch failed: " + sample + " (device router-1)"

				got := m.Text(nil, line)
				if got == line {
					t.Errorf("rule %q did not fire on its own sample: %q", rule.Name, line)
					continue
				}
				if !strings.Contains(got, "router-1") {
					t.Errorf("rule %q destroyed surrounding context: %q", rule.Name, got)
				}
			}
		})
	}
}

// TestMultipleSecretShapesOnOneLineAreAllMasked covers the interaction the
// prefilter optimization introduced.
//
// Masker.Text rebuilds its lowercase copy whenever a rule changes the text,
// because every later rule's prefilter would otherwise be testing a stale
// string. A line carrying several different secret shapes is what exercises
// that, and getting it wrong would mask the first shape and silently skip
// the rest.
func TestMultipleSecretShapesOnOneLineAreAllMasked(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	line := strings.Join([]string{
		"dsn=postgres://admin:hunter2@db.internal/x",
		"auth=Bearer abc123DEF456ghi789",
		"jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.dBjftJeZ4CVP",
		"aws=AKIAIOSFODNN7EXAMPLE",
		"device=router-1",
	}, " ")

	got := m.Text(nil, line)

	for _, mustGo := range []string{
		"admin:hunter2",
		"abc123DEF456ghi789",
		"eyJhbGciOiJIUzI1NiJ9",
		"AKIAIOSFODNN7EXAMPLE",
	} {
		if strings.Contains(got, mustGo) {
			t.Errorf("output still contains %q, so a rule after the first one was skipped: %q", mustGo, got)
		}
	}
	if !strings.Contains(got, "router-1") {
		t.Errorf("masking destroyed the surrounding context: %q", got)
	}
}

// TestPrefilterDoesNotSuppressAMatchInMixedCase covers the case-folding
// half of the prefilter contract. The prefilters are lowercase and the
// input is lowercased before testing, so a rule whose pattern is
// case-sensitive must still be reachable through a mixed-case line.
func TestPrefilterDoesNotSuppressAMatchInMixedCase(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	// "BEARER" uppercase: the prefilter is "bearer", and the pattern is
	// case-insensitive, so both halves have to agree for this to mask.
	got := m.Text(nil, "Authorization: BEARER abc123DEF456ghi789")
	if strings.Contains(got, "abc123DEF456ghi789") {
		t.Errorf("an uppercase bearer token was not masked: %q", got)
	}
}

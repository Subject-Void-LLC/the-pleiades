package rrule

import (
	"fmt"
	"strings"
	"time"
)

// exclusionOverscan is how many extra occurrences the expansion behind an
// exclusion set asks for beyond what the caller wants.
//
// Exclusions are subtractive, so producing N results may require expanding
// more than N candidates. Rather than guess a multiplier, RuleSet.Expand
// re-expands with a growing budget until it has enough or the series ends;
// this is the increment it grows by, and the loop below bounds the number
// of attempts so a rule excluding nearly everything terminates instead of
// growing forever.
const exclusionOverscan = 64

// maxExclusionAttempts bounds the re-expansion loop. Combined with
// exclusionOverscan it caps the total work one Expand call can do at a
// predictable multiple of the caller's limit, so an adversarial rule set
// (a daily rule excluded by an identical daily rule) costs bounded time
// and returns an empty result rather than hanging.
const maxExclusionAttempts = 16

// RuleSet is a recurrence together with everything subtracted from it: the
// EXRULE recurrences and EXDATE instants that carve holes in the series.
//
// It is separate from Recurrence because the two are validated at
// different times and stored in different columns. A Recurrence is one
// rule; a RuleSet is what a schedule actually means. Keeping the exclusion
// logic here rather than inside Expand keeps the common no-exclusion path
// free of it.
type RuleSet struct {
	// Rule is the base recurrence. Required.
	Rule Recurrence

	// Exclusions are EXRULE recurrences whose occurrences are removed from
	// Rule's. Each is expanded in the same location and against the same
	// DTSTART anchor as Rule, which is what makes "the same rule, excluded
	// by itself" produce nothing rather than something arbitrary.
	Exclusions []Recurrence

	// ExDates are individual instants to remove. They are compared by
	// absolute instant rather than wall clock, so an EXDATE written in UTC
	// still cancels the occurrence it names in a zoned schedule.
	ExDates []time.Time
}

// ParseRuleSet builds a RuleSet from a base rule and a list of exclusion
// lines, each of which may be an EXRULE (a recurrence) or an EXDATE (a
// comma-separated instant list).
//
// The two are distinguished by prefix rather than by guessing at content:
// an unprefixed line is treated as an EXRULE, because that is the shape an
// operator copying a rule out of AWX produces, while EXDATE always carries
// its keyword. A line that parses as neither is an error rather than a
// silently ignored exclusion -- an exclusion that quietly does nothing is
// how a maintenance window fails open.
func ParseRuleSet(rule string, exclusions []string) (RuleSet, error) {
	base, err := Parse(rule)
	if err != nil {
		return RuleSet{}, err
	}
	set := RuleSet{Rule: base}

	for i, line := range exclusions {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := cutPrefixFold(line, "EXDATE"); ok {
			dates, err := parseExDates(rest)
			if err != nil {
				return RuleSet{}, fmt.Errorf("exclusion %d: %w", i, err)
			}
			set.ExDates = append(set.ExDates, dates...)
			continue
		}
		ex, err := Parse(line)
		if err != nil {
			return RuleSet{}, fmt.Errorf("exclusion %d: %w", i, err)
		}
		set.Exclusions = append(set.Exclusions, ex)
	}
	return set, nil
}

// Expand returns up to limit occurrences of the rule set at or after
// start, with every exclusion applied, in ascending order.
//
// The base rule and each exclusion are expanded over the same window and
// the exclusions subtracted, rather than testing each base occurrence
// against the exclusion rules one at a time. That is deliberate: testing
// membership would mean asking "does this rule produce exactly this
// instant", which for an ordinal or BYSETPOS rule means expanding its
// whole period anyway, so the set-difference form does the same work while
// staying obviously correct.
func (s RuleSet) Expand(dtstart, start time.Time, loc *time.Location, limit int) ([]time.Time, error) {
	if limit <= 0 {
		return nil, nil
	}
	if len(s.Exclusions) == 0 && len(s.ExDates) == 0 {
		return Expand(s.Rule, dtstart, start, loc, limit)
	}

	budget := limit + exclusionOverscan
	for attempt := 0; attempt < maxExclusionAttempts; attempt++ {
		candidates, err := Expand(s.Rule, dtstart, start, loc, budget)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return nil, nil
		}

		// The exclusion window ends at the last candidate, so an exclusion
		// rule is never expanded further than the base rule reaches.
		window := candidates[len(candidates)-1]
		excluded, err := s.excludedSet(dtstart, start, window, loc, budget)
		if err != nil {
			return nil, err
		}

		kept := make([]time.Time, 0, len(candidates))
		for _, c := range candidates {
			if _, drop := excluded[c.UnixNano()]; drop {
				continue
			}
			kept = append(kept, c)
			if len(kept) == limit {
				return kept, nil
			}
		}

		// Fewer candidates came back than were asked for, so the series
		// itself ended: what survived exclusion is the complete answer.
		if len(candidates) < budget {
			return kept, nil
		}
		budget += exclusionOverscan
	}
	return nil, nil
}

// excludedSet expands every exclusion over the same window as the base
// rule and returns the instants to drop, keyed by UnixNano so the
// comparison is by absolute instant rather than by wall clock.
func (s RuleSet) excludedSet(dtstart, start, window time.Time, loc *time.Location, budget int) (map[int64]struct{}, error) {
	out := make(map[int64]struct{})
	for _, ex := range s.Exclusions {
		occurrences, err := Expand(ex, dtstart, start, loc, budget)
		if err != nil {
			// An unsatisfiable exclusion excludes nothing, which is a
			// coherent answer: it is a rule naming a date that does not
			// exist, so no real occurrence can collide with it. Refusing
			// the whole expansion here would take a schedule offline over
			// a harmless typo in a window that never applies.
			if err == ErrUnsatisfiable {
				continue
			}
			return nil, err
		}
		for _, o := range occurrences {
			if o.After(window) {
				break
			}
			out[o.UnixNano()] = struct{}{}
		}
	}
	for _, d := range s.ExDates {
		out[d.UnixNano()] = struct{}{}
	}
	return out, nil
}

// parseExDates parses an EXDATE value: a comma-separated list of RFC 5545
// date-times, optionally preceded by a ";TZID=Zone" parameter and a colon.
//
// A TZID is honoured when present because an operator importing from AWX
// gets one, and interpreting a zoned EXDATE as UTC would cancel the wrong
// occurrence by the zone's offset -- a silent failure that only shows up
// as a maintenance window that did not hold.
func parseExDates(rest string) ([]time.Time, error) {
	loc := time.UTC
	if strings.HasPrefix(rest, ";") {
		param, remainder, ok := strings.Cut(rest[1:], ":")
		if !ok {
			return nil, fmt.Errorf("%w: EXDATE parameter %q has no value list", ErrMalformed, param)
		}
		name, zone, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "TZID") {
			return nil, fmt.Errorf("%w: EXDATE parameter %q is not TZID", ErrMalformed, param)
		}
		l, err := time.LoadLocation(strings.TrimSpace(zone))
		if err != nil {
			return nil, fmt.Errorf("%w: EXDATE TZID %q is not a known time zone", ErrMalformed, zone)
		}
		loc, rest = l, remainder
	} else {
		rest = strings.TrimPrefix(rest, ":")
	}

	var out []time.Time
	for _, raw := range strings.Split(rest, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		t, err := parseExDate(raw, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: EXDATE lists no dates", ErrMalformed)
	}
	return out, nil
}

// parseExDate parses one EXDATE value in the same three shapes UNTIL
// accepts, resolving an unqualified value in loc.
func parseExDate(s string, loc *time.Location) (time.Time, error) {
	if t, err := time.ParseInLocation("20060102T150405Z", s, time.UTC); err == nil {
		return t, nil
	}
	for _, layout := range []string{"20060102T150405", "20060102"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: EXDATE %q is not an RFC 5545 date or date-time", ErrMalformed, s)
}

// cutPrefixFold strips a case-insensitive prefix, reporting whether it was
// there. It exists because EXDATE may be followed by either ':' or ';' and
// strings.CutPrefix alone cannot express the case-insensitivity RFC 5545
// property names have.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

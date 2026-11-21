// Package main: the text views of `pleiades facts baseline` and `pleiades
// facts drift` (Phase 41d). A key, a value, a reason and a user are text a
// device or a person wrote, so each reaches the terminal through termsafe, as
// the facts view's do.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// printBaselineTaken says what one baseline holds.
func printBaselineTaken(host string, b facts.Baseline) {
	notComparable := 0
	for _, p := range b.Pairs {
		if p.NotComparable {
			notComparable++
		}
	}
	from := ""
	if b.FromName != "" {
		from = " from " + termsafe.Escape(b.FromName)
	}
	fmt.Printf("%s: baseline %q taken%s over %d stable fact(s)", termsafe.Escape(host), b.Name, from, len(b.Pairs)-notComparable)
	if notComparable > 0 {
		fmt.Printf(", %d not comparable (a digest keyed for %s alone)", notComparable, termsafe.Escape(b.FromName))
	}
	fmt.Println()
}

// printDrift is the text view of a drift: each host's state, then each key
// that differs.
func printDrift(doc clireport.FactsDriftReport) {
	if len(doc.Devices) == 0 {
		fmt.Println("no hosts in the inventory")
		return
	}
	for _, d := range doc.Devices {
		host := termsafe.Escape(d.Host)
		switch d.State {
		case facts.DriftUnknown, facts.DriftNoBaseline:
			fmt.Printf("%s: %s: %s\n", host, d.State, termsafe.EscapeLine(d.Reason))
			continue
		}
		fmt.Printf("%s: %s against %s; %d compared, %d match", host, d.State, baselineLine(d.Baseline), d.Compared, d.Matching)
		if n := len(d.NotComparable); n > 0 {
			fmt.Printf(", %d not comparable", n)
		}
		if d.FactsAgeSeconds != nil {
			fmt.Printf("; facts last seen %s ago", age(*d.FactsAgeSeconds))
		}
		fmt.Println()
		for _, c := range d.Changes {
			fmt.Printf("  %s %s%s%s\n", c.Change, termsafe.EscapeLine(c.Key), changeText(c), whenText(c))
		}
		if len(d.Unconfirmed) > 0 {
			keys := make([]string, len(d.Unconfirmed))
			for i, k := range d.Unconfirmed {
				keys[i] = termsafe.EscapeLine(k)
			}
			fmt.Printf("  not confirmed by the latest gather of their family, which a removed item would be (not drift): %s\n", strings.Join(keys, ", "))
		}
	}
}

// baselineLine names a baseline: its name, when, by whom, from where and
// why.
func baselineLine(b *facts.BaselineSummary) string {
	if b == nil {
		return "no baseline"
	}
	from := ""
	if b.FromName != "" {
		from = ", from " + termsafe.Escape(b.FromName)
	}
	return fmt.Sprintf("baseline %q (taken %s by %s%s: %s)", b.Name, b.TakenAt.Local().Format(time.RFC3339), termsafe.EscapeLine(b.By), from, termsafe.EscapeLine(b.Reason))
}

// changeText is what one change shows of its values: none beside a digest.
func changeText(c facts.KeyDrift) string {
	switch {
	case c.Digest:
		return ": holds a digest, so its value is not shown"
	case c.Change == facts.ChangeChanged:
		return ": " + valueText(c.BaselineValue) + " -> " + valueText(c.CurrentValue)
	case c.Change == facts.ChangeAdded:
		return " = " + valueText(c.CurrentValue)
	case c.Change == facts.ChangeRemoved:
		return " (was " + valueText(c.BaselineValue) + ")"
	}
	return ""
}

// valueText is a value as JSON, made safe for a terminal.
func valueText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	return termsafe.EscapeLine(string(raw))
}

// whenText is when a change's current value was observed and last seen.
func whenText(c facts.KeyDrift) string {
	if c.ObservedAt == nil || c.LastSeenAt == nil {
		return ""
	}
	text := fmt.Sprintf("  (observed %s, last seen %s", c.ObservedAt.Local().Format(time.RFC3339), c.LastSeenAt.Local().Format(time.RFC3339))
	if c.AgeSeconds != nil {
		text += ", " + age(*c.AgeSeconds) + " ago"
	}
	return text + ")"
}

// age is a number of seconds as a duration.
func age(seconds int) string {
	return (time.Duration(seconds) * time.Second).String()
}

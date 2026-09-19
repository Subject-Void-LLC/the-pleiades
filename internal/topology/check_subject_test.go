// Package topology: tests of the check subject and consumer.
package topology

import (
	"strings"
	"testing"
)

// TestCheckAndDispatchConsumersAreDisjoint proves exactly one consumer
// matches any dispatch: a check subject never falls under the dispatch
// consumer's filter, and a dispatch subject never under the check
// consumer's. The first half is also what keeps a Runner that predates
// check mode (which creates only the dispatch consumer) from ever
// receiving a check.
func TestCheckAndDispatchConsumersAreDisjoint(t *testing.T) {
	under := func(subject, filter string) bool {
		return strings.HasPrefix(subject, strings.TrimSuffix(filter, ">"))
	}
	dispatch, check := DispatchConsumerConfig(), CheckConsumerConfig()
	if dispatch.Durable == check.Durable {
		t.Fatalf("both consumers are named %q", check.Durable)
	}
	for _, device := range []string{"dev-1", "router.with.dots", "*", ">"} {
		if under(CheckSubject(device), dispatch.FilterSubject) {
			t.Errorf("the dispatch consumer (%s) would receive the check %s", dispatch.FilterSubject, CheckSubject(device))
		}
		if !under(CheckSubject(device), check.FilterSubject) {
			t.Errorf("the check consumer (%s) would not receive the check %s", check.FilterSubject, CheckSubject(device))
		}
		if under(DispatchSubject(device), check.FilterSubject) {
			t.Errorf("the check consumer would receive the real run %s", DispatchSubject(device))
		}
	}
}

// TestCheckSubject_AHostileDeviceIDIsOneToken covers subject construction
// for Phase 46's hardening audit: whatever a device id holds (dots,
// wildcards, whitespace, nothing at all), its check subject is exactly
// the prefix and one token, with no wildcard in it, so a device id can
// never widen a check to other devices or reach another subject space.
func TestCheckSubject_AHostileDeviceIDIsOneToken(t *testing.T) {
	for _, device := range []string{"a.b.c", "*", ">", "pleiades.jobs.dispatch.x", "a b\tc", "", "x\n>", strings.Repeat("d", 4096)} {
		subject := CheckSubject(device)
		token, ok := strings.CutPrefix(subject, checkSubjectPrefix)
		if !ok || token == "" || strings.ContainsAny(token, ".*> \t\r\n") {
			t.Errorf("CheckSubject(%q) = %q, want %q and one plain token", device, subject, checkSubjectPrefix)
		}
	}
}

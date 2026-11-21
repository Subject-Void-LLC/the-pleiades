// Tests that the facts drift document fits its published schema.
package clireport

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestFactsDriftSchema_FitsAFullReport: a drift report with every field
// given a value fits the published schema, each property of it filled.
func TestFactsDriftSchema_FitsAFullReport(t *testing.T) {
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	age := 60
	report := FactsDriftReport{
		SchemaVersion: FactsDriftVersion, At: at, Baseline: "pre-change",
		Devices: []facts.DriftReport{{
			Host: "web1", Device: "id-1", State: facts.DriftDrifted, Reason: "why",
			Baseline: &facts.BaselineSummary{Name: "pre-change", TakenAt: at, By: "ops", Reason: "before the upgrade", From: "id-0", FromName: "golden", KeysID: "project:0011223344556677", Keys: []string{"package.*"}, Pairs: 3},
			Compared: 3, Matching: 1, NotComparable: []string{"file.x"}, KindChanged: []string{"host.uptime"},
			Unconfirmed:     []facts.UnconfirmedKey{{Key: "package.b", WouldBe: facts.ChangeAdded, LastSeenAt: &at, AgeSeconds: &age}},
			Changes:         []facts.KeyDrift{{Key: "package.a", Change: facts.ChangeChanged, BaselineHash: "aa", CurrentHash: "bb", Digest: false, BaselineValue: "1", CurrentValue: "2", ObservedAt: &at, LastSeenAt: &at, AgeSeconds: &age}},
			FactsLastSeenAt: &at, FactsAgeSeconds: &age,
		}},
	}
	fitsStrictly(t, FactsDriftSchema, report, true)
}

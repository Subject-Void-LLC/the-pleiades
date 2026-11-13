// Tests for why a recorded grant grants nothing, and for the definition
// digest every new grant carries.
package record_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// probedWidget is widgetGrant as a probe of the program widgets made it.
func probedWidget(props map[string]any) inventory.Grant {
	g := widgetGrant(props)
	g.Source, g.By, g.Reason, g.Program = inventory.GrantProbed, "", "", "widgets"
	return g
}

// notesOf is what a device built on rec with baseline says of its grants.
func notesOf(rec record.Record, baseline []capability.Name) []inventory.GrantNote {
	return record.NewBase(rec, baseline).GrantNotes()
}

// TestWithGrants_ALapsedGrantNamesItsReason: each way a recorded grant
// grants nothing (a changed definition, properties that no longer fit, a
// requirement the device lacks, a repointed device, no program defining
// it) leaves one lapsed note naming the device, the capability and what to
// do, and a grant that counts leaves none.
func TestWithGrants_ALapsedGrantNamesItsReason(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	ok := map[string]any{"endpoint": "https://widgets.example"}
	baseline := []capability.Name{capability.NameSSHTransport}

	undefined := notesOf(recordWith(probedWidget(ok)), baseline)
	if len(undefined) != 1 || !undefined[0].Lapsed || !strings.Contains(undefined[0].Reason, "no loaded program defines acme.widget_api") {
		t.Fatalf("a grant no program defines: notes %+v", undefined)
	}

	def := widgetDefinition()
	def.Provider = "widgets"
	if err := capability.RegisterData(def); err != nil {
		t.Fatal(err)
	}
	digest := capability.DefinitionDigest(def)
	held := probedWidget(ok)
	held.Definition = digest
	if notes := notesOf(recordWith(held), baseline); len(notes) != 0 {
		t.Fatalf("a grant that holds: notes %+v, want none", notes)
	}

	changed := probedWidget(ok) // made against the definition with no provider
	asserted := widgetGrant(ok)
	misfit := probedWidget(map[string]any{})
	misfit.Definition = digest
	for _, tc := range []struct {
		name     string
		grant    inventory.Grant
		baseline []capability.Name
		want     []string
	}{
		{"a changed definition, probed", changed, baseline, []string{"w1", "acme.widget_api", `program "widgets"`, "onboard it again", "pleiades onboard w1"}},
		{"a changed definition, asserted", asserted, baseline, []string{"w1", "acme.widget_api", "assert it again", "pleiades grant-capability w1 acme.widget_api"}},
		{"properties that no longer fit", misfit, baseline, []string{"w1", "acme.widget_api", "does not fit", "endpoint", "onboard it again"}},
		{"a requirement the device lacks", held, nil, []string{"w1", "acme.widget_api", "needs SSHTransportCapable beside it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordWith(tc.grant)
			notes := notesOf(rec, tc.baseline)
			if len(notes) != 1 || !notes[0].Lapsed || notes[0].Capability != "acme.widget_api" {
				t.Fatalf("notes %+v, want one lapse of acme.widget_api", notes)
			}
			for _, w := range tc.want {
				if !strings.Contains(notes[0].Reason, w) {
					t.Errorf("reason %q does not say %q", notes[0].Reason, w)
				}
			}
			if caps := capsOf(rec, tc.baseline); slices.Contains(caps, "acme.widget_api") {
				t.Errorf("a lapsed grant still declares the capability: %v", caps)
			}
		})
	}

	t.Run("a repointed device", func(t *testing.T) {
		base := record.NewBase(record.Record{Name: "w1", Type: boundGrantType, Properties: map[string]inventory.PropertyValue{"host": "10.0.0.1"}}, baseline)
		if err := base.RecordGrant(held); err != nil {
			t.Fatal(err)
		}
		moved := base.Properties().Raw()
		moved["host"] = "10.0.0.2"
		notes := notesOf(record.Record{Name: "w1", Type: boundGrantType, Properties: moved}, baseline)
		if len(notes) != 1 || !notes[0].Lapsed || !strings.Contains(notes[0].Reason, "other values of host") || !strings.Contains(notes[0].Reason, "onboard it again") {
			t.Fatalf("notes %+v, want the repointed grant's lapse naming host", notes)
		}
	})
}

// TestWithGrants_AnUnreadableTimeLapsesTheGrant: a recorded grant whose
// time cannot be read (missing, malformed, or outside the years a grant
// may hold once in UTC) grants nothing, with one lapsed note saying why
// and what to do, even against the definition it was made against.
func TestWithGrants_AnUnreadableTimeLapsesTheGrant(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(widgetDefinition()); err != nil {
		t.Fatal(err)
	}
	baseline := []capability.Name{capability.NameSSHTransport}
	for name, at := range map[string]any{
		"words":           "yesterday",
		"a number":        float64(7),
		"past year 9999":  "9999-12-31T23:00:00-02:00",
		"before the year": "0001-01-01T00:30:00+01:00",
	} {
		t.Run(name, func(t *testing.T) {
			entry := inventory.GrantsProperty([]inventory.Grant{widgetGrant(map[string]any{"endpoint": "https://w"})})
			entry["acme.widget_api"].(map[string]any)["at"] = at
			rec := record.Record{Name: "w1", Type: grantTypeName, Properties: map[string]inventory.PropertyValue{inventory.GrantedProperty: entry}}
			if caps := capsOf(rec, baseline); slices.Contains(caps, "acme.widget_api") {
				t.Fatalf("a grant whose time cannot be read still grants: %v", caps)
			}
			notes := notesOf(rec, baseline)
			if len(notes) != 1 || !notes[0].Lapsed || !strings.Contains(notes[0].Reason, "cannot be read") || !strings.Contains(notes[0].Reason, "assert it again") {
				t.Errorf("notes %+v, want one lapse saying the grant cannot be read and what to do", notes)
			}
		})
	}
}

// TestWithGrants_ALegacyGrantCountsWithOneWarningOnA03Line: a grant
// written before grants carried a definition digest still counts on a
// 0.3.x tree, with exactly one note, a warning (not a lapse) naming the
// device and the capability.
func TestWithGrants_ALegacyGrantCountsWithOneWarningOnA03Line(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	t.Cleanup(record.SetGrantPolicyLineForTest("0.3.1"))
	if err := capability.RegisterData(widgetDefinition()); err != nil {
		t.Fatal(err)
	}
	legacy := widgetGrant(map[string]any{"endpoint": "https://widgets.example"})
	legacy.Definition = ""
	rec := recordWith(legacy)
	baseline := []capability.Name{capability.NameSSHTransport}
	if caps := capsOf(rec, baseline); !slices.Contains(caps, "acme.widget_api") {
		t.Fatalf("a legacy grant on 0.3.x does not count: caps %v", caps)
	}
	notes := notesOf(rec, baseline)
	if len(notes) != 1 || notes[0].Lapsed || !strings.Contains(notes[0].Reason, "device w1's grant of acme.widget_api predates") {
		t.Fatalf("notes %+v, want one warning naming device w1 and acme.widget_api", notes)
	}
}

// TestWithGrants_ALegacyGrantLapsesFromTheNextMinor: the same grant on
// this tree's own line (0.4.0 and later) grants nothing, saying why.
func TestWithGrants_ALegacyGrantLapsesFromTheNextMinor(t *testing.T) {
	if record.LegacyGrantsCount(buildinfo.CurrentRelease) {
		t.Skipf("this tree is on %s, where a legacy grant still counts", buildinfo.CurrentRelease)
	}
	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(widgetDefinition()); err != nil {
		t.Fatal(err)
	}
	legacy := widgetGrant(map[string]any{"endpoint": "https://widgets.example"})
	legacy.Definition = ""
	rec := recordWith(legacy)
	baseline := []capability.Name{capability.NameSSHTransport}
	if caps := capsOf(rec, baseline); slices.Contains(caps, "acme.widget_api") {
		t.Fatalf("a legacy grant on %s counts: caps %v", buildinfo.CurrentRelease, caps)
	}
	notes := notesOf(rec, baseline)
	if len(notes) != 1 || !notes[0].Lapsed || !strings.Contains(notes[0].Reason, record.GrantDigestRequiredFrom) {
		t.Fatalf("notes %+v, want one lapse naming %s", notes, record.GrantDigestRequiredFrom)
	}
}

// TestLegacyGrantsCount_OnlyBeforeTheNextMinor: the rule's own table,
// failing closed on a line that is not a release.
func TestLegacyGrantsCount_OnlyBeforeTheNextMinor(t *testing.T) {
	for line, want := range map[string]bool{
		"0.3.0": true, "0.3.9": true, "0.2.5": true,
		"0.4.0": false, "0.4.1": false, "0.10.0": false, "1.0.0": false,
		"": false, "0.3": false, "v0.3.1": false, "0.3.x": false, "0.-3.0": false,
	} {
		if got := record.LegacyGrantsCount(line); got != want {
			t.Errorf("LegacyGrantsCount(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestRecordGrant_RefusesAGrantWithoutItsDigest: a grant with no
// definition digest, or one of the wrong shape, is refused, as is an
// assertion naming a build and a probe naming a build that is not a
// digest; nothing is written. A probed grant keeps its digest and build.
func TestRecordGrant_RefusesAGrantWithoutItsDigest(t *testing.T) {
	build := "sha256:" + strings.Repeat("ab", 32)
	for name, mutate := range map[string]func(*inventory.Grant){
		"no definition digest":        func(g *inventory.Grant) { g.Definition = "" },
		"a malformed digest":          func(g *inventory.Grant) { g.Definition = "sha256:abc" },
		"an assertion naming a build": func(g *inventory.Grant) { g.Build = build },
		"a probe naming no digest as its build": func(g *inventory.Grant) {
			g.Source, g.Build = inventory.GrantProbed, "a build"
		},
		"a property shaped like a secret": func(g *inventory.Grant) {
			g.Properties = map[string]any{"endpoint": "https://ops:hunter2-Orchid@widgets.example/api"}
		},
		"no time":                   func(g *inventory.Grant) { g.At = time.Time{} },
		"a time past the year 9999": func(g *inventory.Grant) { g.At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"a time before the year 1": func(g *inventory.Grant) {
			g.At = time.Date(1, 1, 1, 0, 30, 0, 0, time.FixedZone("east", 3600))
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := record.NewBase(record.Record{Name: "w1", Properties: map[string]inventory.PropertyValue{}}, nil)
			g := widgetGrant(map[string]any{"endpoint": "a"})
			mutate(&g)
			if err := b.RecordGrant(g); err == nil || !strings.Contains(err.Error(), "acme.widget_api") {
				t.Fatalf("RecordGrant = %v, want a refusal naming the capability", err)
			}
			if _, present := b.Properties().Raw()[inventory.GrantedProperty]; present || len(b.History()) != 0 {
				t.Error("a refused grant was written")
			}
		})
	}
	b := record.NewBase(record.Record{Name: "w1", Properties: map[string]inventory.PropertyValue{}}, nil)
	g := probedWidget(map[string]any{"endpoint": "a"})
	g.Build = build
	if err := b.RecordGrant(g); err != nil {
		t.Fatal(err)
	}
	grants, _, err := inventory.GrantsFrom(b.Properties())
	if err != nil || len(grants) != 1 || grants[0].Build != build || grants[0].Definition != g.Definition {
		t.Fatalf("grants = %+v, %v; want the probe's build and digest kept", grants, err)
	}
}

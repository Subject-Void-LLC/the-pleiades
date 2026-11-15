// Package yangfacts: tests of the yang.* facts a discovery gives, their
// bounds, and the rule that an unverified device records stats alone.
package yangfacts

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// store is a task's context recording stats and facts, failing a key named in
// failStat or failFact.
type store struct {
	stats, facts       map[string]any
	failStat, failFact string
}

// InjectSecrets implements sdk.RunbookContext.
func (s *store) InjectSecrets() map[string]string { return nil }

// SetStat implements sdk.RunbookContext.
func (s *store) SetStat(key string, value any) error {
	if key == s.failStat {
		return errors.New("stat refused")
	}
	s.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (s *store) EmitFact(key string, value any) error {
	if key == s.failFact {
		return errors.New("fact refused")
	}
	s.facts[key] = value
	return nil
}

// newStore returns an empty store.
func newStore() *store { return &store{stats: map[string]any{}, facts: map[string]any{}} }

// discovery is a complete discovery of two modules.
func discovery() yanglib.Discovery {
	return yanglib.Discovery{
		Modules:      []yanglib.Module{{Name: "ietf-interfaces", Namespace: "urn:if", Revision: "2018-02-20"}, {Name: "vendor-x", Namespace: "urn:x"}},
		Completeness: yanglib.Complete,
		ContentID:    "c1",
	}
}

func TestValues_TheFamilyADiscoveryGives(t *testing.T) {
	values, warnings, err := Values(discovery(), RestconfSource)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Values = %v, %v", warnings, err)
	}
	if got := strings.Join(values[FactModules].([]string), ","); got != "ietf-interfaces@2018-02-20,vendor-x" {
		t.Errorf("yang.modules = %q, want name@revision, the name alone with no revision", got)
	}
	if values[FactModuleCount] != 2 || values[FactCompleteness] != "complete" || values[FactContentID] != "c1" || values[FactModuleDigest] != discovery().ModulesDigest() {
		t.Errorf("values = %v", values)
	}
	d := discovery()
	d.ContentID = ""
	if values, _, _ := Values(d, Source{Fallback: "the hello's, "}); values[FactContentID] != nil {
		t.Error("an empty content-id was recorded")
	}
}

func TestValues_SaysWhatWasLeftOut(t *testing.T) {
	d := discovery()
	d.Modules = nil
	for i := range 4000 {
		d.Modules = append(d.Modules, yanglib.Module{Name: "module-with-a-long-name-" + strings.Repeat("x", 10) + strconv.Itoa(i), Namespace: "urn:m", Revision: "2024-01-01"})
	}
	d.LibraryError = "access denied"
	d.Completeness = yanglib.Unknown
	d.Drop("bad entry")
	values, warnings, err := Values(d, Source{Fallback: "the hello's, "})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values[FactModules]; ok || values[FactModuleCount] != 4000 {
		t.Errorf("yang.modules recorded past its bound, or the count lost: %v", values[FactModuleCount])
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"recorded by count and digest only", "access denied", "the module list is the hello's, unknown", "1 malformed", `"bad entry"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q, want %q", joined, want)
		}
	}
}

func TestFitsAndNonNil(t *testing.T) {
	if !Fits(nil) || !Fits([]string{"a"}) || Fits([]string{strings.Repeat("x", MaxListFactBytes)}) {
		t.Error("Fits misjudged a list against the 60 KiB bound")
	}
	if got := NonNil(nil); got == nil || len(got) != 0 {
		t.Errorf("NonNil(nil) = %#v, want an empty list", got)
	}
	if got := NonNil([]string{"a"}); len(got) != 1 {
		t.Errorf("NonNil kept %v", got)
	}
}

func TestRecord_FactsOnlyFromAVerifiedDevice(t *testing.T) {
	values, _, _ := Values(discovery(), RestconfSource)
	s := newStore()
	if err := Record(s, FactKeys, values, true); err != nil {
		t.Fatal(err)
	}
	if len(s.facts) != len(FactKeys) || len(s.stats) != len(FactKeys) {
		t.Errorf("facts %d, stats %d; want every key as both", len(s.facts), len(s.stats))
	}
	s = newStore()
	if err := Record(s, append([]string{"not.in.values"}, FactKeys...), values, false); err != nil {
		t.Fatal(err)
	}
	if len(s.facts) != 0 || len(s.stats) != len(FactKeys) {
		t.Errorf("facts %v, stats %d; want stats alone from an unverified device", s.facts, len(s.stats))
	}
	for _, failing := range []*store{{stats: map[string]any{}, facts: map[string]any{}, failStat: FactModuleCount}, {stats: map[string]any{}, facts: map[string]any{}, failFact: FactModuleCount}} {
		if err := Record(failing, FactKeys, values, true); err == nil {
			t.Error("a refused stat or fact was not the caller's error")
		}
	}
}

func TestReturns_DocumentsEveryKeyForTheProtocol(t *testing.T) {
	returns := Returns("always here", RestconfSource)
	if len(returns) != len(FactKeys) {
		t.Fatalf("%d returns for %d keys", len(returns), len(FactKeys))
	}
	for i, r := range returns {
		if r.Name != FactKeys[i] || r.Type == "" || r.Description == "" || r.Returned == "" {
			t.Errorf("return %d = %+v, want %s documented", i, r, FactKeys[i])
		}
	}
	if returns[1].Returned != "always here" || returns[4].Returned != RestconfSource.ContentID || returns[3].Description != RestconfSource.Completeness {
		t.Error("the protocol's own wording did not reach the returns")
	}
	if !strings.Contains(returns[0].Description, RestconfSource.Modules) || !strings.Contains(returns[4].Description, RestconfSource.ContentIDFrom) {
		t.Error("the modules or content-id source is not said")
	}
}

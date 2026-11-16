// Package yanglib: tests of the schema entries a library names, against a
// real Catalyst 8000V's libraries (IOS XE 17.15.1a, captured 2026-11-14 and
// 2026-11-15), and of the names a saved source is given.
package yanglib

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
)

// findSchema is the entry named name in schemas, nil when absent.
func findSchema(schemas []Schema, name string) *Schema {
	for i := range schemas {
		if schemas[i].Name == name {
			return &schemas[i]
		}
	}
	return nil
}

// TestSchemas_ARealIOSXEModulesStateOverRESTCONF reads the library IOS XE's
// RESTCONF server answers with in JSON: every module, the import-only ones
// among them, names its source by a schema leaf at the device's own address,
// and a module's submodules name theirs.
func TestSchemas_ARealIOSXEModulesStateOverRESTCONF(t *testing.T) {
	lib, err := ParseJSON(restconftest.IOSXEModulesStateJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	schemas := KeepSchemas(lib.Schemas)
	if len(schemas) != 611 {
		t.Fatalf("schemas = %d, want the capture's 611 entries", len(schemas))
	}
	native := findSchema(schemas, "Cisco-IOS-XE-native")
	if native == nil || native.Revision != "2024-07-01" || native.ImportOnly ||
		strings.Join(native.Locations, ",") != "https://10.10.20.178:443/restconf/tailf/modules/Cisco-IOS-XE-native/2024-07-01" {
		t.Errorf("Cisco-IOS-XE-native = %+v, want its 2024-07-01 source at the device's own address", native)
	}
	importOnly := 0
	for _, s := range schemas {
		if s.ImportOnly {
			importOnly++
		}
	}
	if importOnly != 54 {
		t.Errorf("import-only entries = %d, want the capture's 54", importOnly)
	}
	eigrp := findSchema(schemas, "Cisco-IOS-XE-eigrp")
	if eigrp == nil || len(eigrp.Submodules) != 1 || eigrp.Submodules[0].Key() != "Cisco-IOS-XE-eigrp-obsolete@2022-11-01" ||
		len(eigrp.Submodules[0].Locations) != 1 {
		t.Errorf("Cisco-IOS-XE-eigrp = %+v, want its one submodule with its own location", eigrp)
	}
	// The library the schemas come from still keeps implemented modules
	// alone as Modules, as it did before schemas were read.
	kept, err := Keep(lib, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Modules) != 611-54 || len(kept.Schemas) != 611 {
		t.Errorf("kept %d modules and %d schemas, want 557 and 611", len(kept.Modules), len(kept.Schemas))
	}
}

// TestSchemas_ARealIOSXEYANGLibraryNamesNoLocation holds what the RFC 8525
// library of the same device says: its modules and submodules, and no
// location for any of them, so a RESTCONF client must read modules-state for
// a source's URL. Its schema list (RFC 8525's "schema" elements, named
// "common") is no source location and is not read as one.
func TestSchemas_ARealIOSXEYANGLibraryNamesNoLocation(t *testing.T) {
	for name, raw := range map[string][]byte{"xml": restconftest.IOSXELibraryXML(t), "json": restconftest.IOSXELibraryJSON(t)} {
		var lib Library
		var err error
		if name == "xml" {
			lib, err = ParseXML(raw)
		} else {
			lib, err = ParseJSON(raw)
		}
		if err != nil {
			t.Fatal(err)
		}
		schemas := KeepSchemas(lib.Schemas)
		if len(schemas) <= len(lib.Modules) {
			t.Errorf("%s: %d schemas for %d modules, want the import-only modules beside them", name, len(schemas), len(lib.Modules))
		}
		for _, s := range schemas {
			if len(s.Locations) > 0 {
				t.Fatalf("%s: %s names a location %v, which the capture does not", name, s.Key(), s.Locations)
			}
		}
		if eigrp := findSchema(schemas, "Cisco-IOS-XE-eigrp"); eigrp == nil || len(eigrp.Submodules) != 1 {
			t.Errorf("%s: Cisco-IOS-XE-eigrp = %+v, want its submodule", name, eigrp)
		}
	}
}

// TestSchemas_LocationsOfRFC8525 reads a library that names its sources the
// RFC 8525 way: a location leaf-list on modules, import-only modules and
// submodules.
func TestSchemas_LocationsOfRFC8525(t *testing.T) {
	doc := `<yang-library xmlns="` + Namespace + `"><module-set><name>m</name>
<module><name>a</name><revision>2024-01-01</revision><namespace>urn:a</namespace><location>https://d/yang/a@2024-01-01.yang</location><location>https://d/other/a</location>
<submodule><name>a-sub</name><revision>2024-01-02</revision><location>https://d/yang/a-sub@2024-01-02.yang</location></submodule></module>
<import-only-module><name>types</name><revision>2013-07-15</revision><namespace>urn:t</namespace><location>https://d/yang/types@2013-07-15.yang</location></import-only-module>
</module-set><content-id>1</content-id></yang-library>`
	lib, err := ParseXML([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := Keep(lib, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	d := Discovery{}.WithLibrary(kept)
	if len(d.Modules) != 1 || len(d.Schemas) != 2 {
		t.Fatalf("modules %d, schemas %d; want the one implemented module and both entries", len(d.Modules), len(d.Schemas))
	}
	a, types := findSchema(d.Schemas, "a"), findSchema(d.Schemas, "types")
	if a == nil || len(a.Locations) != 2 || len(a.Submodules) != 1 || a.Submodules[0].Locations[0] != "https://d/yang/a-sub@2024-01-02.yang" {
		t.Errorf("a = %+v, want both locations and its submodule's", a)
	}
	if types == nil || !types.ImportOnly || types.Locations[0] != "https://d/yang/types@2013-07-15.yang" {
		t.Errorf("types = %+v, want an import-only entry with its location", types)
	}
}

// TestSchemas_StayOutOfTheModulesDigest proves where a module is served from
// is no part of the module set's identity: the same modules with and without
// schema entries give one digest.
func TestSchemas_StayOutOfTheModulesDigest(t *testing.T) {
	lib, err := ParseJSON(restconftest.IOSXEModulesStateJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := Keep(lib, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	with := Discovery{}.WithLibrary(kept)
	without := with
	without.Schemas = nil
	if with.ModulesDigest() != without.ModulesDigest() || len(with.Schemas) == 0 {
		t.Errorf("digest with schemas %s, without %s; want one value", with.ModulesDigest(), without.ModulesDigest())
	}
}

// TestKeepSchemas_RefusesAMalformedEntryWhole holds each rule: an entry is
// kept whole or not at all, never with a part of it cut away.
func TestKeepSchemas_RefusesAMalformedEntryWhole(t *testing.T) {
	good := Schema{Name: "good", Revision: "2024-01-01", Locations: []string{" https://d/good "}}
	many := make([]string, MaxLocations+1)
	for i := range many {
		many[i] = "https://d/" + strconv.Itoa(i)
	}
	subs := make([]Schema, MaxListEntries+1)
	for i := range subs {
		subs[i] = Schema{Name: "s" + strconv.Itoa(i)}
	}
	cases := []Schema{
		{Name: "../etc"},
		{Name: "a/b"},
		{Name: ""},
		{Name: "bad-revision", Revision: "2024-1-1"},
		{Name: "empty-location", Locations: []string{" "}},
		{Name: "control-location", Locations: []string{"https://d/\x1b[2J"}},
		{Name: "long-location", Locations: []string{"https://d/" + strings.Repeat("a", MaxLocationBytes)}},
		{Name: "many-locations", Locations: many},
		{Name: "many-submodules", Submodules: subs},
		{Name: "bad-submodule", Submodules: []Schema{{Name: "../x"}}},
		{Name: "nested-submodule", Submodules: []Schema{{Name: "x", Submodules: []Schema{{Name: "y"}}}}},
	}
	for _, c := range cases {
		if kept := KeepSchemas([]Schema{c, good}); len(kept) != 1 || kept[0].Name != "good" {
			t.Errorf("KeepSchemas(%q) = %+v, want only the good entry", c.Name, kept)
		}
	}
	kept := KeepSchemas([]Schema{good, {Name: "good", Revision: "2024-01-01", Locations: []string{"https://elsewhere"}}, {Name: "a"}})
	if len(kept) != 2 || kept[0].Name != "a" || kept[1].Locations[0] != "https://d/good" {
		t.Errorf("KeepSchemas = %+v, want two entries sorted, the first listing of good kept and trimmed", kept)
	}
	all := make([]Schema, MaxModules+5)
	for i := range all {
		all[i] = Schema{Name: "m" + strconv.Itoa(i)}
	}
	if got := len(KeepSchemas(all)); got != MaxModules {
		t.Errorf("KeepSchemas of %d entries kept %d, want MaxModules", len(all), got)
	}
}

// TestSchemaFileName_BuildsOnlyFromAnIdentifierAndADate holds every name a
// file is given to the two words it may be built from.
func TestSchemaFileName_BuildsOnlyFromAnIdentifierAndADate(t *testing.T) {
	for _, c := range []struct {
		s    Schema
		want string
	}{
		{Schema{Name: "ietf-interfaces", Revision: "2018-02-20"}, "ietf-interfaces@2018-02-20.yang"},
		{Schema{Name: "ATM-FORUM-TC-MIB"}, "ATM-FORUM-TC-MIB.yang"},
		{Schema{Name: "a.b_c-d", Revision: "2024-01-01"}, "a.b_c-d@2024-01-01.yang"},
	} {
		if got, err := c.s.FileName(); err != nil || got != c.want {
			t.Errorf("FileName(%+v) = %q, %v; want %q", c.s, got, err, c.want)
		}
	}
	for _, s := range []Schema{
		{Name: "../x"}, {Name: "a/b"}, {Name: ".."}, {Name: ".hidden"}, {Name: ""}, {Name: "a\\b"},
		{Name: "a", Revision: "../../x"}, {Name: "a", Revision: "2024-01-01/.."}, {Name: "a b"},
	} {
		if got, err := s.FileName(); err == nil {
			t.Errorf("FileName(%+v) = %q, want a refusal", s, got)
		}
	}
}

// TestIsRevision holds a revision to RFC 7950's date form.
func TestIsRevision(t *testing.T) {
	for in, want := range map[string]bool{"2024-07-01": true, "2024-7-1": false, "": false, "2024-07-01\n": false, "latest": false} {
		if got := IsRevision(in); got != want {
			t.Errorf("IsRevision(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestParseKey reads how a task names a module.
func TestParseKey(t *testing.T) {
	for in, want := range map[string][2]string{
		"ietf-interfaces":            {"ietf-interfaces", ""},
		"ietf-interfaces@2018-02-20": {"ietf-interfaces", "2018-02-20"},
		" spaced@2018-02-20 ":        {"spaced", "2018-02-20"},
	} {
		name, revision, err := ParseKey(in)
		if err != nil || name != want[0] || revision != want[1] {
			t.Errorf("ParseKey(%q) = %q, %q, %v; want %v", in, name, revision, err, want)
		}
	}
	for _, in := range []string{"", "../x", "x@", "x@latest", "x@2018-02-20@1", "a/b@2018-02-20"} {
		if _, _, err := ParseKey(in); err == nil {
			t.Errorf("ParseKey(%q) succeeded, want a refusal", in)
		}
	}
}

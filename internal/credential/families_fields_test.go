// The reflective completeness test of Credential's two families: every field
// belongs to exactly one, as HasLogin and SNMP place it, and WithSNMP treats
// it as its family says.
package credential_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// familyFaults sets each Credential field alone, by reflection, and names
// each field hasLogin and hasSNMP do not place in exactly one family, and
// each WithSNMP does not treat as that family: an SNMP field it keeps when
// clearing the family or loses on a round trip through SNMP, and a login
// field it touches. The predicates are parameters so the control below can
// prove a field in neither family, or in both, is caught.
func familyFaults(t *testing.T, hasLogin, hasSNMP func(credential.Credential) bool) []string {
	t.Helper()
	typ := reflect.TypeOf(credential.Credential{})
	var faults []string
	for i := range typ.NumField() {
		var c credential.Credential
		field := reflect.ValueOf(&c).Elem().Field(i)
		name := typ.Field(i).Name
		switch field.Kind() {
		case reflect.String:
			field.SetString("value-of-" + strings.ToLower(name))
		case reflect.Slice:
			field.SetBytes([]byte("value-of-" + strings.ToLower(name)))
		default:
			t.Fatalf("Credential.%s is a %s, which this test cannot set: teach it, then place the field in a family", name, field.Kind())
		}
		login, snmp := hasLogin(c), hasSNMP(c)
		switch {
		case login && snmp:
			faults = append(faults, name+" is in both families")
			continue
		case !login && !snmp:
			faults = append(faults, name+" is in neither family")
			continue
		}
		cleared := c.WithSNMP(wire.SNMPCredential{})
		switch {
		case snmp && !reflect.ValueOf(cleared).IsZero():
			faults = append(faults, name+" is an SNMP field that WithSNMP keeps when it clears the family")
		case snmp && !reflect.DeepEqual(c.WithSNMP(c.SNMP()), c):
			faults = append(faults, name+" is an SNMP field lost on a round trip through SNMP and WithSNMP")
		case login && !reflect.DeepEqual(cleared, c):
			faults = append(faults, name+" is a login field that WithSNMP changes")
		}
	}
	return faults
}

// hasSNMP is the SNMP family's own predicate: SNMP() sees a field.
func hasSNMP(c credential.Credential) bool { return !c.SNMP().Empty() }

// TestCredential_EveryFieldIsInExactlyOneFamily: a save that replaces one
// family relies on HasLogin and SNMP to see every field of the other, so a
// login field HasLogin forgot would be dropped without a word by an
// SNMP-only save. Every field set alone is seen by exactly one of them, and
// WithSNMP touches exactly the SNMP ones.
func TestCredential_EveryFieldIsInExactlyOneFamily(t *testing.T) {
	if faults := familyFaults(t, credential.Credential.HasLogin, hasSNMP); len(faults) > 0 {
		t.Errorf("Credential's families: %s; add the field to HasLogin, or to SNMP and WithSNMP and wire.SNMPCredential", strings.Join(faults, "; "))
	}
}

// TestFamilyFaults_CatchesAFieldInNeitherOrBoth is the control: a HasLogin
// that forgets the enable secret leaves it in neither family, and one that
// also counts the USM user puts that in both, and the test above names each.
func TestFamilyFaults_CatchesAFieldInNeitherOrBoth(t *testing.T) {
	forgets := func(c credential.Credential) bool { c.EnablePassword = ""; return c.HasLogin() }
	if got := familyFaults(t, forgets, hasSNMP); !slices.Equal(got, []string{"EnablePassword is in neither family"}) {
		t.Errorf("a field in neither family: faults %q", got)
	}
	doubles := func(c credential.Credential) bool { return c.HasLogin() || c.SNMPUser != "" }
	if got := familyFaults(t, doubles, hasSNMP); !slices.Equal(got, []string{"SNMPUser is in both families"}) {
		t.Errorf("a field in both families: faults %q", got)
	}
}

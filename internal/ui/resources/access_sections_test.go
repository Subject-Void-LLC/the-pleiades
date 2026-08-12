package resources_test

import (
	"strings"
	"testing"
)

// This file covers the per-object Access sections: the grants on one
// record, rendered under that record.
//
// They exist alongside the deployment-wide Access table rather than instead
// of it, because the two answer different questions. The table is the
// auditor's one page; the section answers "who reaches this", asked while
// looking at the thing. Both read the same access.Bindings port, so what
// they cannot do is disagree.

func TestAccessSection_AnOrganizationShowsTheGrantsThatNameIt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstRecordID(t, h, "organizations")
	if id == "" {
		t.Fatal("the fixture rendered no organization to open")
	}

	body := h.get(t, "/ui/organizations/"+id).Body.String()
	if !strings.Contains(body, "Access") {
		t.Fatal("an organization's detail page has no Access section")
	}

	// The seeded grant names this organization at organization scope. The
	// section renders the holding team by name, which is the whole reason
	// it is worth rendering at all.
	if !strings.Contains(body, "conformance-team") {
		t.Error("the Access section does not name the team that holds the grant on this organization")
	}

	// And not the device grant, which carries the same scope_id. scope_id
	// has no foreign key and its values are per level, so matching the id
	// without the level would put another record's grants on this page.
	if strings.Contains(body, ">device<") {
		t.Error("an organization's Access section shows a device-scope grant that merely shares its id")
	}
}

func TestAccessSection_ATeamShowsWhatItReaches(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstRecordID(t, h, "teams")
	if id == "" {
		t.Fatal("the fixture rendered no team to open")
	}

	body := h.get(t, "/ui/teams/"+id).Body.String()
	if !strings.Contains(body, "Access") {
		t.Fatal("a team's detail page has no Access section")
	}

	// The other direction from an organization's section: a team holds
	// grants and is never the target of one, so what it renders is where
	// each grant points. "Everywhere (system)" is the wording the global
	// table uses for a system grant, and it comes from the same builder,
	// which is what keeps the two pages saying the same thing.
	if !strings.Contains(body, "Everywhere (system)") {
		t.Error("a team's Access section does not say where its system-scope grant reaches")
	}
}

func TestAccessSection_SaysSoWhenThereAreNoGrants(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// A record with no grants on it at all: the second organization in the
	// fixture has its own, so this uses a freshly created one.
	w := h.post(t, "/ui/organizations", map[string]string{"name": "ungranted-organization"})
	if w.Code >= 400 {
		t.Fatalf("POST /ui/organizations = %d, want a successful write", w.Code)
	}

	id := recordIDNamed(t, h, "organizations", "ungranted-organization")
	body := h.get(t, "/ui/organizations/"+id).Body.String()

	// An empty table and "nothing grants access to this" look identical,
	// and here the difference matters more than usual: one of them is a
	// statement about a permission boundary.
	if !strings.Contains(body, "No grants name this organization directly") {
		t.Error("an organization with no grants renders an empty table rather than saying so")
	}
}

// recordIDNamed finds the id of the record whose row carries name.
func recordIDNamed(t *testing.T, h *harness, view, name string) string {
	t.Helper()

	list := h.get(t, "/ui/"+view).Body.String()
	prefix := `href="/ui/` + view + `/`
	for _, row := range strings.Split(list, "<tr") {
		if !strings.Contains(row, name) {
			continue
		}
		start := strings.Index(row, prefix)
		if start < 0 {
			continue
		}
		rest := row[start+len(prefix):]
		if end := strings.Index(rest, `"`); end > 0 {
			return rest[:end]
		}
	}
	t.Fatalf("no %s named %q appears in the list", view, name)
	return ""
}

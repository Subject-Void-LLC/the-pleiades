// Package resources_test: a record whose name a URL path must escape is
// opened, edited and deleted through the UI by the link the UI draws for
// it, with the session, CSRF and admission middleware in place.
package resources_test

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// uiAddressedNames returns the names the UI test addresses, unique to this
// call. Each special character appears alone and beside a "/", for the
// reason internal/api's addressedNames gives: a "/" (or a ",") gives the
// request a RawPath, so chi records every value in it still escaped, which
// is what a handler reading chi.URLParam got wrong; alone, the value is
// already decoded, which is what a handler decoding every value would get
// wrong.
func uiAddressedNames(t *testing.T) []string {
	base := uniqueName(t, "addressed")
	return []string{
		base + "/1",
		base + "-50%", base + "/50%",
		base + "-a%2Fb", base + "/a%2Fb",
		base + "-a b", base + "/a b",
		base + "-café", base + "/café",
		base + ",b",
	}
}

// TestDevices_ANameAURLMustEscapeIsReachedByItsLink creates each device
// through the UI, then follows the link the UI answers with to its page,
// its edit form, an update and a delete. A decoy named as each name's
// escaped form is created first: it is the record a handler reading the
// value still escaped would have served instead.
func TestDevices_ANameAURLMustEscapeIsReachedByItsLink(t *testing.T) {
	h := newHarness(t, adminIdentity)
	names := uiAddressedNames(t)

	create := func(t *testing.T, name string) string {
		t.Helper()
		rec := h.post(t, "/ui/devices", map[string]string{"name": name, "type": "linux_server"})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("creating %q = %d, want 303: %s", name, rec.Code, rec.Body.String())
		}
		return rec.Header().Get("Location")
	}
	for _, name := range names {
		if decoy := url.PathEscape(name); decoy != name {
			create(t, decoy)
		}
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			link := create(t, name)
			if want := "/ui/devices/" + url.PathEscape(name); link != want {
				t.Fatalf("creating %q redirected to %s, want %s", name, link, want)
			}

			page := h.get(t, link)
			if page.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", link, page.Code)
			}
			if got := pageTitle(page.Body.String()); !strings.HasPrefix(got, html.EscapeString(name)+" ") {
				t.Fatalf("GET %s rendered the record titled %q, want %q", link, got, name)
			}

			edit := h.get(t, link+"/edit")
			if edit.Code != http.StatusOK {
				t.Fatalf("GET %s/edit = %d, want 200", link, edit.Code)
			}
			// The name is fixed once a device exists, so the edit form
			// carries it only in where it submits to, which must be this
			// record's own address.
			if !strings.Contains(edit.Body.String(), `action="`+html.EscapeString(link)+`"`) {
				t.Errorf("the edit form at %s/edit does not submit to %s", link, link)
			}

			// An update changes the stored device the path named, so the
			// record it returns to is that one.
			updated := h.post(t, link, map[string]string{"tags": "patched"})
			if updated.Code != http.StatusSeeOther {
				t.Fatalf("POST %s = %d, want 303: %s", link, updated.Code, updated.Body.String())
			}
			if got := updated.Header().Get("Location"); got != link {
				t.Errorf("updating %q returned to %s, want %s", name, got, link)
			}
			stored, err := conformanceRepo.GetByName(t.Context(), name)
			if err != nil {
				t.Fatalf("GetByName(%q) after the update = %v, want nil", name, err)
			}
			if got := stored.Tags(); len(got) != 1 || got[0] != "patched" {
				t.Errorf("updating %q stored the tags %v, want [patched]", name, got)
			}

			deleted := h.post(t, link, map[string]string{"_method": http.MethodDelete})
			if deleted.Code != http.StatusSeeOther {
				t.Fatalf("deleting %s = %d, want 303: %s", link, deleted.Code, deleted.Body.String())
			}
			retired := conformanceRepo.retiredNames()
			if len(retired) == 0 || retired[len(retired)-1] != name {
				t.Errorf("deleting %s retired %v last, want %q", link, retired, name)
			}
		})
	}
}

// pageTitle is the text of a page's <title> element, as rendered.
func pageTitle(body string) string {
	start := strings.Index(body, "<title>")
	end := strings.Index(body, "</title>")
	if start < 0 || end < start {
		return ""
	}
	return body[start+len("<title>") : end]
}

// TestDevices_CreateRefusesANameNoURLCarries submits the create form with a
// name of "." and "..". Each is refused on the name field with the reason
// (422, the form redisplayed) and nothing reaches the store; an empty name
// is the shared validator's, since the field is required.
func TestDevices_CreateRefusesANameNoURLCarries(t *testing.T) {
	h := newHarness(t, adminIdentity)
	before := conformanceRepo.itemCount()
	for _, name := range []string{".", ".."} {
		rec := h.post(t, "/ui/devices", map[string]string{"name": name, "type": "linux_server"})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("creating a device named %q = %d, want 422: %s", name, rec.Code, rec.Body.String())
		}
		if want := html.EscapeString(`the name "` + name + `" cannot be used`); !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the form for %q does not say why it was refused", name)
		}
	}
	if rec := h.post(t, "/ui/devices", map[string]string{"name": "", "type": "linux_server"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("creating a device with no name = %d, want 422", rec.Code)
	}
	if after := conformanceRepo.itemCount(); after != before {
		t.Errorf("the store holds %d devices after the refused creates, want %d", after, before)
	}
}

// TestDevices_ANameEqualToAFixedSegmentOpensItsOwnPage creates devices named
// "new" and "chart.json", the fixed segments the router matches beside a
// record's {id}, and follows the link the UI answers with. Linked as written,
// "new" opened the create form and "chart.json" a chart's data; each must
// open its own page. Neither name is refused: both are legitimate hostnames.
func TestDevices_ANameEqualToAFixedSegmentOpensItsOwnPage(t *testing.T) {
	h := newHarness(t, adminIdentity)
	for _, name := range []string{"new", "chart.json"} {
		t.Run(name, func(t *testing.T) {
			rec := h.post(t, "/ui/devices", map[string]string{"name": name, "type": "linux_server"})
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("creating %q = %d, want 303: %s", name, rec.Code, rec.Body.String())
			}
			link := rec.Header().Get("Location")
			page := h.get(t, link)
			if page.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", link, page.Code)
			}
			if got := pageTitle(page.Body.String()); !strings.HasPrefix(got, name+" ") {
				t.Fatalf("the link %s for the device %q opened %q, not the device's own page", link, name, got)
			}
			list := h.get(t, "/ui/devices")
			if !strings.Contains(list.Body.String(), `href="`+link+`"`) {
				t.Errorf("the device list does not link %q as %s", name, link)
			}
		})
	}
}

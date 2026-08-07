package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// This file covers PLAN.md Section 21.2's OPTIONS pre-flight: "Every
// endpoint natively supports the OPTIONS method... It returns an Allow
// header listing only the HTTP methods the user is authorized to perform
// (e.g., Allow: GET, OPTIONS)."
//
// The header value in that example is asserted literally below, because
// the whole requirement is that a Viewer and an Admin hitting the same URL
// get different answers. A test that only checked "some Allow header
// exists" would pass against chi's own built-in behavior, which emits the
// unfiltered method set and is exactly what this replaces.

// allowSet parses an Allow header into its methods.
func allowSet(t *testing.T, raw string) []string {
	t.Helper()
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func TestOptions_AllowHeaderIsFilteredByCallerScope(t *testing.T) {
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	for _, tc := range []struct {
		name     string
		identity *auth.Identity
		want     string
		why      string
	}{
		{
			name:     "read only caller",
			identity: readOnlyIdentity,
			want:     "GET, OPTIONS",
			why:      "PLAN.md Section 21.2's own worked example, asserted literally",
		},
		{
			name:     "read and write caller",
			identity: readWriteIdentity,
			want:     "DELETE, GET, OPTIONS",
			why:      "the positive half: without it, a filtered header is indistinguishable from an empty one",
		},
		{
			name:     "admin holding no explicit scopes",
			identity: adminIdentity,
			want:     "DELETE, GET, OPTIONS",
			why:      "the admin bypass must reach the Allow header the same way it reaches _links",
		},
		{
			name:     "caller holding an unrelated scope",
			identity: unrelatedID,
			want:     "OPTIONS",
			why:      "OPTIONS is always permitted: refusing to answer 'what may I do here' tells a caller nothing it can act on, which is what Section 21.2 exists to fix",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGateFixture(t)
			rr := f.do(t, http.MethodOptions, target, tc.identity)

			if rr.Code != http.StatusNoContent {
				t.Errorf("OPTIONS returned %d, want %d", rr.Code, http.StatusNoContent)
			}
			if got := rr.Header().Get("Allow"); got != tc.want {
				t.Errorf("Allow is %q, want %q (%s)", got, tc.want, tc.why)
			}
			// A body would be a second, redundant representation of the
			// contract the header already carries, free to drift from it.
			if rr.Body.Len() != 0 {
				t.Errorf("OPTIONS returned a body of %d bytes, want none: the Allow header is the whole contract", rr.Body.Len())
			}
		})
	}
}

func TestOptions_AllowIsOneCommaJoinedHeaderValue(t *testing.T) {
	// RFC 9110 Section 10.2.1 permits both spellings, but chi's own
	// handler calls Header().Add in a loop and emits one line per method.
	// A client splitting on "," sees the full set only if this is one
	// value, so the spelling is asserted rather than left to chance.
	f := newGateFixture(t)
	rr := f.do(t, http.MethodOptions, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, readWriteIdentity)

	values := rr.Header().Values("Allow")
	if len(values) != 1 {
		t.Fatalf("Allow emitted as %d header values %q, want exactly 1 comma-joined value", len(values), values)
	}
	if got := allowSet(t, values[0]); len(got) != 3 {
		t.Errorf("Allow parsed to %v, want 3 methods", got)
	}
}

func TestOptions_UnauthenticatedIsRejected(t *testing.T) {
	// The permitted set is caller-dependent, so an anonymous answer would
	// either lie by listing everything or hand a scanner the route table.
	f := newGateFixture(t)
	rr := f.do(t, http.MethodOptions, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous OPTIONS returned %d, want 401", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "" {
		t.Errorf("anonymous OPTIONS disclosed an Allow header %q", got)
	}
}

func TestOptions_UnknownPathIs404(t *testing.T) {
	f := newGateFixture(t)
	rr := f.do(t, http.MethodOptions, api.APIVersionPrefix+"/no/such/resource", readWriteIdentity)

	if rr.Code != http.StatusNotFound {
		t.Errorf("OPTIONS on an unrouted path returned %d, want 404", rr.Code)
	}
}

func TestOptions_DoesNotDiscloseWhetherTheResourceExists(t *testing.T) {
	// The handler answers purely from the route table and never touches
	// the repository, so a caller cannot use OPTIONS to enumerate which
	// device names are real.
	f := newGateFixture(t)
	base := api.APIVersionPrefix + "/inventory/devices/"

	real := f.do(t, http.MethodOptions, base+gateDeviceName, readWriteIdentity)
	fake := f.do(t, http.MethodOptions, base+"definitely-not-a-real-device", readWriteIdentity)

	if real.Code != fake.Code {
		t.Errorf("OPTIONS status differs for a real (%d) and an absent (%d) device", real.Code, fake.Code)
	}
	if real.Header().Get("Allow") != fake.Header().Get("Allow") {
		t.Errorf("OPTIONS Allow differs for a real (%q) and an absent (%q) device",
			real.Header().Get("Allow"), fake.Header().Get("Allow"))
	}
}

func TestMethodNotAllowed_AllowHeaderIsAlsoScopeFiltered(t *testing.T) {
	// chi's built-in 405 handler emits every registered method regardless
	// of permission. Left in place it would tell a read-only caller that
	// DELETE is available here, contradicting both the _links array and
	// the OPTIONS response that same caller just received.
	f := newGateFixture(t)
	rr := f.do(t, http.MethodPut, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, readOnlyIdentity)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT returned %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "GET, OPTIONS" {
		t.Errorf("405 Allow is %q, want %q: it must agree with what OPTIONS told the same caller", got, "GET, OPTIONS")
	}
}

func TestOptions_AgreesWithTheLinksArray(t *testing.T) {
	// One mechanism, two renderings. Both read the same route table and
	// ask the same generator, so this asserts they cannot drift: every
	// method named in Allow, other than OPTIONS itself, must appear as a
	// link's method, and vice versa.
	for _, id := range []*auth.Identity{readOnlyIdentity, readWriteIdentity, adminIdentity} {
		t.Run(id.Subject, func(t *testing.T) {
			f := newGateFixture(t)
			target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

			allow := map[string]bool{}
			for _, m := range allowSet(t, f.do(t, http.MethodOptions, target, id).Header().Get("Allow")) {
				if m != http.MethodOptions {
					allow[m] = true
				}
			}

			linked := map[string]bool{}
			body := decodeDevice(t, f.do(t, http.MethodGet, target, id))
			if body.Links != nil {
				for _, l := range *body.Links {
					linked[l.Method] = true
				}
			}

			for m := range allow {
				if !linked[m] {
					t.Errorf("Allow offers %s but no link does", m)
				}
			}
			for m := range linked {
				if !allow[m] {
					t.Errorf("a link offers %s but Allow does not", m)
				}
			}
		})
	}
}

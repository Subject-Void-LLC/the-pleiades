package apispec

import (
	"net/http"
	"testing"
)

func TestEndpoints_WellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Endpoints {
		if e.Name == "" {
			t.Errorf("endpoint %s %s has an empty Name", e.Method, e.Pattern)
		}
		if seen[e.Name] {
			t.Errorf("duplicate endpoint Name %q", e.Name)
		}
		seen[e.Name] = true

		if e.Method == "" {
			t.Errorf("endpoint %q has an empty Method", e.Name)
		}
		if e.Pattern == "" {
			t.Errorf("endpoint %q has an empty Pattern", e.Name)
		}
		if e.Scope == "" {
			t.Errorf("endpoint %q has an empty Scope", e.Name)
		}
		if e.Rel == "" {
			t.Errorf("endpoint %q has an empty Rel", e.Name)
		}
		if e.Summary == "" {
			t.Errorf("endpoint %q has an empty Summary", e.Name)
		}
		if e.Description == "" {
			t.Errorf("endpoint %q has an empty Description", e.Name)
		}
		if len(e.Responses) == 0 {
			t.Errorf("endpoint %q documents no Responses", e.Name)
		}
		for _, r := range e.Responses {
			if r.Status == 0 {
				t.Errorf("endpoint %q has a Response with a zero Status", e.Name)
			}
			if r.Description == "" {
				t.Errorf("endpoint %q response %d has an empty Description", e.Name, r.Status)
			}
		}
		for _, p := range e.Params {
			if p.Name == "" {
				t.Errorf("endpoint %q has a Param with an empty Name", e.Name)
			}
			if p.In != "path" && p.In != "query" {
				t.Errorf("endpoint %q param %q has In %q, want \"path\" or \"query\"", e.Name, p.Name, p.In)
			}
			if p.Description == "" {
				t.Errorf("endpoint %q param %q has an empty Description", e.Name, p.Name)
			}
		}
	}
}

// TestEndpoints_PathParamsMatchPattern proves every "{name}" placeholder
// in an Endpoint's Pattern has a matching Param documented with In ==
// "path", and vice versa: a route whose real chi pattern requires a path
// parameter cannot silently omit it from the generated documentation, and
// a documented path parameter cannot silently name a segment the pattern
// does not actually have.
func TestEndpoints_PathParamsMatchPattern(t *testing.T) {
	for _, e := range Endpoints {
		documented := map[string]bool{}
		for _, p := range e.Params {
			if p.In == "path" {
				documented[p.Name] = true
			}
		}

		inPattern := map[string]bool{}
		var name string
		inBraces := false
		for _, r := range e.Pattern {
			switch {
			case r == '{':
				inBraces = true
				name = ""
			case r == '}':
				inBraces = false
				inPattern[name] = true
			case inBraces:
				name += string(r)
			}
		}

		for n := range inPattern {
			if !documented[n] {
				t.Errorf("endpoint %q: pattern %q names path param %q, not documented in Params", e.Name, e.Pattern, n)
			}
		}
		for n := range documented {
			if !inPattern[n] {
				t.Errorf("endpoint %q: Params documents path param %q, not present in pattern %q", e.Name, n, e.Pattern)
			}
		}
	}
}

func TestEndpoint_Route(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	route := GetJob.Route(handler)

	if route.Method != GetJob.Method {
		t.Errorf("route.Method = %q, want %q", route.Method, GetJob.Method)
	}
	if route.Pattern != GetJob.Pattern {
		t.Errorf("route.Pattern = %q, want %q", route.Pattern, GetJob.Pattern)
	}
	if route.Scope != GetJob.Scope {
		t.Errorf("route.Scope = %q, want %q", route.Scope, GetJob.Scope)
	}
	if route.Rel != GetJob.Rel {
		t.Errorf("route.Rel = %q, want %q", route.Rel, GetJob.Rel)
	}
	if route.Handler == nil {
		t.Error("route.Handler is nil")
	}
}

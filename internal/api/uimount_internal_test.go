package api

import (
	"net/http"
	"strings"
	"testing"
)

// validateUIMount decides where the web UI may be mounted inside this
// router, and every refusal below is a real way to break the control plane
// by configuration rather than by code.
//
// The failure shape is what makes this worth its own test: a bad mount point
// does not error at runtime, it silently shadows something. A UI mounted at
// "/" swallows every API route. A UI mounted at "/healthz" makes the
// liveness probe start returning HTML, which a Kubernetes readiness check
// reads as a 200 and reports as healthy while nothing works. Each of those
// must be refused at construction, where a human is still watching.

func uiConfig(prefix string) *RouterConfig {
	return &RouterConfig{
		UI:       http.NotFoundHandler(),
		UIPrefix: prefix,
	}
}

func TestValidateUIMount_AcceptsAnOrdinarySubtree(t *testing.T) {
	for _, prefix := range []string{"/ui", "/console", "/admin/ui", "/ui-v2"} {
		if err := validateUIMount(uiConfig(prefix)); err != nil {
			t.Errorf("validateUIMount(%q) = %v, want nil", prefix, err)
		}
	}
}

// TestValidateUIMount_NoUIMeansNoConstraint: a controller serving no UI has
// no prefix to check, and demanding one would break every caller that does
// not want a UI at all.
func TestValidateUIMount_NoUIMeansNoConstraint(t *testing.T) {
	if err := validateUIMount(&RouterConfig{}); err != nil {
		t.Errorf("validateUIMount with no UI = %v, want nil", err)
	}
	if err := validateUIMount(&RouterConfig{UIPrefix: "/ui"}); err != nil {
		t.Errorf("validateUIMount with a prefix but no UI = %v, want nil", err)
	}
}

func TestValidateUIMount_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		want   string
	}{
		{"empty prefix", "", "UIPrefix is empty"},
		{"relative prefix", "ui", "must start with /"},
		{"root", "/", "would shadow every other route"},

		// The API subtree. A UI here would shadow the routes it exists to
		// present, and the failure would look like the API disappearing.
		{"the API prefix itself", APIVersionPrefix, "collides with the versioned API prefix"},
		{"inside the API prefix", APIVersionPrefix + "/ui", "collides with the versioned API prefix"},

		// Operational endpoints. These are the dangerous ones: an
		// orchestrator reads a 200 as healthy, so a UI serving HTML at
		// /healthz would report a broken controller as fine.
		{"healthz", "/healthz", "operational endpoint"},
		{"readyz", "/readyz", "operational endpoint"},
		{"metrics", "/metrics", "operational endpoint"},
		{"well-known", "/.well-known", "operational endpoint"},
		{"beneath healthz", "/healthz/ui", "operational endpoint"},
		{"beneath well-known", "/.well-known/ui", "operational endpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUIMount(uiConfig(tc.prefix))
			if err == nil {
				t.Fatalf("validateUIMount(%q) = nil, want an error mentioning %q", tc.prefix, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validateUIMount(%q) = %q, want it to mention %q", tc.prefix, err, tc.want)
			}
		})
	}
}

// TestValidateUIMount_PrefixLookAlikesAreAllowed is the boundary case in the
// other direction: /healthzz and /metrics-ui are different trees, and
// refusing them would be a substring check pretending to be a path check.
func TestValidateUIMount_PrefixLookAlikesAreAllowed(t *testing.T) {
	for _, prefix := range []string{"/healthzz", "/metrics-ui", "/readyz-console"} {
		if err := validateUIMount(uiConfig(prefix)); err != nil {
			t.Errorf("validateUIMount(%q) = %v, want nil: it is a sibling of a reserved "+
				"path, not inside one", prefix, err)
		}
	}
}

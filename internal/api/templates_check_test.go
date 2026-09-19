// Package api_test: tests of the template check route.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// checkPath is a template's check route.
func checkPath(templateID int) string {
	return "/api/v1/templates/" + strconv.Itoa(templateID) + "/check"
}

// launchedMode posts body to target, requires 202, and returns the mode the
// persisted job records: what the fan-out reads, rather than what the
// response says.
func (f *templateFixture) launchedMode(t *testing.T, target, body string) (jobID, mode string) {
	t.Helper()
	rec := doJSON(t, f.router, http.MethodPost, target, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST %s: status = %d, want 202: %s", target, rec.Code, rec.Body.String())
	}
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	job, _, err := f.jobs.Get(context.Background(), launched.JobID)
	if err != nil {
		t.Fatalf("reading the job: %v", err)
	}
	return launched.JobID, job.Fields.String(launch.ModeField)
}

// TestCheckRoute_RunsTheTemplateAsACheck covers the route's one promise:
// the job it creates is a check, without the template opening its mode
// field (the fixture's template opens limit, forks and extra_vars only),
// with the rest of the launch body applied as a launch applies it, and a
// relaunch of it is a check too.
func TestCheckRoute_RunsTheTemplateAsACheck(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	jobID, mode := f.launchedMode(t, checkPath(tmpl.ID), "")
	if mode != "check" {
		t.Errorf("POST /check with no body made a job with mode %q, want check", mode)
	}
	if _, mode := f.launchedMode(t, checkPath(tmpl.ID), `{"overrides":{"limit":"edge-07","mode":"check"}}`); mode != "check" {
		t.Errorf("POST /check asking for check made a job with mode %q", mode)
	}
	if _, mode := f.launchedMode(t, "/api/v1/jobs/"+jobID+"/relaunch", ""); mode != "check" {
		t.Errorf("relaunching a check made a job with mode %q, want check: a relaunch repeats what ran", mode)
	}
	if _, mode := f.launchedMode(t, launchPath(tmpl.ID), ""); mode != "execute" {
		t.Errorf("the control, POST /launch with no body, made a job with mode %q, want execute", mode)
	}
}

// TestCheckRoute_RefusesAnythingButACheck covers the refusals: a body
// asking the check route for any other mode is refused rather than
// overruled, and a kind that cannot run as a check is refused rather than
// run. Each is 422 and none creates a job.
func TestCheckRoute_RefusesAnythingButACheck(t *testing.T) {
	f := newTemplateFixture(t)
	runbook := f.createTemplate(t, f.gateTemplateBody("patch"))
	playbook := f.createTemplate(t, fmt.Sprintf(`{
		"name": "legacy", "kind": "playbook", "definition": "playbooks/other.yml", "inventory": %d
	}`, f.invA))

	before := f.client.Job.Query().CountX(context.Background())
	for name, tc := range map[string]struct {
		template int
		body     string
	}{
		"a body asking for a real run":     {runbook.ID, `{"overrides":{"mode":"execute"}}`},
		"a body asking for a mode unknown": {runbook.ID, `{"overrides":{"mode":"chekc"}}`},
		"a mode that is not text":          {runbook.ID, `{"overrides":{"mode":true}}`},
		"a mode that is an object":         {runbook.ID, `{"overrides":{"mode":{"check":true}}}`},
		"a playbook":                       {playbook.ID, ""},
	} {
		rec := doJSON(t, f.router, http.MethodPost, checkPath(tc.template), tc.body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422: %s", name, rec.Code, rec.Body.String())
		}
	}
	if after := f.client.Job.Query().CountX(context.Background()); after != before {
		t.Errorf("refused checks created %d job(s)", after-before)
	}
}

// TestCheckRoute_NamesItsOwnAffordance covers the route's hypermedia: its
// 202 carries a "check" link to itself, never "execute", and OPTIONS
// answers POST. A client told "check" can follow it and reach a check and
// nothing else, which is the reason the route exists.
func TestCheckRoute_NamesItsOwnAffordance(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	rec := doJSON(t, f.router, http.MethodPost, checkPath(tmpl.ID), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Links []struct {
			Rel    string `json:"rel"`
			Href   string `json:"href"`
			Method string `json:"method"`
		} `json:"_links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	var rels []string
	for _, l := range body.Links {
		rels = append(rels, l.Rel)
		if l.Rel == "check" && (l.Method != http.MethodPost || l.Href != checkPath(tmpl.ID)) {
			t.Errorf("the check link is %s %s, want POST %s", l.Method, l.Href, checkPath(tmpl.ID))
		}
	}
	if !slices.Equal(rels, []string{"check"}) {
		t.Errorf("the check route's links are %v, want exactly [check]", rels)
	}

	rec = doJSON(t, f.router, http.MethodOptions, checkPath(tmpl.ID), "")
	if got := rec.Header().Get("Allow"); !strings.Contains(got, http.MethodPost) {
		t.Errorf("OPTIONS %s: Allow = %q, want POST in it", checkPath(tmpl.ID), got)
	}
}

// TestCheckRoute_ACheckOnlyCallerMayCheckAndNotRun covers runbook:check
// through real tokens, the real authentication middleware and the real
// scope rule: a caller holding only runbook:check may use the check route
// and not the launch route, and the job it creates may not run an
// external program's check; a caller holding runbook:execute may use both
// (execute implies check), and its check may. A caller with neither may
// do neither.
func TestCheckRoute_ACheckOnlyCallerMayCheckAndNotRun(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	issuer := authtest.New(t, "check-scope-issuer", "check-scope-audience")
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store), api.WithLaunchConfigs(f.store))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate),
			apispec.CheckTemplate.Route(dispatcher.CheckFromTemplate),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(target string, id *auth.Identity) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		req.Header.Set("Authorization", issuer.BearerToken(t, id))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	externalChecks := func(rr *httptest.ResponseRecorder) bool {
		t.Helper()
		var launched decodedLaunch
		if err := json.Unmarshal(rr.Body.Bytes(), &launched); err != nil {
			t.Fatal(err)
		}
		job, _, err := f.jobs.Get(context.Background(), launched.JobID)
		if err != nil {
			t.Fatal(err)
		}
		return job.ExternalChecks
	}

	checker := &auth.Identity{Subject: "auditor@example.com", Role: auth.RoleViewer, Scopes: []auth.Scope{auth.ScopeRunbookCheck}}
	runner := &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeRunbookExecute}}
	nobody := &auth.Identity{Subject: "viewer@example.com", Role: auth.RoleViewer, Scopes: []auth.Scope{auth.ScopeTemplateRead}}

	if rr := post(launchPath(tmpl.ID), checker); rr.Code != http.StatusForbidden {
		t.Errorf("a check-only caller launching for real: %d, want 403", rr.Code)
	}
	rr := post(checkPath(tmpl.ID), checker)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("a check-only caller checking: %d, want 202: %s", rr.Code, rr.Body.String())
	}
	if externalChecks(rr) {
		t.Error("a check-only caller's check may run an external program's check")
	}

	rr = post(checkPath(tmpl.ID), runner)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("an execute holder checking: %d, want 202: %s", rr.Code, rr.Body.String())
	}
	if !externalChecks(rr) {
		t.Error("an execute holder's check may not run an external program's check")
	}
	if rr := post(launchPath(tmpl.ID), runner); rr.Code != http.StatusAccepted {
		t.Errorf("an execute holder launching: %d, want 202", rr.Code)
	}

	for _, target := range []string{launchPath(tmpl.ID), checkPath(tmpl.ID)} {
		if rr := post(target, nobody); rr.Code != http.StatusForbidden {
			t.Errorf("a caller with neither scope, POST %s: %d, want 403", target, rr.Code)
		}
	}
}

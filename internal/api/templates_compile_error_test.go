// Tests for a template and a runbook that does not compile (FAILURE_PATTERNS
// 1360), over the real template store, the real runbook source and the
// catalog the Controller wires between them: creating a template over such a
// runbook is a 400 with the builder's message, and a template whose runbook
// broke later can still be read, listed, edited and deleted while its launch
// is refused.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// compileTemplateRouter is the template routes over the real store, whose
// catalog is api.RunbookCatalog over c's source, as cmd/controller wires it.
// It returns the router and the inventory a template can name.
func compileTemplateRouter(t *testing.T, c *compileSource) (http.Handler, int) {
	t.Helper()
	ctx := context.Background()
	client := newSerializedSQLiteClient(t, fmt.Sprintf("templates-compile-%d", templateFixtureSeq.Add(1)))
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	inv := client.Inventory.Create().SetName("edge routers").SetOrganization(org).SaveX(ctx)

	store := launch.NewEntStore(client, launch.NewSourceCatalog(map[string]launch.KindCatalog{
		launch.DefaultKind: api.RunbookCatalog(c.src),
	}))
	dispatcher := api.NewDispatcher(c.src, dispatch.NewEntJobStore(client), newCapturingBus(),
		api.WithTemplates(store), api.WithLaunchConfigs(store))
	handler := api.NewTemplateHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListTemplates.Route(handler.List),
			apispec.GetTemplate.Route(handler.Get),
			apispec.CreateTemplate.Route(handler.Create),
			apispec.UpdateTemplate.Route(handler.Update),
			apispec.DeleteTemplate.Route(handler.Delete),
			apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router, inv.ID
}

// TestTemplates_CreatingOneOverARunbookThatDoesNotCompileIs400: the create
// names a definition nothing can launch, so it is a 400 with the builder's
// message, where the catalog used to hand the handler an error it could only
// answer with a 500 and "internal error". A missing runbook is still a 400
// naming it.
func TestTemplates_CreatingOneOverARunbookThatDoesNotCompileIs400(t *testing.T) {
	router, inv := compileTemplateRouter(t, newCompileSource(t))
	body := func(definition string) string {
		return fmt.Sprintf(`{"name":"t-%s","kind":"runbook","definition":%q,"inventory":%d}`, definition, definition, inv)
	}

	rec := doJSON(t, router, http.MethodPost, "/api/v1/templates", body("broken"))
	if missing := says(rec.Body.String(), builderSays); rec.Code != http.StatusBadRequest || len(missing) > 0 {
		t.Errorf("a template over a runbook that does not compile: status %d, body %s; want 400 saying %q", rec.Code, rec.Body.String(), builderSays)
	}
	rec = doJSON(t, router, http.MethodPost, "/api/v1/templates", body("missing"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no runbook") {
		t.Errorf("a template over a missing runbook: status %d, body %s; want 400 naming it", rec.Code, rec.Body.String())
	}
	if rec = doJSON(t, router, http.MethodPost, "/api/v1/templates", body("plain")); rec.Code != http.StatusCreated {
		t.Errorf("control: a template over a runbook that compiles = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
}

// TestRunbookCatalog_TellsTheKindsApart: the catalog answers a missing runbook
// as launch.ErrDefinitionNotFound, one that does not compile as
// launch.ErrInvalidTemplate, and a runbook it cannot read as neither, so a
// storage error is never shown as the author's mistake.
func TestRunbookCatalog_TellsTheKindsApart(t *testing.T) {
	c := newCompileSource(t)
	catalog := api.RunbookCatalog(c.src)
	ctx := context.Background()

	if err := catalog.Verify(ctx, "plain"); err != nil {
		t.Errorf("Verify(plain) = %v, want nil", err)
	}
	if err := catalog.Verify(ctx, "missing"); !errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Errorf("Verify(missing) = %v, want ErrDefinitionNotFound", err)
	}
	err := catalog.Verify(ctx, "broken")
	if !errors.Is(err, launch.ErrInvalidTemplate) || len(says(err.Error(), builderSays)) > 0 {
		t.Errorf("Verify(broken) = %v, want ErrInvalidTemplate with the builder's message", err)
	}
	// An id the source refuses before it reads anything is a lookup error.
	err = catalog.Verify(ctx, "../etc")
	if err == nil || errors.Is(err, launch.ErrInvalidTemplate) || errors.Is(err, launch.ErrDefinitionNotFound) || api.CompileMessage(err) != "" {
		t.Errorf("Verify of an id the source refuses = %v, want a plain source error", err)
	}
	if ids, err := catalog.List(ctx); err != nil || len(ids) != 2 {
		t.Errorf("List = %v, %v; want the source's two ids", ids, err)
	}
}

// TestTemplates_OneWhoseRunbookBrokeLaterStaysReadableEditableAndDeletable: a
// template created while its runbook compiled is still read, listed, renamed
// and deleted after the runbook stops compiling; only its launch is refused,
// with the builder's message.
func TestTemplates_OneWhoseRunbookBrokeLaterStaysReadableEditableAndDeletable(t *testing.T) {
	c := newCompileSource(t)
	c.write(t, "later", "id: later\ntasks:\n  - name: step\n    noop: {}\n")
	router, inv := compileTemplateRouter(t, c)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/templates",
		fmt.Sprintf(`{"name":"patch","kind":"runbook","definition":"later","inventory":%d}`, inv))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body %s", rec.Code, rec.Body.String())
	}
	var created decodedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/templates/%d", created.ID)

	c.write(t, "later", strings.Replace(brokenRunbook, "id: broken", "id: later", 1))

	if rec := doJSON(t, router, http.MethodGet, path, ""); rec.Code != http.StatusOK {
		t.Errorf("GET = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodGet, "/api/v1/templates", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"patch"`) {
		t.Errorf("list = %d, want 200 holding the template; body %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodPatch, path, `{"name":"patch, renamed"}`); rec.Code != http.StatusOK {
		t.Errorf("rename = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodPost, path+"/launch", `{}`)
	if missing := says(rec.Body.String(), builderSays); rec.Code != http.StatusUnprocessableEntity || len(missing) > 0 {
		t.Errorf("launch = %d, body %s; want 422 saying %q", rec.Code, rec.Body.String(), builderSays)
	}
	if rec := doJSON(t, router, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
}

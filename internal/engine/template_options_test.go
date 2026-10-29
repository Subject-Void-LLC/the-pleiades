// Tests that a parameter holding only a statement or a comment renders,
// that raw keeps one literal, and that a template-file method's trim_blocks
// and lstrip_blocks reach the renderer for src and content alike, through
// the real Executor.
package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// runFixture registers enginetest.opts with params, runs one task of it
// with the YAML params given, and returns what the method was handed, or
// the node's error.
func runFixture(t *testing.T, decl []collection.Param, params string, files map[string]string) (map[string]any, error) {
	t.Helper()
	// Restored on return, since one test runs several fixtures.
	restore := collection.SnapshotForTest()
	defer restore()
	var got map[string]any
	if err := collection.Register(collection.Descriptor{
		Name: "enginetest.opts",
		Manifest: collection.Manifest{
			Status:           collection.StatusImplemented,
			Reversibility:    collection.Reversibility{Notes: "a test fixture that changes nothing"},
			ExecutionContext: collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
			Doc:              collection.Doc{Params: decl},
		},
		Invoke: func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			got = p
			return collection.Result{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	read := func(name string, _ int64) ([]byte, error) {
		data, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s: no such file", name)
		}
		return []byte(data), nil
	}
	dag := yamlRunbook(t, "id: r\ntasks:\n  - name: t\n    enginetest.opts:\n      "+params+"\n")
	x := engine.NewExecutor(mapResolver{}, engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext),
		lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(map[string]any{"env": "prod", "hosts": []any{"a", "b"}}), engine.WithRenderer(render.New()), engine.WithTemplateFiles(read))
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return got, result.Nodes[0].Err
}

// TestStatementOnlyParamsRender: a parameter holding {% or {# and no {{
// renders, and raw keeps what it wraps as written.
func TestStatementOnlyParamsRender(t *testing.T) {
	decl := []collection.Param{{Name: "text", Type: "string"}}
	for params, want := range map[string]string{
		`text: "{% if vars.env == 'prod' %}p{% else %}d{% endif %}"`: "p",
		`text: "a{# note #}b"`:                                 "ab",
		`text: "printf '{% raw %}{%s}{% endraw %}'"`:           "printf '{%s}'",
		`text: "{% for h in vars.hosts %}{{ h }}{% endfor %}"`: "ab",
	} {
		got, err := runFixture(t, decl, params, nil)
		if err != nil || got["text"] != want {
			t.Errorf("%s: handed %#v, %v; want %q", params, got["text"], err, want)
		}
	}
	if _, err := runFixture(t, decl, `text: "printf '{%s}'"`, nil); err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("a literal {%% that is not a statement gave %v, want a refusal naming the parameter", err)
	}
}

// TestTemplateFileBlockOptions: trim_blocks defaults to what the method
// declares (true here, as file.template does), lstrip_blocks to false, both
// apply to src and to content, and a value that is not written out as true
// or false is refused before the method runs.
func TestTemplateFileBlockOptions(t *testing.T) {
	decl := []collection.Param{
		{Name: "src", Type: "string", Format: collection.ParamFormatTemplateFile},
		{Name: "content", Type: "string"},
		{Name: "trim_blocks", Type: "bool", Default: "true"},
		{Name: "lstrip_blocks", Type: "bool", Default: "false"},
	}
	files := map[string]string{"t/h.conf": "{% for h in vars.hosts %}\n  {{ h }}\n  {% endfor %}\nend\n"}
	for _, tc := range []struct {
		name, params, want, wantErr string
	}{
		{name: "src trims by default", params: "src: t/h.conf", want: "  a\n    b\n  end\n"},
		{name: "src with both", params: "src: t/h.conf\n      lstrip_blocks: true", want: "  a\n  b\nend\n"},
		{name: "src with trim off", params: "src: t/h.conf\n      trim_blocks: false", want: "\n  a\n  \n  b\n  \nend\n"},
		{name: "content trims too", params: "content: \"{% if vars.env %}\\nx\\n{% endif %}\\n\"", want: "x\n"},
		{name: "content with trim off", params: "content: \"{% if vars.env %}\\nx\\n{% endif %}\\n\"\n      trim_blocks: false", want: "\nx\n\n"},
		{name: "plain content is left as written", params: "content: plain", want: "plain"},
		{name: "a templated option is refused", params: "src: t/h.conf\n      trim_blocks: \"{{ vars.env }}\"", wantErr: "true or false"},
		{name: "an option that is text is refused", params: "content: x\n      lstrip_blocks: \"yes\"", wantErr: "true or false"},
	} {
		got, err := runFixture(t, decl, tc.params, files)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: error %v, want %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got["content"] != tc.want {
			t.Errorf("%s: content %#v, %v; want %q", tc.name, got["content"], err, tc.want)
		}
	}

	// A method without the two parameters keeps Jinja's defaults.
	plain := []collection.Param{{Name: "src", Type: "string", Format: collection.ParamFormatTemplateFile}, {Name: "content", Type: "string"}}
	got, err := runFixture(t, plain, "src: t/h.conf", files)
	if err != nil || got["content"] != "\n  a\n  \n  b\n  \nend\n" {
		t.Errorf("a method declaring no options got %#v, %v; want Jinja's defaults", got["content"], err)
	}
}

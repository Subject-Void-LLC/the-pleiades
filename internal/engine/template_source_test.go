// Tests for template files (file.template's src) through the real Executor:
// the engine reads the project file, renders it and hands the method the
// rendered text as content, and every refusal happens before the method runs.
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

// TestTemplateFile_RendersToContent runs a method that declares a
// template-file parameter and checks what it is handed, and that a refusal
// stops the task before the method is called.
func TestTemplateFile_RendersToContent(t *testing.T) {
	files := map[string]string{
		"t/ok.conf":    "env={{ vars.env }}\nhost={{ vars.host | upper }}\n",
		"t/typo.conf":  "env={{ vars.enviroment }}\n",
		"t/roots.conf": "x={{ secret.token.value }}\n",
		"t/bad.conf":   "x={{ vars.env \n",
		"t/lit.conf":   "v={{ vars.literal }}\n",
	}
	read := func(name string, _ int64) ([]byte, error) {
		data, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s: no such file", name)
		}
		return []byte(data), nil
	}
	vars := map[string]any{"env": "prod", "host": "sw1", "literal": "{{ not.rendered }}"}

	for _, tc := range []struct {
		name    string
		params  string
		reader  engine.TemplateFileReader
		want    map[string]any
		wantErr string
	}{
		{name: "src renders into content and is removed", params: "src: t/ok.conf\n      mode: \"0644\"", reader: read,
			want: map[string]any{"content": "env=prod\nhost=SW1\n", "mode": "0644"}},
		{name: "a rendered value holding braces is not rendered again", params: "src: t/lit.conf", reader: read,
			want: map[string]any{"content": "v={{ not.rendered }}\n"}},
		{name: "other params still render", params: "src: t/ok.conf\n      mode: \"{{ vars.env }}\"", reader: read,
			want: map[string]any{"content": "env=prod\nhost=SW1\n", "mode": "prod"}},
		{name: "an undefined variable fails closed", params: "src: t/typo.conf", reader: read, wantErr: "vars.enviroment"},
		{name: "a root a parameter may not read is refused", params: "src: t/roots.conf", reader: read, wantErr: "secret"},
		{name: "a template that does not compile is refused", params: "src: t/bad.conf", reader: read, wantErr: "t/bad.conf"},
		{name: "a missing file is refused", params: "src: t/nope.conf", reader: read, wantErr: "no such file"},
		{name: "content and src together are refused", params: "src: t/ok.conf\n      content: x", reader: read, wantErr: "not both"},
		{name: "a computed src is refused", params: "src: \"t/{{ vars.env }}.conf\"", reader: read, wantErr: "written out"},
		{name: "no project is refused naming content", params: "src: t/ok.conf", reader: nil, wantErr: "pass the template text as content"},
		{name: "inline content still renders", params: "content: \"e={{ vars.env }}\"", reader: read, want: map[string]any{"content": "e=prod"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			var got map[string]any
			ran := false
			if err := collection.Register(collection.Descriptor{
				Name: "enginetest.tplfile",
				Manifest: collection.Manifest{
					Status:           collection.StatusImplemented,
					Reversibility:    collection.Reversibility{Notes: "a test fixture that changes nothing"},
					ExecutionContext: collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
					Doc: collection.Doc{Params: []collection.Param{
						{Name: "src", Type: "string", Format: collection.ParamFormatTemplateFile},
						{Name: "content", Type: "string"},
						{Name: "mode", Type: "string"},
					}},
				},
				Invoke: func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
					ran = true
					got = params
					return collection.Result{}, nil
				},
			}); err != nil {
				t.Fatal(err)
			}
			dag := yamlRunbook(t, "id: r\ntasks:\n  - name: tpl\n    enginetest.tplfile:\n      "+tc.params+"\n")
			opts := []engine.ExecutorOption{engine.WithVariables(vars), engine.WithRenderer(render.New())}
			if tc.reader != nil {
				opts = append(opts, engine.WithTemplateFiles(tc.reader))
			}
			x := engine.NewExecutor(mapResolver{}, engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext),
				lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			node := result.Nodes[0]
			if tc.wantErr != "" {
				if node.Err == nil || !strings.Contains(node.Err.Error(), tc.wantErr) {
					t.Fatalf("node error = %v, want it to say %q", node.Err, tc.wantErr)
				}
				if ran {
					t.Error("the method ran after a refusal")
				}
				return
			}
			if node.Err != nil {
				t.Fatalf("node failed: %v", node.Err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("method params = %#v, want %#v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("param %s = %#v, want %#v", k, got[k], v)
				}
			}
			if _, has := got["src"]; has {
				t.Error("src reached the method, so an unrendered template could be written")
			}
		})
	}
}

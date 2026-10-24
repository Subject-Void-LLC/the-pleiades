// Package clidialog_test: reading prompts, ask and expect_disconnect into a
// dialog, and a model answering a question nobody listed.
package clidialog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// bare is a run context with no project, as a Walk Runner's is.
type bare struct{}

func (bare) InjectSecrets() map[string]string   { return nil }
func (bare) SetStat(string, interface{}) error  { return nil }
func (bare) EmitFact(string, interface{}) error { return nil }

// provider is a local Ollama provider record at base.
type provider struct {
	*inventorytest.Stub
	base string
}

func (p provider) AIKind() string                          { return "ollama" }
func (p provider) AIBaseURL() string                       { return p.base }
func (p provider) AIResidency() string                     { return "local" }
func (p provider) AIPrices() map[string]capability.AIPrice { return nil }
func (p provider) AIHostGuard() string                     { return "" }
func (p provider) AIAllowPlaintextCredentials() bool       { return false }

// project is a run context whose project holds one provider and keeps
// what is written to it.
type project struct {
	bare
	providers map[string]inventory.InventoryItem
}

func (p *project) AITrustedKeys() ([]byte, bool, error)          { return nil, false, nil }
func (p *project) AIPrompt(string) ([]byte, []byte, error)       { return nil, nil, errors.New("none") }
func (p *project) AIPromptVersions() (sdk.PromptVersions, error) { return versions{}, nil }
func (p *project) AIWrite(string, []byte, bool) error            { return nil }
func (p *project) AIDropped(string, string) []string             { return nil }
func (p *project) AIDrop(string, string, string)                 {}
func (p *project) AIProvider(_ context.Context, name string) (inventory.InventoryItem, map[string]string, error) {
	item, ok := p.providers[name]
	if !ok {
		return nil, nil, fmt.Errorf("%s is not an onboarded provider", name)
	}
	return item, nil, nil
}

// versions is an empty prompt version register.
type versions struct{}

func (versions) Newest(string, string) (int64, bool, error) { return 0, false, nil }
func (versions) Use(string, string, int64) error            { return nil }

// ollama answers every chat with answer, as Ollama streams it.
func ollama(t *testing.T, answer string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		enc, _ := json.Marshal(answer)
		_, _ = fmt.Fprintf(w, "{\"model\":\"m:latest\",\"message\":{\"content\":%s},\"done\":false}\n", enc)
		_, _ = fmt.Fprint(w, "{\"model\":\"m:latest\",\"message\":{\"content\":\"\"},\"done\":true,\"prompt_eval_count\":12,\"eval_count\":5}\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// choices are an overwrite question's two answers.
func choices() []any {
	return []any{
		map[string]any{"id": "confirm", "answer": "", "means": "Press Enter to overwrite."},
		map[string]any{"id": "decline", "answer": "n", "means": "Answer no."},
	}
}

func TestRead_PromptsAndExpectDisconnect(t *testing.T) {
	params := map[string]any{
		"expect_disconnect": true,
		"prompts": []any{
			map[string]any{"match": `Destination filename`, "answer": ""},
			map[string]any{"match": `over ?write`, "refuse": true},
		},
	}
	if !clidialog.Uses(params) || clidialog.Uses(map[string]any{"command": "x"}) {
		t.Fatal("Uses did not tell a dialog from a plain command")
	}
	opts, err := clidialog.Read(context.Background(), bare{}, "net.x", params)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ExpectDisconnect || len(opts.Prompts) != 2 || opts.Prompts[0].Answer != "" || opts.Prompts[0].Refuse || !opts.Prompts[1].Refuse || opts.Ask != nil {
		t.Fatalf("options %+v", opts)
	}
	if !opts.Prompts[0].Match.MatchString("Destination filename [a]?") {
		t.Error("the match did not compile as written")
	}
}

func TestRead_RefusesWhatDoesNotRead(t *testing.T) {
	ask := func(extra map[string]any) map[string]any {
		m := map[string]any{"provider": "w", "model": "m", "choices": choices()}
		for k, v := range extra {
			m[k] = v
		}
		return map[string]any{"ask": m}
	}
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"prompts not a list", map[string]any{"prompts": "x"}, "is a list"},
		{"an entry not a mapping", map[string]any{"prompts": []any{"x"}}, "not a mapping"},
		{"no match", map[string]any{"prompts": []any{map[string]any{"answer": ""}}}, "match is required"},
		{"a match that is not text", map[string]any{"prompts": []any{map[string]any{"match": 3, "answer": ""}}}, "regular expression"},
		{"a match that does not compile", map[string]any{"prompts": []any{map[string]any{"match": "(", "answer": ""}}}, "match"},
		{"an answer that is not text", map[string]any{"prompts": []any{map[string]any{"match": "x", "answer": 1}}}, "answer is text"},
		{"both answer and refuse", map[string]any{"prompts": []any{map[string]any{"match": "x", "answer": "", "refuse": true}}}, "not both"},
		{"refuse that is not a bool", map[string]any{"prompts": []any{map[string]any{"match": "x", "refuse": "yes"}}}, "refuse"},
		{"an unknown key", map[string]any{"prompts": []any{map[string]any{"match": "x", "answr": ""}}}, "answr"},
		{"expect_disconnect not a bool", map[string]any{"expect_disconnect": "yes"}, "expect_disconnect"},
		{"ask not a mapping", map[string]any{"ask": "w"}, "is a mapping"},
		{"no choices", ask(map[string]any{"choices": nil}), "choices is a non-empty list"},
		{"a choice not a mapping", ask(map[string]any{"choices": []any{"x"}}), "not a mapping"},
		{"a bad id", ask(map[string]any{"choices": []any{map[string]any{"id": "Confirm!", "answer": "", "means": "m"}}}), "lowercase"},
		{"a reserved id", ask(map[string]any{"choices": []any{map[string]any{"id": "refuse", "answer": "", "means": "m"}}}), "not refuse or wait"},
		{"no answer", ask(map[string]any{"choices": []any{map[string]any{"id": "a", "means": "m"}}}), "answer is text"},
		{"no means", ask(map[string]any{"choices": []any{map[string]any{"id": "a", "answer": ""}}}), "means"},
		{"an unknown choice key", ask(map[string]any{"choices": []any{map[string]any{"id": "a", "answer": "", "means": "m", "x": 1}}}), "x is not one of"},
		{"a duplicate id", ask(map[string]any{"choices": []any{map[string]any{"id": "a", "answer": "", "means": "m"}, map[string]any{"id": "a", "answer": "n", "means": "m"}}}), "listed twice"},
		{"max out of range", ask(map[string]any{"max": 11}), "max is 1 to 10"},
		{"max not a number", ask(map[string]any{"max": "3"}), "max"},
		{"a shape that does not compile", ask(map[string]any{"shape": "("}), "shape"},
		{"no project", ask(nil), "Walk tier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := clidialog.Read(context.Background(), bare{}, "net.x", tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRead_AskChoosesThroughTheModel(t *testing.T) {
	srv := ollama(t, `{"choice":"decline","confidence":0.8,"reason":"keep the file"}`)
	rc := &project{providers: map[string]inventory.InventoryItem{"workstation": provider{Stub: &inventorytest.Stub{StubName: "workstation"}, base: srv.URL}}}
	opts, err := clidialog.Read(context.Background(), rc, "net.x", map[string]any{"ask": map[string]any{
		"provider": "workstation", "model": "m:latest", "residency": "local", "choices": choices(), "max": 2, "shape": `\?`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Ask.Max != 2 || len(opts.Ask.Choices) != 2 || opts.Ask.Choices[1].Answer != "n" || !opts.Ask.Shape.MatchString("?") {
		t.Fatalf("ask %+v", opts.Ask)
	}
	d, err := opts.Ask.Choose(context.Background(), "Do you want to over write? [confirm]", opts.Ask.Choices)
	if err != nil || d.ID != "decline" || d.Reason != "keep the file" || d.Detail["provider"] != "workstation" {
		t.Fatalf("decision %+v, err %v", d, err)
	}

	if _, err := clidialog.Read(context.Background(), rc, "net.x", map[string]any{"ask": map[string]any{
		"provider": "elsewhere", "model": "m", "choices": choices(),
	}}); err == nil || !strings.Contains(err.Error(), "not an onboarded provider") {
		t.Fatalf("an unknown provider: %v", err)
	}
}

func TestRead_AModelErrorFailsTheChoice(t *testing.T) {
	srv := ollama(t, `not json`)
	rc := &project{providers: map[string]inventory.InventoryItem{"w": provider{Stub: &inventorytest.Stub{StubName: "w"}, base: srv.URL}}}
	opts, err := clidialog.Read(context.Background(), rc, "net.x", map[string]any{"ask": map[string]any{"provider": "w", "model": "m", "choices": choices()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opts.Ask.Choose(context.Background(), "q?", nil); err == nil {
		t.Fatal("an answer that is not the schema's was taken")
	}
}

func TestStats_RecordsEachExchange(t *testing.T) {
	stats := clidialog.Stats(netcli.DialogResult{Disconnected: true, Exchanges: []netcli.Exchange{
		{Question: "Destination filename [a]?", Prompt: 0},
		{Question: "over write? [confirm]", Answer: "", Prompt: -1, Choice: "confirm", Reason: "r", Detail: map[string]any{"provider": "w"}},
	}})
	exchanges := stats["exchanges"].([]any)
	first, second := exchanges[0].(map[string]any), exchanges[1].(map[string]any)
	if stats["disconnected"] != true || first["prompt"] != 0 || second["choice"] != "confirm" || second["provider"] != "w" {
		t.Fatalf("stats %v", stats)
	}
	if _, set := second["prompt"]; set {
		t.Error("a model's exchange named a prompt")
	}
}

func TestParams_DocumentEveryParameter(t *testing.T) {
	names := map[string]bool{}
	for _, p := range clidialog.Params() {
		names[p.Name] = p.Description != ""
	}
	for _, want := range []string{clidialog.ParamPrompts, clidialog.ParamAsk, clidialog.ParamExpectDisconnect} {
		if !names[want] {
			t.Errorf("%s is not documented", want)
		}
	}
	if len(clidialog.Returns()) != 2 {
		t.Errorf("returns %v", clidialog.Returns())
	}
}

// TestReadConfig_TakesPromptsAndAskAndRefusesADisconnect: a configuration
// method reads prompts as Read does, and refuses expect_disconnect, set
// either way; its reference documents prompts and ask and no disconnect.
func TestReadConfig_TakesPromptsAndAskAndRefusesADisconnect(t *testing.T) {
	opts, err := clidialog.ReadConfig(context.Background(), bare{}, "net.x", map[string]any{
		"prompts": []any{map[string]any{"match": `\[yes/no\]`, "answer": "yes"}},
	})
	if err != nil || len(opts.Prompts) != 1 || opts.Prompts[0].Answer != "yes" {
		t.Fatalf("options %+v, %v", opts, err)
	}
	for _, v := range []any{true, false} {
		if _, err := clidialog.ReadConfig(context.Background(), bare{}, "net.x", map[string]any{"expect_disconnect": v}); err == nil || !strings.Contains(err.Error(), "configuration lines") {
			t.Errorf("expect_disconnect %v: %v", v, err)
		}
	}
	var names []string
	for _, p := range clidialog.ConfigParams() {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "prompts,ask" {
		t.Errorf("documented %v", names)
	}
	if r := clidialog.ConfigReturns(); len(r) != 1 || r[0].Name != "exchanges" {
		t.Errorf("returns %v", r)
	}
	if len(clidialog.Params()) != 3 {
		t.Error("ConfigParams changed Params' own list")
	}
}

// Package aitest is what the ai methods' tests share: a real HTTP server
// speaking the part of Ollama's API the methods use, holding models on
// disk and in memory; real ai_provider devices, onboarded; and a run
// context with a project in memory (sdk.AIProject). It is imported by
// tests only.
package aitest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Ollama is a fake Ollama host: models on disk and in memory, a chat that
// answers Answer, and a log of every request.
type Ollama struct {
	*httptest.Server
	mu sync.Mutex
	// Disk and Memory are the models on disk and in memory, with tags.
	Disk, Memory []string
	// Answer is what every chat answers.
	Answer string
	// Reject, when set, is an option a chat refuses with a 400 naming it.
	Reject string
	// FailStatus, when set, is the status every chat fails with.
	FailStatus int
	requests   []string
	chats      int
}

// NewOllama starts a fake host with disk on it and nothing in memory.
func NewOllama(t *testing.T, disk ...string) *Ollama {
	t.Helper()
	o := &Ollama{Disk: append([]string(nil), disk...), Answer: "OK"}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(o.Close)
	return o
}

// Requests is every request so far, as "METHOD /path".
func (o *Ollama) Requests() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.requests...)
}

// Chats is how many chat calls reached the host.
func (o *Ollama) Chats() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.chats
}

// State is what is on disk and in memory, sorted.
func (o *Ollama) State() (disk, memory []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	disk, memory = append([]string(nil), o.Disk...), append([]string(nil), o.Memory...)
	slices.Sort(disk)
	slices.Sort(memory)
	return disk, memory
}

func (o *Ollama) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	model, _ := req["model"].(string)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, r.Method+" "+r.URL.Path)
	list := func(names []string) {
		models := make([]map[string]any, len(names))
		for i, n := range names {
			models[i] = map[string]any{"name": n, "size": 1000 + i, "digest": fmt.Sprintf("sha256:%064d", i)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/tags":
		list(o.Disk)
	case "GET /api/ps":
		list(o.Memory)
	case "POST /api/generate":
		if !slices.Contains(o.Disk, model) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model not found"}`))
			return
		}
		o.Memory = slices.DeleteFunc(o.Memory, func(m string) bool { return m == model })
		if keep, _ := req["keep_alive"].(float64); keep != 0 {
			o.Memory = append(o.Memory, model)
		}
		_, _ = w.Write([]byte(`{"done":true}`))
	case "POST /api/pull":
		_, _ = w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"status\":\"success\"}\n"))
		if !slices.Contains(o.Disk, model) {
			o.Disk = append(o.Disk, model)
		}
	case "DELETE /api/delete":
		o.Disk = slices.DeleteFunc(o.Disk, func(m string) bool { return m == model })
	case "POST /api/chat":
		o.chats++
		opts, _ := req["options"].(map[string]any)
		if _, sent := opts[o.Reject]; o.Reject != "" && sent {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":"option %s is not supported"}`, o.Reject)
			return
		}
		if o.FailStatus != 0 {
			w.WriteHeader(o.FailStatus)
			_, _ = w.Write([]byte(`{"error":"overloaded"}`))
			return
		}
		answer, _ := json.Marshal(o.Answer)
		_, _ = fmt.Fprintf(w, "{\"model\":%q,\"message\":{\"content\":%s},\"done\":false}\n", model, answer)
		_, _ = fmt.Fprintf(w, "{\"model\":%q,\"message\":{\"content\":\"\"},\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":10,\"eval_count\":4}\n", model)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// Provider builds an onboarded ai_provider named name from props, with a
// discovery bound to them, as onboarding writes one.
func Provider(t *testing.T, name string, props map[string]any) inventory.InventoryItem {
	t.Helper()
	p := map[string]inventory.PropertyValue{}
	for k, v := range props {
		p[k] = v
	}
	d := inventory.Discovery{Protocol: "https", Capabilities: []capability.Name{capability.NameAIProvider}}
	d.Binding = record.DiscoveryBinding(ai.TypeProvider, inventory.NewProperties(p))
	p[inventory.DiscoveredProperty] = d.Property()
	item, err := ai.NewProvider(record.Record{ID: inventory.DeviceID(name), Name: name, Type: ai.TypeProvider, Properties: p})
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasCapability(capability.NameAIProvider) {
		t.Fatalf("%s does not hold %s", name, capability.NameAIProvider)
	}
	return item
}

// Local is an onboarded local Ollama provider on host, guarded when
// guarded is set.
func Local(t *testing.T, name string, host *Ollama, guarded bool) inventory.InventoryItem {
	t.Helper()
	props := map[string]any{ai.KindProperty: "ollama", ai.APIBaseProperty: host.URL, ai.ResidencyProperty: ai.ResidencyLocal,
		ai.PricesProperty: "m:latest=1/2", ai.CurrencyProperty: "USD"}
	if guarded {
		props[ai.HostGuardProperty] = ai.GuardSingleResident
	}
	return Provider(t, name, props)
}

// Context is a run context with a project in memory: stats kept, trusted
// keys, prompts and a version register, written files, providers by name,
// and the run's dropped parameters. A nil *Context field is absent.
type Context struct {
	Secrets   map[string]string
	Stats     map[string]any
	Keys      []byte
	Prompts   map[string][2][]byte
	Versions  map[string]int64
	Written   map[string][]byte
	Providers map[string]inventory.InventoryItem
	// Reviews are the reviews held, by key; Decide, when set, decides a
	// held one at once, as a person at the terminal would.
	Reviews map[string]sdk.Review
	Decide  func(sdk.Review) sdk.ReviewStatus
	mu      sync.Mutex
	dropped map[string][]string
}

// NewContext is a context with nothing in its project.
func NewContext() *Context {
	return &Context{Stats: map[string]any{}, Versions: map[string]int64{}, Written: map[string][]byte{}, Prompts: map[string][2][]byte{}}
}

func (c *Context) InjectSecrets() map[string]string { return c.Secrets }
func (c *Context) SetStat(k string, v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Stats[k] = v
	return nil
}
func (c *Context) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// AITrustedKeys implements sdk.AIProject.
func (c *Context) AITrustedKeys() ([]byte, bool, error) { return c.Keys, c.Keys != nil, nil }

// AIPrompt implements sdk.AIProject.
func (c *Context) AIPrompt(name string) ([]byte, []byte, error) {
	p, ok := c.Prompts[name]
	if !ok {
		return nil, nil, fmt.Errorf("the project has no prompt %s", name)
	}
	return p[0], p[1], nil
}

// AIPromptVersions implements sdk.AIProject.
func (c *Context) AIPromptVersions() (sdk.PromptVersions, error) { return versions{c}, nil }

// AIWrite implements sdk.AIProject, keeping each file's bytes and
// refusing to write a result over another.
func (c *Context) AIWrite(rel string, data []byte, appendTo bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.Written[rel]; exists && !appendTo {
		return fmt.Errorf("%s already exists", rel)
	}
	if !strings.HasPrefix(rel, "ai/usage/") && !strings.HasPrefix(rel, "ai/bench/") {
		return fmt.Errorf("%s is not under ai/usage or ai/bench", rel)
	}
	c.Written[rel] = append(c.Written[rel], data...)
	return nil
}

// AIProvider implements sdk.AIProject.
func (c *Context) AIProvider(_ context.Context, name string) (inventory.InventoryItem, map[string]string, error) {
	item, ok := c.Providers[name]
	if !ok {
		return nil, nil, fmt.Errorf("no provider named %s", name)
	}
	return item, map[string]string{}, nil
}

// AIDropped implements sdk.AIProject.
func (c *Context) AIDropped(provider, model string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dropped[provider+"/"+model]...)
}

// AIDrop implements sdk.AIProject.
func (c *Context) AIDrop(provider, model, param string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dropped == nil {
		c.dropped = map[string][]string{}
	}
	if !slices.Contains(c.dropped[provider+"/"+model], param) {
		c.dropped[provider+"/"+model] = append(c.dropped[provider+"/"+model], param)
	}
}

// UsageLines is the records appended to the sink at rel.
func (c *Context) UsageLines(t *testing.T, rel string) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(c.Written[rel])), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a usage line is not JSON: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

// versions is a Context's version register.
type versions struct{ c *Context }

func (v versions) Newest(kind, name string) (int64, bool, error) {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	n, ok := v.c.Versions[kind+"/"+name]
	return n, ok, nil
}

func (v versions) Use(kind, name string, version int64) error {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if version > v.c.Versions[kind+"/"+name] {
		v.c.Versions[kind+"/"+name] = version
	}
	return nil
}

// Decided implements sdk.Reviewer over the context's reviews.
func (c *Context) Decided(key string) (sdk.Review, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.Reviews[key]
	return r, ok, nil
}

// Hold implements sdk.Reviewer: Decide, when set, is the person at the
// terminal; with none the review is kept pending, as the Crawl tier keeps
// it with nobody there.
func (c *Context) Hold(_ context.Context, r sdk.Review) (sdk.Review, error) {
	r.Status = sdk.ReviewPending
	if c.Decide != nil {
		r.Status = c.Decide(r)
		r.DecidedBy = "tester"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Reviews == nil {
		c.Reviews = map[string]sdk.Review{}
	}
	c.Reviews[r.Key] = r
	return r, nil
}

// Unreviewed is a context with a project and no reviewer: a run with
// nobody to ask, as a Walk Runner's is until Phase 27.
type Unreviewed struct{ C *Context }

func (u Unreviewed) InjectSecrets() map[string]string     { return u.C.InjectSecrets() }
func (u Unreviewed) SetStat(k string, v any) error        { return u.C.SetStat(k, v) }
func (u Unreviewed) EmitFact(k string, v any) error       { return u.C.EmitFact(k, v) }
func (u Unreviewed) AITrustedKeys() ([]byte, bool, error) { return u.C.AITrustedKeys() }
func (u Unreviewed) AIPrompt(n string) ([]byte, []byte, error) {
	return u.C.AIPrompt(n)
}
func (u Unreviewed) AIPromptVersions() (sdk.PromptVersions, error) { return u.C.AIPromptVersions() }
func (u Unreviewed) AIWrite(rel string, data []byte, appendTo bool) error {
	return u.C.AIWrite(rel, data, appendTo)
}
func (u Unreviewed) AIProvider(ctx context.Context, n string) (inventory.InventoryItem, map[string]string, error) {
	return u.C.AIProvider(ctx, n)
}
func (u Unreviewed) AIDropped(p, m string) []string { return u.C.AIDropped(p, m) }
func (u Unreviewed) AIDrop(p, m, param string)      { u.C.AIDrop(p, m, param) }

// Package bench implements ai.bench.run: a prompt set asked of a
// provider's model, each answer scored, and every call counted
// (docs/design/ai.md).
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnRun = "ai.bench.run"

// Parameters beside a call's.
const (
	paramPrompts = "prompts"
	paramSaveAs  = "save_as"
)

// Bounds.
const (
	maxPrompts     = 200
	maxRegexBytes  = 1024
	maxSavedAnswer = 4 << 10
)

var (
	now        = time.Now
	promptID   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	resultName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}\.json$`)
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRun,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportHTTPS},
			RequiredCapabilities: []capability.Name{capability.NameAIProvider},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Notes: "Each prompt is a counted call the provider has seen and billed; nothing on a device changes. A saved result is a new file under the project's ai/bench, never written over, which the run reports and does not remove.",
			},
			Doc: runDoc(),
		},
		Invoke: Run,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return run(ctx, rc, d, p, true)
		},
	})
}

// item is one prompt of the set: its id, what it sends, and its scorer.
type item struct {
	id       string
	messages []aiprovider.Message
	score    func(answer string) bool
}

// parseSet reads prompts: a list of {id, text | messages, expect}.
func parseSet(raw any) ([]item, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 || len(list) > maxPrompts {
		return nil, fmt.Errorf("%s is a list of 1 to %d {id, text or messages, expect}", paramPrompts, maxPrompts)
	}
	seen := map[string]bool{}
	out := make([]item, 0, len(list))
	for i, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", paramPrompts, i)
		}
		for k := range m {
			if k != "id" && k != "text" && k != "messages" && k != "expect" {
				return nil, fmt.Errorf("%s[%d]: %s is not id, text, messages or expect", paramPrompts, i, k)
			}
		}
		id, _ := m["id"].(string)
		if !promptID.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("%s[%d]: id %q is missing, not letters, digits, ., - and _, or used twice", paramPrompts, i, id)
		}
		seen[id] = true
		it := item{id: id}
		text, hasText := m["text"].(string)
		_, hasMessages := m["messages"]
		switch {
		case hasText == hasMessages:
			return nil, fmt.Errorf("prompt %s: give exactly one of text and messages", id)
		case hasText && text != "":
			it.messages = []aiprovider.Message{{Role: "user", Content: text}}
		case hasText:
			return nil, fmt.Errorf("prompt %s: text is empty", id)
		default:
			var err error
			if it.messages, err = aitask.ParseMessages(m["messages"]); err != nil {
				return nil, fmt.Errorf("prompt %s: %w", id, err)
			}
		}
		var err error
		if it.score, err = scorer(m["expect"]); err != nil {
			return nil, fmt.Errorf("prompt %s: expect: %w", id, err)
		}
		out = append(out, it)
	}
	return out, nil
}

// scorer reads expect: exactly one of exact, contains, regex (RE2) and
// json_field ({path, equals}).
func scorer(raw any) (func(string) bool, error) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("it is one of exact, contains, regex and json_field")
	}
	for kind, v := range m {
		switch kind {
		case "exact", "contains", "regex":
			want, ok := v.(string)
			if !ok || want == "" {
				return nil, fmt.Errorf("%s is non-empty text", kind)
			}
			switch kind {
			case "exact":
				return func(a string) bool { return strings.TrimSpace(a) == want }, nil
			case "contains":
				return func(a string) bool { return strings.Contains(a, want) }, nil
			}
			if len(want) > maxRegexBytes {
				return nil, fmt.Errorf("regex is over %d bytes", maxRegexBytes)
			}
			re, err := regexp.Compile(want)
			if err != nil {
				return nil, fmt.Errorf("regex: %w", err)
			}
			return re.MatchString, nil
		case "json_field":
			spec, ok := v.(map[string]any)
			path, _ := spec["path"].(string)
			equals, hasEquals := spec["equals"]
			if !ok || len(spec) != 2 || path == "" || !hasEquals {
				return nil, fmt.Errorf("json_field is {path, equals}")
			}
			want, err := json.Marshal(equals)
			if err != nil {
				return nil, fmt.Errorf("json_field equals: %w", err)
			}
			return func(a string) bool { return jsonField(a, path, string(want)) }, nil
		}
		return nil, fmt.Errorf("%s is not exact, contains, regex or json_field", kind)
	}
	return nil, fmt.Errorf("it is empty")
}

// jsonField reports whether answer, read as JSON, holds want (as JSON) at
// path, a dotted list of object keys.
func jsonField(answer, path, want string) bool {
	var v any
	if json.Unmarshal([]byte(answer), &v) != nil {
		return false
	}
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = obj[key]; !ok {
			return false
		}
	}
	got, err := json.Marshal(v)
	return err == nil && string(got) == want
}

// Run implements ai.bench.run.
func Run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, false)
}

func run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
	}
	model, err := sdk.RequiredStringParam(params, aitask.ParamModel)
	if err != nil {
		return fail(err)
	}
	set, err := parseSet(params[paramPrompts])
	if err != nil {
		return fail(err)
	}
	t, err := aitask.TargetFor(device, rc.InjectSecrets())
	if err != nil {
		return fail(err)
	}
	tag, err := aitask.ParseResidency(params)
	if err != nil {
		return fail(err)
	}
	if err := aitask.CheckResidency(tag, t.Name, t.Residency); err != nil {
		return fail(err)
	}
	o, err := aitask.ParseOptions(params)
	if err != nil {
		return fail(err)
	}
	sink, err := aitask.ParseSink(params)
	if err != nil {
		return fail(err)
	}
	saveAs := sdk.StringParam(params, paramSaveAs)
	if saveAs != "" && !resultName.MatchString(saveAs) {
		return fail(fmt.Errorf("%s %q is not a file name ending in .json (it is written under the project's ai/bench)", paramSaveAs, saveAs))
	}
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return fail(err)
	}
	if check {
		return checkSet(rc, t, model, set, o, sink != "" || saveAs != "")
	}
	if err := aitask.PrepareSink(rc, sink); err != nil {
		return fail(err)
	}
	if saveAs != "" {
		if err := aitask.HasProject(rc); err != nil {
			return fail(fmt.Errorf("%s: %w", paramSaveAs, err))
		}
	}
	results, saved, passed, total, priced, currency := make([]any, 0, len(set)), make([]any, 0, len(set)), 0, 0.0, true, ""
	for _, it := range set {
		p := aitask.Prompt{Messages: it.messages}
		resp, usage, callErr := aitask.Send(ctx, rc, t, model, p, o)
		rec := aitask.Record(fqcnRun, t, model, usage, p, callErr, now())
		if err := aitask.WriteSink(rc, sink, rec); err != nil {
			return fail(fmt.Errorf("prompt %s was asked, and its usage was not recorded: %w", it.id, err))
		}
		ok := callErr == nil && it.score(resp.Text)
		r := map[string]any{"id": it.id, "passed": ok, "input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "duration_ms": usage.Duration.Milliseconds()}
		if c, cur, has := aitask.Cost(t, model, usage); has {
			r["cost"], total, currency = c, total+c, cur
		} else {
			priced = false
		}
		if callErr != nil {
			r["error"] = callErr.Error()
		}
		if ok {
			passed++
		}
		results = append(results, r)
		withAnswer := map[string]any{"answer": bounded(resp.Text)}
		for k, v := range r {
			withAnswer[k] = v
		}
		saved = append(saved, withAnswer)
	}
	stats := map[string]any{
		"results": results, "passed": passed, "count": len(set), "score": float64(passed) / float64(len(set)),
		"provider": t.Name, "model": model,
	}
	if priced {
		stats["cost"], stats["currency"] = total, currency
	}
	if saveAs != "" {
		doc := map[string]any{"time": now().UTC().Format(time.RFC3339)}
		for k, v := range stats {
			doc[k] = v
		}
		doc["results"] = saved
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fail(err)
		}
		path := "ai/bench/" + saveAs
		if err := rc.(sdk.AIProject).AIWrite(path, append(data, '\n'), false); err != nil {
			return fail(fmt.Errorf("saving the results: %w", err))
		}
		stats["saved"] = path
	}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// checkSet is a check: the set is valid, the files can be written, and
// what the set would cost.
func checkSet(rc sdk.RunbookContext, t aitask.Target, model string, set []item, o aitask.Options, writes bool) (collection.Result, error) {
	if writes {
		if err := aitask.HasProject(rc); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
		}
	}
	tokens := 0
	estimates := make([]any, 0, len(set))
	for _, it := range set {
		est := aitask.Estimate(rc, t, model, aitask.Prompt{Messages: it.messages}, o)
		tokens += est["input_tokens"].(int)
		estimates = append(estimates, est)
	}
	stats := map[string]any{"count": len(set), "estimate": map[string]any{"input_tokens": tokens, "prompts": estimates}}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnRun, err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// bounded cuts a saved answer to maxSavedAnswer bytes, on a rune.
func bounded(s string) string {
	if len(s) <= maxSavedAnswer {
		return s
	}
	cut := maxSavedAnswer
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

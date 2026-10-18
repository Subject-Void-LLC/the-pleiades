// Package bench implements ai.bench.run: a prompt set asked of a
// provider's model, each answer scored, and every call counted
// (docs/design/ai.md).
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

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
		// Claim the file before the first call: a name already taken, or a
		// directory that cannot be written, is found before the set is
		// billed and not after, when the results could only be lost.
		if err := rc.(sdk.AIProject).AIWrite("ai/bench/"+saveAs, nil, false); err != nil {
			return fail(fmt.Errorf("%s %s: %w", paramSaveAs, saveAs, err))
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
		if err := rc.(sdk.AIProject).AIWrite(path, append(data, '\n'), true); err != nil {
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

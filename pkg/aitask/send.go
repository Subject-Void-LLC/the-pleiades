// Package aitask: making a call, the usage record it ends in, the sink the
// record goes to, and what a check estimates instead.
package aitask

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Request is the request a call sends.
func Request(model string, p Prompt, o Options) aiprovider.Request {
	return aiprovider.Request{Model: model, Messages: p.Messages, Params: o.Params, Schema: o.Schema}
}

// Send makes one call to t, holding it to aiprovider.Call's rules and to
// the run's memory of dropped parameters (sdk.AIProject; the task's own
// without one), and a structured answer to its schema. The usage record is
// returned with an error too.
func Send(ctx context.Context, rc sdk.RunbookContext, t Target, model string, p Prompt, o Options) (aiprovider.Response, aiprovider.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	drops := aiprovider.Drops{}
	proj, hasProject := rc.(sdk.AIProject)
	if hasProject {
		for _, d := range proj.AIDropped(t.Name, model) {
			drops[d] = true
		}
	}
	resp, usage, err := aiprovider.Call(ctx, t.Provider.Kind, t.Provider.Endpoint, Request(model, p, o), drops, aiprovider.CallOptions{Retries: o.Retries})
	if hasProject {
		for _, d := range usage.Dropped {
			proj.AIDrop(t.Name, model, d)
		}
	}
	if err != nil {
		return resp, usage, err
	}
	if err := o.ValidateAnswer(resp.Text); err != nil {
		return resp, usage, fmt.Errorf("the answer does not meet the schema: %w", err)
	}
	return resp, usage, nil
}

// Cost is what usage cost under t's prices: by the model the provider says
// answered, else by the model asked for; false when neither is priced.
func Cost(t Target, model string, u aiprovider.Usage) (float64, string, bool) {
	if c, cur, ok := t.Prices.Cost(u); ok {
		return c, cur, true
	}
	u.Model = model
	return t.Prices.Cost(u)
}

// Record is a call's usage record: when, which method, which provider and
// model, the tokens and time it took, its retries, the parameters ignored
// and dropped, its cost when priced, the prompt's verdict, and whether it
// succeeded. It holds nothing the call sent or received.
func Record(fqcn string, t Target, model string, u aiprovider.Usage, p Prompt, callErr error, now time.Time) map[string]any {
	rec := u.Map()
	rec["time"] = now.UTC().Format(time.RFC3339)
	rec["method"] = fqcn
	rec["provider"] = t.Name
	rec["residency"] = t.Residency
	rec["model"] = model
	rec["answered_by"] = u.Model
	rec["prompt"] = verdictMap(p.Verdict)
	if cost, currency, ok := Cost(t, model, u); ok {
		rec["cost"], rec["currency"] = cost, currency
	}
	rec["outcome"] = "ok"
	if callErr != nil {
		rec["outcome"] = "failed"
	}
	return rec
}

// verdictMap is a verdict as a usage record holds it.
func verdictMap(v promptsign.Verdict) map[string]any {
	if v.Name == "" {
		return map[string]any{"status": string(v.Status)}
	}
	return v.Map()
}

// PrepareSink proves the sink at path writable before any call is made,
// so a call is never made whose record could not be kept.
func PrepareSink(rc sdk.RunbookContext, path string) error {
	if path == "" {
		return nil
	}
	proj, ok := rc.(sdk.AIProject)
	if !ok {
		return fmt.Errorf("%s: %w", ParamUsageSink, sdk.ErrNoAIProject)
	}
	if err := proj.AIWrite(path, nil, true); err != nil {
		return fmt.Errorf("%s: %w", ParamUsageSink, err)
	}
	return nil
}

// WriteSink appends rec to the sink at path as one JSON line.
func WriteSink(rc sdk.RunbookContext, path string, rec map[string]any) error {
	if path == "" {
		return nil
	}
	proj, ok := rc.(sdk.AIProject)
	if !ok {
		return fmt.Errorf("%s: %w", ParamUsageSink, sdk.ErrNoAIProject)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return proj.AIWrite(path, append(line, '\n'), true)
}

// Estimate is what a check reports instead of a call: the tokens the
// prompt is estimated at (four characters a token), the most the answer
// may take (max_tokens, when set), the parameters the kind would ignore
// and the run has dropped, and the most it could cost when the model is
// priced; without max_tokens only the prompt's cost is known, and the
// estimate says the answer's is unbounded.
func Estimate(rc sdk.RunbookContext, t Target, model string, p Prompt, o Options) map[string]any {
	in := 0
	for _, m := range p.Messages {
		in += aiprovider.EstimateTokens(m.Content)
	}
	est := map[string]any{"input_tokens": in, "provider": t.Name, "model": model}
	var ignored, dropped []string
	run := map[string]bool{}
	if proj, ok := rc.(sdk.AIProject); ok {
		for _, d := range proj.AIDropped(t.Name, model) {
			run[d] = true
		}
	}
	for _, param := range Request(model, p, o).Set() {
		switch {
		case !t.Provider.Kind.Accepts(param):
			ignored = append(ignored, param)
		case run[param]:
			dropped = append(dropped, param)
		}
	}
	est["ignored"], est["dropped"] = list(ignored), list(dropped)
	price, priced := t.Prices[model]
	if o.Params.MaxTokens != nil {
		est["max_output_tokens"] = *o.Params.MaxTokens
	} else {
		est["output_unbounded"] = true
	}
	if priced {
		out := 0
		if o.Params.MaxTokens != nil {
			out = *o.Params.MaxTokens
		}
		est["max_cost"] = (float64(in)*price.Input + float64(out)*price.Output) / 1e6
		est["currency"] = price.Currency
	}
	return est
}

// list is a string list as a return value.
func list(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

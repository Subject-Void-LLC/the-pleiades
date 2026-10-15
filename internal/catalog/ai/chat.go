// Package ai implements ai.chat and ai.route: a language model called as a
// step of a run (docs/design/ai.md). ai.chat calls the provider the task
// targets; ai.route calls a use's providers in fallback order. Both hold
// every call to pkg/aitask's rules: residency before contact, a prompt
// bundle's signature under the use's policy, parameters a kind does not
// accept ignored and ones a provider rejects dropped, retries for what is
// retryable only, and a usage record for every call, which never holds
// what was sent or answered. A check makes no call: it validates and
// estimates.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods' names.
const (
	fqcnChat  = "ai.chat"
	fqcnRoute = "ai.route"
)

// callNotes is why a call cannot be undone, which every calling method
// shares.
const callNotes = "A call is made and cannot be unmade: the provider has seen what was sent and counted what it answered. Nothing on a device changes, so there is nothing a later task could put back."

// now is the clock signatures and records are read against.
var now = time.Now

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnChat,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportHTTPS},
			RequiredCapabilities: []capability.Name{capability.NameAIProvider},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility:        collection.Reversibility{Notes: callNotes},
			Doc:                  chatDoc(),
		},
		Invoke: Chat,
		Check:  checkChat,
	})
}

// Chat implements ai.chat.
func Chat(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return chat(ctx, rc, device, params, false)
}

// checkChat is ai.chat's check: everything up to the call, and an
// estimate in its place.
func checkChat(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return chat(ctx, rc, device, params, true)
}

// chat reads the task, refuses what the use does not admit, and makes the
// call, or in a check estimates it.
func chat(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnChat, err)
	}
	model, err := sdk.RequiredStringParam(params, aitask.ParamModel)
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
	sink, err := aitask.ParseSink(params)
	if err != nil {
		return fail(err)
	}
	p, err := aitask.ParsePrompt(rc, params, model, now())
	if err != nil {
		return fail(err)
	}
	o, err := aitask.ParseOptions(p.Params)
	if err != nil {
		return fail(err)
	}
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return fail(err)
	}
	gate, err := reviewGate(rc, fqcnChat, t.Name, p, params, nil)
	if err != nil {
		return fail(err)
	}
	if check {
		if sink != "" {
			if err := aitask.HasProject(rc); err != nil {
				return fail(fmt.Errorf("%s: %w", aitask.ParamUsageSink, err))
			}
		}
		if err := setStats(rc, map[string]any{"estimate": aitask.Estimate(rc, t, model, p, o), "prompt": verdict(p), "review": gate.Peek()}); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, nil
	}
	// A request already decided is answered from its review, with no
	// call: asking again after a person's decision never bills twice.
	if decided, err := gate.Before(); err != nil || decided != nil {
		if err != nil {
			return fail(err)
		}
		return released(rc, fail, gate, decided, o, p)
	}
	if err := aitask.PrepareSink(rc, sink); err != nil {
		return fail(err)
	}
	if err := aitask.Commit(rc, p); err != nil {
		return fail(fmt.Errorf("recording the prompt's version: %w", err))
	}
	resp, usage, callErr := aitask.Send(ctx, rc, t, model, p, o)
	rec := aitask.Record(fqcnChat, t, model, usage, p, callErr, now())
	if err := aitask.WriteSink(rc, sink, rec); err != nil {
		if callErr != nil {
			return fail(fmt.Errorf("%w; and its usage was not recorded: %v", callErr, err))
		}
		return fail(fmt.Errorf("the call was made, and its usage was not recorded: %w", err))
	}
	if callErr != nil {
		return fail(callErr)
	}
	// The answer is held here, before any stat a later task could read is
	// set, when it crosses one of the review's thresholds.
	approved, err := gate.After(ctx, sdk.Review{Method: fqcnChat, Provider: t.Name, Model: model, Answer: resp.Text, Usage: rec}, outcome(t, model, resp, usage, p, false))
	if err != nil {
		return fail(err)
	}
	stats := answerStats(resp, o, rec, p)
	stats["review"] = aitask.Stat(gate, approved)
	if err := setStats(rc, stats); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

// reviewGate is the request's review: the task's rules added to the use's
// (extra), keyed by the request as asked.
func reviewGate(rc sdk.RunbookContext, fqcn, target string, p aitask.Prompt, params map[string]any, extra []aitask.Rule) (aitask.Gate, error) {
	rules, err := aitask.ParseReview(params[aitask.ParamReview])
	if err != nil {
		return aitask.Gate{}, err
	}
	rules = append(append([]aitask.Rule(nil), extra...), rules...)
	if len(rules) == 0 {
		return aitask.Gate{}, nil
	}
	bundle := ""
	if p.Bundle != nil {
		bundle = p.Bundle.Hash
	}
	key, err := aitask.RequestKey(fqcn, target, bundle, params)
	if err != nil {
		return aitask.Gate{}, err
	}
	return aitask.NewGate(rc, rules, key), nil
}

// outcome is what a call came to, as review rules read it.
func outcome(t aitask.Target, model string, resp aiprovider.Response, usage aiprovider.Usage, p aitask.Prompt, fallback bool) aitask.Outcome {
	cost, _, priced := aitask.Cost(t, model, usage)
	return aitask.Outcome{
		Answer: resp.Text, StopReason: resp.StopReason, OutputTokens: usage.OutputTokens, Cost: cost, Priced: priced,
		Verified: p.Verdict.Status == promptsign.Verified, Fallback: fallback,
	}
}

// released returns an answer a person approved earlier, from its review,
// with the usage record of the call that made it. Nothing is called.
func released(rc sdk.RunbookContext, fail func(error) (collection.Result, error), gate aitask.Gate, r *sdk.Review, o aitask.Options, p aitask.Prompt) (collection.Result, error) {
	if err := o.ValidateAnswer(r.Answer); err != nil {
		return fail(fmt.Errorf("the approved answer does not meet the schema: %w", err))
	}
	stats := answerStats(aiprovider.Response{Text: r.Answer}, o, r.Usage, p)
	stats["review"], stats["reused"] = aitask.Stat(gate, r), true
	if err := setStats(rc, stats); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

// answerStats are what a call returns: the answer (and its JSON value
// when a schema asked for one), why it stopped, the usage record, the
// prompt's verdict, and the parameters ignored and dropped.
func answerStats(resp aiprovider.Response, o aitask.Options, rec map[string]any, p aitask.Prompt) map[string]any {
	stats := map[string]any{
		"answer": resp.Text, "stop_reason": resp.StopReason, "usage": rec, "prompt": verdict(p),
		"ignored": rec["ignored"], "dropped": rec["dropped"],
	}
	if len(o.Schema) > 0 {
		var v any
		if json.Unmarshal([]byte(resp.Text), &v) == nil {
			stats["json"] = v
		}
	}
	return stats
}

// verdict is the prompt's verdict as a return value.
func verdict(p aitask.Prompt) map[string]any {
	if p.Bundle == nil {
		return map[string]any{"status": string(p.Verdict.Status)}
	}
	return p.Verdict.Map()
}

// setStats sets each stat.
func setStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}

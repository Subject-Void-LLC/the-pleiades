// Package aitask: one call to one provider, made the way ai.chat makes it
// and shared by every method that asks a single provider: what it sends,
// the review it answers to, and the call with its usage record.
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

// Call is one call to one provider, read and ready: Prepare builds it and
// Run makes it.
type Call struct {
	// FQCN is the method making the call, as its usage record names it.
	FQCN    string
	Target  Target
	Model   string
	Prompt  Prompt
	Options Options
	Gate    Gate
	// Sink is the usage sink's path in the project, "" for none.
	Sink string

	now func() time.Time
}

// Prepare reads a call to t from params: the residency it is tagged with,
// its usage sink, what it sends (holding a bundle to its signing policy),
// its options and its review, and records t's warnings. It refuses, before
// any contact, a use tagged local sent to a hosted provider. Nothing is
// sent or written.
func Prepare(rc sdk.RunbookContext, fqcn string, t Target, model string, params map[string]any, now func() time.Time) (Call, error) {
	tag, err := ParseResidency(params)
	if err != nil {
		return Call{}, err
	}
	if err := CheckResidency(tag, t.Name, t.Residency); err != nil {
		return Call{}, err
	}
	sink, err := ParseSink(params)
	if err != nil {
		return Call{}, err
	}
	p, err := ParsePrompt(rc, params, model, now())
	if err != nil {
		return Call{}, err
	}
	o, err := ParseOptions(p.Params)
	if err != nil {
		return Call{}, err
	}
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return Call{}, err
	}
	gate, err := GateFor(rc, fqcn, t.Name, p, params, nil)
	if err != nil {
		return Call{}, err
	}
	return Call{FQCN: fqcn, Target: t, Model: model, Prompt: p, Options: o, Gate: gate, Sink: sink, now: now}, nil
}

// Answer is what a call came to.
type Answer struct {
	Response aiprovider.Response
	// Record is the usage record: this call's, or for a reused answer the
	// call's that made it.
	Record map[string]any
	// Approved is a person's approval of the answer, nil when no review
	// rule held it.
	Approved *sdk.Review
	// Reused is whether the answer came from a decision already on
	// record, with no call.
	Reused bool
}

// Run makes the call, or answers it from a person's decision already on
// record for this very request with no second call. The usage record is
// appended to the sink before the answer is looked at; an answer crossing
// a review threshold is held, and Run fails as held, denied, or with
// nobody to ask.
func (c Call) Run(ctx context.Context, rc sdk.RunbookContext) (Answer, error) {
	if decided, err := c.Gate.Before(); err != nil || decided != nil {
		if err != nil {
			return Answer{}, err
		}
		if err := c.Options.ValidateAnswer(decided.Answer); err != nil {
			return Answer{}, fmt.Errorf("the approved answer does not meet the schema: %w", err)
		}
		return Answer{Response: aiprovider.Response{Text: decided.Answer}, Record: decided.Usage, Approved: decided, Reused: true}, nil
	}
	if err := PrepareSink(rc, c.Sink); err != nil {
		return Answer{}, err
	}
	if err := Commit(rc, c.Prompt); err != nil {
		return Answer{}, fmt.Errorf("recording the prompt's version: %w", err)
	}
	resp, usage, callErr := Send(ctx, rc, c.Target, c.Model, c.Prompt, c.Options)
	rec := Record(c.FQCN, c.Target, c.Model, usage, c.Prompt, callErr, c.now())
	if err := WriteSink(rc, c.Sink, rec); err != nil {
		if callErr != nil {
			return Answer{}, fmt.Errorf("%w; and its usage was not recorded: %v", callErr, err)
		}
		return Answer{}, fmt.Errorf("the call was made, and its usage was not recorded: %w", err)
	}
	if callErr != nil {
		return Answer{}, callErr
	}
	approved, err := c.Gate.After(ctx, sdk.Review{Method: c.FQCN, Provider: c.Target.Name, Model: c.Model, Answer: resp.Text, Usage: rec},
		OutcomeOf(c.Target, c.Model, resp, usage, c.Prompt, false))
	if err != nil {
		return Answer{}, err
	}
	return Answer{Response: resp, Record: rec, Approved: approved}, nil
}

// Stats are what a released answer returns as a method's stats: the
// answer, its JSON value when a schema asked for one, why it stopped, the
// usage record, the prompt's verdict, the parameters ignored and dropped,
// the review, and whether it was reused.
func (c Call) Stats(a Answer) map[string]any {
	stats := AnswerStats(a.Response, c.Options, a.Record, c.Prompt)
	stats["review"] = Stat(c.Gate, a.Approved)
	if a.Reused {
		stats["reused"] = true
	}
	return stats
}

// Estimate is what a check of the call reports in its place.
func (c Call) Estimate(rc sdk.RunbookContext) map[string]any {
	return Estimate(rc, c.Target, c.Model, c.Prompt, c.Options)
}

// GateFor is a request's review: rules from params added to extra (a
// use's, which a task can add to and never take from), keyed by the
// request as asked. With no rules it is a gate that holds nothing.
func GateFor(rc sdk.RunbookContext, fqcn, target string, p Prompt, params map[string]any, extra []Rule) (Gate, error) {
	rules, err := ParseReview(params[ParamReview])
	if err != nil {
		return Gate{}, err
	}
	rules = append(append([]Rule(nil), extra...), rules...)
	if len(rules) == 0 {
		return Gate{}, nil
	}
	bundle := ""
	if p.Bundle != nil {
		bundle = p.Bundle.Hash
	}
	key, err := RequestKey(fqcn, target, bundle, params)
	if err != nil {
		return Gate{}, err
	}
	return NewGate(rc, rules, key), nil
}

// OutcomeOf is what a call came to, as review rules read it.
func OutcomeOf(t Target, model string, resp aiprovider.Response, usage aiprovider.Usage, p Prompt, fallback bool) Outcome {
	cost, _, priced := Cost(t, model, usage)
	return Outcome{
		Answer: resp.Text, StopReason: resp.StopReason, OutputTokens: usage.OutputTokens, Reported: usage.Reported, Cost: cost, Priced: priced,
		Verified: p.Verdict.Status == promptsign.Verified, Fallback: fallback,
	}
}

// AnswerStats are what a call returns: the answer (and its JSON value
// when a schema asked for one), why it stopped, the usage record, the
// prompt's verdict, and the parameters ignored and dropped.
func AnswerStats(resp aiprovider.Response, o Options, rec map[string]any, p Prompt) map[string]any {
	stats := map[string]any{
		"answer": resp.Text, "stop_reason": resp.StopReason, "usage": rec, "prompt": p.VerdictStat(),
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

// VerdictStat is the prompt's verdict as a return value.
func (p Prompt) VerdictStat() map[string]any {
	if p.Bundle == nil {
		return map[string]any{"status": string(p.Verdict.Status)}
	}
	return p.Verdict.Map()
}

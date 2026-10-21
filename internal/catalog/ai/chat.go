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
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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
	call, err := aitask.Prepare(rc, fqcnChat, t, model, params, now)
	if err != nil {
		return fail(err)
	}
	if check {
		if call.Sink != "" {
			if err := aitask.HasProject(rc); err != nil {
				return fail(fmt.Errorf("%s: %w", aitask.ParamUsageSink, err))
			}
		}
		if err := setStats(rc, map[string]any{"estimate": call.Estimate(rc), "prompt": call.Prompt.VerdictStat(), "review": call.Gate.Peek()}); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, nil
	}
	// A request already decided is answered from its review, with no call,
	// and an answer crossing a review threshold is held here, before any
	// stat a later task could read is set.
	answer, err := call.Run(ctx, rc)
	if err != nil {
		return fail(err)
	}
	if err := setStats(rc, call.Stats(answer)); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

// released returns an answer a person approved earlier, from its review,
// with the usage record of the call that made it. Nothing is called.
func released(rc sdk.RunbookContext, fail func(error) (collection.Result, error), gate aitask.Gate, r *sdk.Review, o aitask.Options, p aitask.Prompt) (collection.Result, error) {
	if err := o.ValidateAnswer(r.Answer); err != nil {
		return fail(fmt.Errorf("the approved answer does not meet the schema: %w", err))
	}
	stats := aitask.AnswerStats(aiprovider.Response{Text: r.Answer}, o, r.Usage, p)
	stats["review"], stats["reused"] = aitask.Stat(gate, r), true
	if err := setStats(rc, stats); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
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

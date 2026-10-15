// ai.route: a call made through a use's routing table, falling back
// through its providers in order.
package ai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRoute,
		Manifest: collection.Manifest{
			// It reaches the providers its table names, not a target, so it
			// declares no transport or capability of its own; each provider
			// is held to AIProviderCapable as it is resolved.
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility:        collection.Reversibility{Notes: callNotes},
			Doc:                  routeDoc(),
		},
		Invoke: Route,
		Check:  checkRoute,
	})
}

// routed is one provider of a use, resolved.
type routed struct {
	step   aitask.Step
	target aitask.Target
}

// Route implements ai.route.
func Route(ctx context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return route(ctx, rc, params, false)
}

// checkRoute is ai.route's check: the table, every provider's residency
// and key resolved, and an estimate for each, with no call.
func checkRoute(ctx context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return route(ctx, rc, params, true)
}

func route(ctx context.Context, rc sdk.RunbookContext, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRoute, err)
	}
	use, steps, err := resolveRoute(ctx, rc, params)
	if err != nil {
		return fail(err)
	}
	sink, err := aitask.ParseSink(params)
	if err != nil {
		return fail(err)
	}
	// The use decides the signing policy; the prompt is read once per
	// provider because a bundle may hold a variant per model.
	withPolicy := make(map[string]any, len(params)+1)
	for k, v := range params {
		withPolicy[k] = v
	}
	withPolicy[aitask.ParamSigning] = string(use.Signing)
	prompts := make([]aitask.Prompt, len(steps))
	var opts aitask.Options
	for i, s := range steps {
		if prompts[i], err = aitask.ParsePrompt(rc, withPolicy, s.step.Model, now()); err != nil {
			return fail(err)
		}
		if b := prompts[i].Bundle; b != nil && b.Use != use.Name {
			return fail(fmt.Errorf("the prompt bundle is for use %s, not %s", b.Use, use.Name))
		}
		if opts, err = aitask.ParseOptions(prompts[i].Params); err != nil {
			return fail(err)
		}
		if err := sdk.RecordWarnings(rc, s.target.Provider.Warnings); err != nil {
			return fail(err)
		}
	}
	if check {
		if sink != "" {
			if err := aitask.HasProject(rc); err != nil {
				return fail(fmt.Errorf("%s: %w", aitask.ParamUsageSink, err))
			}
		}
		estimates := make([]any, len(steps))
		for i, s := range steps {
			estimates[i] = aitask.Estimate(rc, s.target, s.step.Model, prompts[i], opts)
		}
		if err := setStats(rc, map[string]any{"estimate": estimates, "prompt": verdict(prompts[0]), "use": use.Name}); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, nil
	}
	if err := aitask.PrepareSink(rc, sink); err != nil {
		return fail(err)
	}
	if err := aitask.Commit(rc, prompts[0]); err != nil {
		return fail(fmt.Errorf("recording the prompt's version: %w", err))
	}
	var attempts []any
	var failures []string
	for i, s := range steps {
		resp, usage, callErr := aitask.Send(ctx, rc, s.target, s.step.Model, prompts[i], opts)
		rec := aitask.Record(fqcnRoute, s.target, s.step.Model, usage, prompts[i], callErr, now())
		if err := aitask.WriteSink(rc, sink, rec); err != nil {
			return fail(fmt.Errorf("a call to %s was made, and its usage was not recorded: %w", s.step.Provider, err))
		}
		attempt := map[string]any{"provider": s.step.Provider, "model": s.step.Model, "outcome": rec["outcome"]}
		if callErr == nil {
			attempts = append(attempts, attempt)
			stats := answerStats(resp, opts, rec, prompts[i])
			stats["provider"], stats["model"], stats["use"], stats["attempts"] = s.step.Provider, s.step.Model, use.Name, attempts
			if err := setStats(rc, stats); err != nil {
				return fail(err)
			}
			return collection.Result{Changed: true}, nil
		}
		attempt["error"] = callErr.Error()
		attempts = append(attempts, attempt)
		failures = append(failures, fmt.Sprintf("%s (%s): %v", s.step.Provider, s.step.Model, callErr))
		if !fallsBack(ctx, callErr) {
			return fail(fmt.Errorf("%s (%s) failed in a way the next provider would not mend: %w", s.step.Provider, s.step.Model, callErr))
		}
	}
	return fail(fmt.Errorf("use %s: every provider failed: %s", use.Name, strings.Join(failures, "; ")))
}

// resolveRoute reads the use and its table, refuses what the use decides
// being set on the task, and resolves every provider through the project,
// refusing the route as a whole, before any call, when one is not an
// onboarded provider, has no key its kind needs, or would take the use's
// data somewhere its residency forbids.
func resolveRoute(ctx context.Context, rc sdk.RunbookContext, params map[string]any) (aitask.Use, []routed, error) {
	name, err := sdk.RequiredStringParam(params, aitask.ParamUse)
	if err != nil {
		return aitask.Use{}, nil, err
	}
	table, err := aitask.ParseRoutes(params[aitask.ParamRoutes])
	if err != nil {
		return aitask.Use{}, nil, err
	}
	use, ok := table[name]
	if !ok {
		known := make([]string, 0, len(table))
		for k := range table {
			known = append(known, k)
		}
		sort.Strings(known)
		return aitask.Use{}, nil, fmt.Errorf("%s %s is not in the routing table (%s)", aitask.ParamUse, name, strings.Join(known, ", "))
	}
	for _, k := range []string{aitask.ParamModel, aitask.ParamSigning, aitask.ParamResidency} {
		if v, set := params[k]; set && v != nil {
			return aitask.Use{}, nil, fmt.Errorf("%s comes from the routing table's use, not the task", k)
		}
	}
	proj, ok := rc.(sdk.AIProject)
	if !ok {
		return aitask.Use{}, nil, sdk.ErrNoAIProject
	}
	steps := make([]routed, 0, len(use.Steps))
	for _, s := range use.Steps {
		item, secrets, err := proj.AIProvider(ctx, s.Provider)
		if err != nil {
			return aitask.Use{}, nil, err
		}
		t, err := aitask.TargetFor(item, secrets)
		if err != nil {
			return aitask.Use{}, nil, err
		}
		if err := aitask.CheckResidency(use.Residency, t.Name, t.Residency); err != nil {
			return aitask.Use{}, nil, fmt.Errorf("use %s: %w", use.Name, err)
		}
		steps = append(steps, routed{step: s, target: t})
	}
	return use, steps, nil
}

// fallsBack reports whether a failed call may move on to the next
// provider: this provider could not be reached, or failed in a way only
// time mends and its retries ran out, before any answer streamed. A
// refusal of the request itself (a 400, a refused key, a schema the
// answer missed) does not fall back: the next provider would be asked the
// same wrong thing, or would hide a broken credential.
func fallsBack(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var apiErr *aiprovider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	return errors.Is(err, context.DeadlineExceeded)
}

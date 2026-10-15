// ai.provider.probe: one counted call that shows a provider answers.
package provider

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnProbe,
		Manifest: manifest(probeDoc(), collection.Reversibility{
			Notes: "A probe is a counted call: the provider has seen it and billed it, and nothing on a device changed that could be put back.",
		}),
		Invoke: Probe,
		Check:  checkProbe,
	})
}

// Probe implements ai.provider.probe.
func Probe(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return probe(ctx, rc, device, params, false)
}

// checkProbe reports the call a probe would make, without it.
func checkProbe(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return probe(ctx, rc, device, params, true)
}

func probe(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnProbe, err)
	}
	model, err := sdk.RequiredStringParam(params, aitask.ParamModel)
	if err != nil {
		return fail(err)
	}
	t, err := aitask.TargetFor(device, rc.InjectSecrets())
	if err != nil {
		return fail(err)
	}
	sink, err := aitask.ParseSink(params)
	if err != nil {
		return fail(err)
	}
	o, err := aitask.ParseOptions(map[string]any{aitask.ParamTimeout: params[aitask.ParamTimeout], aitask.ParamMaxTokens: 8, aitask.ParamTemperature: 0})
	if err != nil {
		return fail(err)
	}
	o.Retries = 0
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return fail(err)
	}
	p := aitask.Prompt{Messages: []aiprovider.Message{{Role: "user", Content: probeMessage}}}
	if check {
		if sink != "" {
			if err := aitask.HasProject(rc); err != nil {
				return fail(fmt.Errorf("%s: %w", aitask.ParamUsageSink, err))
			}
		}
		if err := rc.SetStat("estimate", aitask.Estimate(rc, t, model, p, o)); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, nil
	}
	if err := aitask.PrepareSink(rc, sink); err != nil {
		return fail(err)
	}
	resp, usage, callErr := aitask.Send(ctx, rc, t, model, p, o)
	rec := aitask.Record(fqcnProbe, t, model, usage, p, callErr, now())
	if err := aitask.WriteSink(rc, sink, rec); err != nil {
		return fail(fmt.Errorf("the probe was made, and its usage was not recorded: %w", err))
	}
	if callErr != nil {
		return fail(callErr)
	}
	for k, v := range map[string]any{"usage": rec, "latency_ms": usage.Duration.Milliseconds(), "answered_by": resp.Model} {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	return collection.Result{Changed: true}, nil
}

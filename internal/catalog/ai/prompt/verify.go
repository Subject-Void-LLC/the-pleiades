// Package prompt implements ai.prompt.verify: a signed document (a prompt
// bundle, a parameter set or a routing table) checked against the
// project's trusted keys, as a call would check it, without making one
// (docs/design/ai.md). Signing is the CLI's (pleiades ai prompt sign),
// never a runbook's: it needs a private key and a person.
package prompt

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnVerify = "ai.prompt.verify"

var now = time.Now

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnVerify,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true, Notes: "It reads the document, its signature, the trusted keys and the version register, and records nothing: a version is recorded as used only by a call that sends it.",
			},
			Doc: verifyDoc(),
		},
		Invoke: Verify,
		// It only reads, so a check of it is the task itself.
		Check: Verify,
	})
}

// Verify implements ai.prompt.verify.
func Verify(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	raw := sdk.StringParam(params, aitask.ParamSigning)
	if raw == "" {
		raw = string(promptsign.Required)
	}
	policy, err := promptsign.ParsePolicy(raw)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", aitask.ParamSigning, err))
	}
	doc, sig, err := aitask.LoadBundle(rc, params)
	if err != nil {
		return fail(err)
	}
	if use := sdk.StringParam(params, aitask.ParamUse); use != "" && use != doc.Use {
		return fail(fmt.Errorf("the %s is for use %s, not %s", doc.Kind, doc.Use, use))
	}
	v, err := aitask.Verify(rc, doc, sig, policy, now())
	if err != nil {
		return fail(err)
	}
	stats := v.Map()
	stats["kind"], stats["use"] = doc.Kind, doc.Use
	for k, val := range stats {
		if err := rc.SetStat(k, val); err != nil {
			return fail(err)
		}
	}
	return collection.Result{}, nil
}

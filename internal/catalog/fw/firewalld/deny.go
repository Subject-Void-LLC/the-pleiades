package firewalld

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "fw.firewalld.deny",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameFirewalld},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It queries firewalld before it acts, so a check can predict through the same code (CheckDeny).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed the rule from the permanent configuration, the runtime one, or both " +
					"emits an fw.firewalld.allow naming the same port or service and zone, with permanent and " +
					"immediate set to match exactly which half this run actually changed. A run that found " +
					"everything already denied emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "fw.firewalld.allow", Record: []string{"port", "protocol", "service", "zone", "permanent", "immediate"}},
				},
			},
			Doc: denyDoc(),
		},
		Invoke: Deny,
		Check:  CheckDeny,
	})
}

func denyDoc() collection.Doc {
	return collection.Doc{
		Summary: "Closes a port or service in firewalld.",
		Description: "Makes sure a port or service is not allowed through firewalld in the given zone. This " +
			"is close to community.general.firewalld with state=disabled, except permanent and immediate are " +
			"decided independently rather than as one of firewalld's own permanent/runtime toggle: either can " +
			"be true without the other. Both configurations are read before anything is sent, so a half " +
			"already denying the rule is left alone and reports no change from that half.",
		Params: []collection.Param{
			{Name: paramPort, Type: "int", Description: "The port to deny. Exactly one of port or service is required."},
			{Name: paramProtocol, Type: "string", Default: "tcp", Description: "The protocol for port (tcp or udp). Ignored when service is given."},
			{Name: paramService, Type: "string", Description: "The firewalld service name to deny, e.g. http. Exactly one of port or service is required."},
			{Name: paramZone, Type: "string", Default: "public", Description: "The firewalld zone to remove the rule from."},
			{Name: paramPermanent, Type: "bool", Default: "true", Description: "Also remove the rule from the permanent configuration."},
			{Name: paramImmediate, Type: "bool", Default: "true", Description: "Also remove the rule from the runtime configuration, so it takes effect now."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statZone, Type: "string", Returned: "always", Description: "The zone this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "Whether the rule was allowed in the permanent configuration and in the runtime one, before this task and after it. Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Close a port",
				RunbookYAML: "- name: Deny telnet\n  fw.firewalld.deny:\n    port: 23\n",
			},
		},
		SeeAlso: []string{"fw.firewalld.allow", "fw.firewalld.reload"},
	}
}

// Deny implements "fw.firewalld.deny".
func Deny(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return deny(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDeny is fw.firewalld.deny's check: the same queries and decision as Deny (ruleChanges), through the one body both share, then a prediction instead of firewall-cmd's add or remove.
func CheckDeny(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return deny(ctx, rc, device, params, collection.ModeCheck)
}

// deny is Deny's and CheckDeny's one body; mode says which.
func deny(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "fw.firewalld.deny"

	tgt, err := parseTarget(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	zone := zoneParam(params)
	permanent, err := sdk.BoolParamOr(params, paramPermanent, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramPermanent, err)
	}
	immediate, err := sdk.BoolParamOr(params, paramImmediate, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramImmediate, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryRuleState(ctx, conn, zone, tgt)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		result, err := checkRule(rc, zone, before, permanent, immediate, false)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return result, nil
	}

	permanentChanged, runtimeChanged, err := convergeRule(ctx, conn, zone, tgt, before, permanent, immediate, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	changed := permanentChanged || runtimeChanged

	after := before
	if changed {
		if after, err = queryRuleState(ctx, conn, zone, tgt); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordRuleState(rc, zone, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		invParams := tgt.Map()
		invParams[paramZone] = zone
		invParams[paramPermanent] = permanentChanged
		invParams[paramImmediate] = runtimeChanged
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "fw.firewalld.allow",
			Params: invParams,
			Description: fmt.Sprintf("Re-add the rule this task removed from zone %s, in exactly the "+
				"configuration(s) it removed it from.", zone),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

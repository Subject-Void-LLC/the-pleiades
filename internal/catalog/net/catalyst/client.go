// Package catalyst implements the net.catalyst.* namespaced Collection
// methods: read-only fact gatherers over a Cisco Catalyst Center.
//
// Every method here is controller-side and read-only. They run on the
// runner and address the controller over its REST API rather than
// connecting to any device, and none of them changes anything: the whole
// namespace exists to answer questions about a fleet, which is why every
// one of them reports Changed false and why they can be verified against
// Cisco's read-only DevNet sandbox at all.
//
// This file holds what they share: building an authenticated client for the
// controller a task targets, and the parameter parsing that goes with it.
//
// Like every generated Collection package it imports only pkg/, which is
// why the REST client lives in pkg/catalystcenter rather than under
// internal/. That constraint is not incidental here: it is the same one a
// third-party Collection will have to satisfy once Part X's OCI
// distribution exists, so a built-in that quietly reached into internal/
// would be proving a pattern nobody else can follow.
package catalyst

import (
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/catalystcenter"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/sdk"
)

// Secret keys these methods read from the RunbookContext.
//
// Credentials arrive through InjectSecrets rather than through params,
// because params come from the runbook file and a runbook file is committed
// to version control. Section 6c's rule that discovery credentials are
// looked up rather than inlined applies just as much to a fact gatherer.
const (
	secretUsername = "username"
	secretPassword = "password"
)

// paramInsecureSkipVerify opts out of TLS verification for one task, for
// the appliance-with-a-wrong-certificate case (the DevNet sandbox itself is
// one: its certificate is issued for a Kong ingress, not for its hostname).
const paramInsecureSkipVerify = "insecure_skip_verify"

// clientForDevice builds an authenticated Catalyst Center client for the
// controller a task targets.
//
// It reads the base URL from the device's own CatalystAPICapable accessor
// rather than from a task parameter, which is what makes these methods
// retargetable: the same runbook runs against a lab controller and a
// production one by changing which device it targets, with nothing in the
// runbook file to edit.
func clientForDevice(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (*catalystcenter.Client, error) {
	if device == nil {
		return nil, fmt.Errorf("no target device: net.catalyst methods address a specific Catalyst Center, set the task's target")
	}
	if !device.HasCapability(capability.NameCatalystAPI) {
		return nil, fmt.Errorf("device %q does not have %s", device.Name(), capability.NameCatalystAPI)
	}

	addressable, ok := device.(capability.CatalystAPICapable)
	if !ok {
		// HasCapability already ANDs the structural assertion, so this is
		// unreachable through a correctly built device type. It is here
		// because the alternative to an explicit refusal is a panic.
		return nil, fmt.Errorf("device %q declares %s but does not implement it", device.Name(), capability.NameCatalystAPI)
	}

	baseURL := addressable.CatalystBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("device %q has no Catalyst Center base URL", device.Name())
	}

	secrets := rc.InjectSecrets()
	username := secrets[secretUsername]
	if username == "" {
		return nil, fmt.Errorf("no %q secret available for device %q", secretUsername, device.Name())
	}

	var opts []catalystcenter.Option
	if boolParam(params, paramInsecureSkipVerify) {
		opts = append(opts, catalystcenter.WithInsecureSkipVerify(true))
	}

	return catalystcenter.New(baseURL, username, secrets[secretPassword], opts...)
}

// boolParam reads a boolean task parameter, treating a missing or
// non-boolean value as false.
//
// It accepts a real bool only, not the string "true". YAML already decodes
// an unquoted true into a bool, and accepting the string form would mean
// silently honoring a quoted "false" as true-ish somewhere down the line.
func boolParam(params map[string]any, key string) bool {
	v, ok := params[key].(bool)
	return ok && v
}

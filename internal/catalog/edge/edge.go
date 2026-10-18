// Package edge implements edge.tunnel, edge.access and edge.dns: whether
// what a Cloudflare account serves is what the operator meant it to be
// (docs/design/edge.md). Each reads through pkg/cfapi with the account
// device's own token, compares what it read with what the task expects,
// and fails listing every difference. None writes, so a check of each is
// the task itself.
package edge

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods' names.
const (
	fqcnTunnel = "edge.tunnel"
	fqcnAccess = "edge.access"
	fqcnDNS    = "edge.dns"
)

// paramExpect is what every method compares against.
const paramExpect = "expect"

// statDifferences is every method's list of what differed.
const statDifferences = "differences"

// readOnly is what every method declares of its effect.
var readOnly = collection.Reversibility{ReadOnly: true, Notes: "It reads the account through Cloudflare's API and changes nothing."}

// manifest is what every method declares.
func manifest(doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{capability.TransportHTTPS},
		RequiredCapabilities: []capability.Name{capability.NameCloudflareAPI},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		SupportsCheck:        true,
		Reversibility:        readOnly,
		Doc:                  doc,
	}
}

// method registers a read: its check is the task itself.
func method(name string, doc collection.Doc, fn collection.Method) {
	collection.MustRegister(collection.Descriptor{Name: name, Manifest: manifest(doc), Invoke: fn, Check: fn})
}

// account resolves device into its client and account id, and records
// what its TLS settings make a run say.
func account(rc sdk.RunbookContext, device inventory.InventoryItem) (*cfapi.Client, string, error) {
	if device == nil {
		return nil, "", fmt.Errorf("this task has no Cloudflare account to read")
	}
	dev, ok := device.(capability.CloudflareAPICapable)
	if !ok {
		return nil, "", fmt.Errorf("%s is not a Cloudflare account (a cloudflare_account device)", device.Name())
	}
	c, warnings, err := cfapi.ClientFor(device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return nil, "", err
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return nil, "", err
	}
	return c, dev.CloudflareAccountID(), nil
}

// Bounds on how a difference is told: each one's length, and how many an
// error names.
const (
	maxDifference       = 512
	maxNamedDifferences = 20
)

// finish sets the stats, then fails naming the differences (the first
// maxNamedDifferences of them), or succeeds when there is none.
func finish(rc sdk.RunbookContext, fqcn string, stats map[string]any, diffs []string) (collection.Result, error) {
	// A difference quotes what the API answered (record contents, names), so
	// each is cut and escaped, and the error names the first few of them.
	list := make([]any, len(diffs))
	for i, d := range diffs {
		diffs[i] = remoteexec.Excerpt(d, maxDifference)
		list[i] = diffs[i]
	}
	stats[statDifferences] = list
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if len(diffs) > 0 {
		named := diffs
		if len(named) > maxNamedDifferences {
			named = append(slices.Clone(diffs[:maxNamedDifferences]), fmt.Sprintf("and %d more (the differences return value holds them all)", len(diffs)-maxNamedDifferences))
		}
		return collection.Result{}, fmt.Errorf("%s: %d difference(s) from what was expected: %s", fqcn, len(diffs), strings.Join(named, "; "))
	}
	return collection.Result{}, nil
}

// expectMap reads expect, refusing a key outside allowed.
func expectMap(params map[string]any, allowed ...string) (map[string]any, error) {
	raw, present := params[paramExpect]
	if !present || raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is a mapping of %s", paramExpect, strings.Join(allowed, ", "))
	}
	for k := range m {
		found := false
		for _, a := range allowed {
			found = found || a == k
		}
		if !found {
			return nil, fmt.Errorf("%s.%s is not one of %s", paramExpect, k, strings.Join(allowed, ", "))
		}
	}
	return m, nil
}

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// ctxDone is how a cancelled read reads.
func ctxDone(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

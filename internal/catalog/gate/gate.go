// Package gate implements gate.run and gate.list: a project's gate
// manifest of named checks, read from the device, validated, and either run
// (one typed result per check) or listed. The manifest's rules, selection,
// scheduling and results are pkg/gate's; this package reaches the device.
package gate

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods' names.
const (
	fqcnRun  = "gate.run"
	fqcnList = "gate.list"
)

// Parameter names.
const (
	paramManifest    = "manifest"
	paramKinds       = "kinds"
	paramChecks      = "checks"
	paramHeavy       = "heavy"
	paramParallelism = "parallelism"
	paramFailOnSkip  = "fail_on_skip"
)

// Return names.
const (
	statChecks   = "checks"
	statPassed   = "passed"
	statFailed   = "failed"
	statSkipped  = "skipped"
	statSummary  = "summary"
	statLedger   = "ledger"
	statKinds    = "kinds"
	statSelected = "selected"
)

// The bounds on what a check's result keeps of its output.
const (
	tailLines = 40
	tailBytes = 8 << 10
)

// maxManifestPath bounds the manifest parameter.
const maxManifestPath = 4096

// request is a task's parameters, read and checked.
type request struct {
	manifest    string
	selection   gate.Selection
	parallelism int
	failOnSkip  bool
}

// parseRequest reads params, refusing anything malformed before the
// device is contacted.
func parseRequest(params map[string]any) (request, error) {
	var req request
	manifest, err := sdk.RequiredStringParam(params, paramManifest)
	if err != nil {
		return req, err
	}
	if len(manifest) > maxManifestPath || strings.ContainsAny(manifest, "\x00\n\r") {
		return req, fmt.Errorf("%s is not a path", paramManifest)
	}
	req.manifest = manifest
	if req.selection.Kinds, _, err = sdk.StringSlice(params, paramKinds); err != nil {
		return req, err
	}
	if req.selection.Checks, _, err = sdk.StringSlice(params, paramChecks); err != nil {
		return req, err
	}
	req.selection.Heavy = sdk.StringParam(params, paramHeavy)
	if n, set, err := sdk.IntParam(params, paramParallelism); err != nil {
		return req, err
	} else if set && (n < 1 || n > gate.MaxParallelism) {
		return req, fmt.Errorf("%s %d: it is 1 to %d", paramParallelism, n, gate.MaxParallelism)
	} else {
		req.parallelism = n
	}
	if req.failOnSkip, err = sdk.BoolParamOr(params, paramFailOnSkip, false); err != nil {
		return req, err
	}
	return req, nil
}

// loadManifest reads the manifest at file from the device and parses it.
// A file past gate.MaxManifestBytes is refused unread past that bound.
func loadManifest(ctx context.Context, conn *remoteexec.Conn, file string) (*gate.Manifest, error) {
	res, err := conn.RunWithStdinBounded(ctx, remoteexec.QuoteCommand([]string{"cat", "--", file}), nil, gate.MaxManifestBytes)
	if errors.Is(err, remoteexec.ErrOutputTooLarge) {
		return nil, fmt.Errorf("the manifest %s is over the %d bytes a manifest may have", file, gate.MaxManifestBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the manifest %s: %w", file, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("could not read the manifest %s: %s", file, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	m, err := gate.Parse([]byte(res.Stdout))
	if err != nil {
		return nil, fmt.Errorf("the manifest %s: %w", file, err)
	}
	return m, nil
}

// dirOf is where check c runs: its dir, relative to the manifest's
// directory unless absolute, with a leading '-' made a relative path so no
// shell reads it as an option.
func dirOf(manifest string, c gate.Check) string {
	dir := c.Dir
	if !path.IsAbs(dir) {
		dir = path.Join(path.Dir(manifest), dir)
	}
	if strings.HasPrefix(dir, "-") {
		dir = "./" + dir
	}
	return dir
}

// needsEngine reports whether a check runs on, or requires, the device's
// container engine.
func needsEngine(c gate.Check) bool { return c.Image != "" || c.Requires.Engine }

// engineRefusal is why device cannot run check c, or "": an image check,
// or one requiring the engine, on a device with no engine onboarded.
func engineRefusal(device inventory.InventoryItem, c gate.Check) string {
	if needsEngine(c) && !device.HasCapability(capability.NameContainerEngine) {
		return "it needs the device's container engine, and no engine was onboarded on it (run pleiades onboard)"
	}
	return ""
}

// connectFor opens the task's own SSH connection, naming the method.
func connectFor(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*remoteexec.Conn, error) {
	return sdk.Connect(ctx, rc, device, params, fqcn)
}

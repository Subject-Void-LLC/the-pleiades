// Package lock implements build.lock.verify: a lockfile checked against
// its manifest on the device that holds the project. Reading a lockfile is
// pkg/lockcheck's, through the format registered for it.
package lock

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/lockcheck"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fqcnVerify is the method's name.
const fqcnVerify = "build.lock.verify"

// Parameter names.
const (
	paramLockfile = "lockfile"
	paramManifest = "manifest"
	paramFormat   = "format"
	paramSources  = "sources"
)

// statFindings is the return naming each shortfall.
const statFindings = "findings"

// maxListed bounds how many findings a failed task's error names; the rest
// are counted.
const maxListed = 50

// checkPath refuses a path no file could have.
func checkPath(key, p string) error {
	if p == "" || len(p) > 4096 || strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("%s: %q is not a path", key, p)
	}
	return nil
}

// listed is lines for a failed task's error: at most maxListed of them,
// the rest counted, each cut to maxShown bytes and escaped, since a line
// carries text read from the device (a reference, a package name).
func listed(lines []string) string {
	shown := make([]string, 0, min(len(lines), maxListed)+1)
	for _, line := range lines[:min(len(lines), maxListed)] {
		shown = append(shown, remoteexec.Excerpt(line, maxShown))
	}
	if len(lines) > maxListed {
		shown = append(shown, fmt.Sprintf("... and %d more", len(lines)-maxListed))
	}
	return "\n  " + strings.Join(shown, "\n  ")
}

// maxShown bounds one item of device text in an error or a stat. No image
// reference is near it: a name is at most 255 bytes, a tag 128.
const maxShown = 512

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnVerify,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It reads a lockfile and its manifest, and writes nothing.",
			},
			Doc: lockDoc(),
		},
		Invoke: Verify,
		// It only reads, so a check of it is the task itself.
		Check: Verify,
	})
}

// Verify implements build.lock.verify.
func Verify(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	lockfile, err := sdk.RequiredStringParam(params, paramLockfile)
	if err == nil {
		err = checkPath(paramLockfile, lockfile)
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	format, err := formatFor(params, lockfile)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	manifest := sdk.StringParam(params, paramManifest)
	if manifest == "" {
		manifest = path.Join(path.Dir(lockfile), format.ManifestFile())
	}
	if err := checkPath(paramManifest, manifest); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	sources, _, err := sdk.StringSlice(params, paramSources)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcnVerify)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	lockData, _, err := remotefile.ReadText(ctx, conn, lockfile, lockcheck.MaxFileBytes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	manifestData, _, err := remotefile.ReadText(ctx, conn, manifest, lockcheck.MaxFileBytes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	findings, err := format.Verify(lockData, manifestData, sources)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcnVerify, lockfile, err)
	}

	entries := make([]any, len(findings))
	lines := make([]string, len(findings))
	for i, f := range findings {
		entries[i] = map[string]any{"package": remoteexec.Excerpt(f.Package, maxShown), "problem": remoteexec.Excerpt(f.Problem, maxShown)}
		lines[i] = f.Package + ": " + f.Problem
	}
	if err := rc.SetStat(statFindings, entries); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVerify, err)
	}
	if len(findings) > 0 {
		return collection.Result{}, fmt.Errorf("%s: %s falls short of %s in %d way(s)%s", fqcnVerify, lockfile, manifest, len(findings), listed(lines))
	}
	return collection.Result{Changed: false}, nil
}

// formatFor is the format a task names, or the one whose lockfile is named
// like lockfile.
func formatFor(params map[string]any, lockfile string) (lockcheck.Format, error) {
	if name := sdk.StringParam(params, paramFormat); name != "" {
		f, ok := lockcheck.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("format %q is not one this reads (%s)", name, strings.Join(lockcheck.Names(), ", "))
		}
		return f, nil
	}
	f, ok := lockcheck.ForLockFile(lockfile)
	if !ok {
		return nil, fmt.Errorf("%s is not a lockfile this recognizes by name; set format (%s)", lockfile, strings.Join(lockcheck.Names(), ", "))
	}
	return f, nil
}

// scan.sbom: Syft over an image archive or a directory, an SPDX 2.3 SBOM
// held to the NTIA minimum elements, written with its SHA256SUMS into a
// new directory on the device.
package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sbomcheck"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// sbom's own parameters and returns.
const (
	paramDest            = "dest"
	paramName            = "name"
	paramVersion         = "version"
	paramSupplier        = "supplier"
	paramStrictSuppliers = "strict_suppliers"

	statDest            = "dest"
	statSBOM            = "sbom"
	statSHA256          = "sha256"
	statComponents      = "components"
	statMissingSupplier = "missing_supplier"
)

// The files scan.sbom writes into its directory.
const (
	sbomFile = "sbom.spdx.json"
	sumsFile = "SHA256SUMS"
)

// cleanupTimeout bounds removing the directory a failed write left.
const cleanupTimeout = 30 * time.Second

// sbomTool is how this platform names itself among an SBOM's creators,
// having set the subject's supplier.
const sbomTool = "pleiades"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnSBOM,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: requiredCapabilities,
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo removes the directory it created; it never writes into an existing one, so nothing else is lost.",
				Inverses:   []sdk.InverseSpec{{FQCN: "file.remove", Record: []string{"path", "recurse"}}},
			},
			Doc: sbomDoc(),
		},
		Invoke: SBOM,
		Check:  CheckSBOM,
	})
}

// SBOM implements scan.sbom.
func SBOM(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return sbom(ctx, rc, device, params, collection.ModeExecute)
}

// CheckSBOM is scan.sbom's check: the SBOM generated and validated as a
// run does, and nothing written.
func CheckSBOM(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return sbom(ctx, rc, device, params, collection.ModeCheck)
}

// sbom is SBOM's and CheckSBOM's one body.
func sbom(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnSBOM, err)
	}
	t, err := targetParam(params, true)
	if err != nil {
		return fail(err)
	}
	dest, err := sdk.RequiredStringParam(params, paramDest)
	if err == nil {
		err = absolutePath(paramDest, dest)
	}
	if err != nil {
		return fail(err)
	}
	subject := map[string]string{}
	for _, key := range []string{paramName, paramVersion, paramSupplier} {
		v, err := sdk.RequiredStringParam(params, key)
		if err != nil {
			return fail(err)
		}
		if strings.ContainsAny(v, "\x00\r\n") || len(v) > 256 {
			return fail(fmt.Errorf("%s: %q is not one line of at most 256 bytes", key, v))
		}
		subject[key] = v
	}
	if !strings.HasPrefix(subject[paramSupplier], "Organization: ") && !strings.HasPrefix(subject[paramSupplier], "Person: ") {
		return fail(fmt.Errorf("%s is \"Organization: name\" or \"Person: name\", as SPDX writes a supplier", paramSupplier))
	}
	strict, err := sdk.BoolParamOr(params, paramStrictSuppliers, false)
	if err != nil {
		return fail(err)
	}
	allowEmpty, err := sdk.BoolParamOr(params, paramAllowEmpty, false)
	if err != nil {
		return fail(err)
	}
	timeout, err := durationParam(params, paramTimeout, defaultTimeout, maxTimeout)
	if err != nil {
		return fail(err)
	}
	pull, err := sdk.BoolParamOr(params, paramPull, true)
	if err != nil {
		return fail(err)
	}

	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnSBOM)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()
	if err := t.check(ctx, ssh); err != nil {
		return fail(err)
	}
	if err := destFree(ctx, ssh, dest); err != nil {
		return fail(err)
	}
	engine, err := openEngine(ctx, rc, device, params, fqcnSBOM)
	if err != nil {
		return collection.Result{}, err
	}
	source := "dir:" + t.inContainer()
	if !t.dir {
		source = "docker-archive:" + t.inContainer()
	}
	ran, err := run(ctx, engine, runOptions{
		image: syftImage, mounts: []containerengine.ReadOnlyMount{t.mount()}, timeout: timeout, pull: pull, check: mode == collection.ModeCheck,
		cmd: []string{"scan", source, "-o", "spdx-json", "--source-name", subject[paramName], "--source-version", subject[paramVersion]},
		// Syft asks the network for its own latest release, which a
		// container with none answers with an error on every run.
		env: []string{"SYFT_CHECK_FOR_APP_UPDATE=false"},
	})
	if err != nil {
		return fail(err)
	}
	if ran.exitCode != 0 {
		return fail(ran.failure("Syft"))
	}
	doc, err := sbomcheck.Parse(ran.report)
	if err != nil {
		return fail(fmt.Errorf("Syft's SBOM: %w", err))
	}
	written, err := doc.SetSubjectSupplier(subject[paramSupplier], sbomTool)
	if err != nil {
		return fail(err)
	}
	final, err := sbomcheck.Parse(written)
	if err != nil {
		return fail(err)
	}
	report := final.Validate()
	if len(report.Problems) > 0 {
		return fail(fmt.Errorf("the SBOM does not meet the NTIA minimum elements%s", listed(report.Problems)))
	}
	if strict && len(report.MissingSupplier) > 0 {
		return fail(fmt.Errorf("%d component(s) have no supplier (%s)%s", len(report.MissingSupplier), paramStrictSuppliers, listed(report.MissingSupplier)))
	}
	var warnings []string
	// An SBOM of nothing but its subject meets every element and says
	// nothing: a wrong path, or files Syft could not read, look the same.
	if report.Dependencies == 0 {
		if !allowEmpty {
			return fail(fmt.Errorf("the SBOM holds no component but its subject: Syft found nothing to describe in %s; set %s if that is expected", t.path, paramAllowEmpty))
		}
		warnings = append(warnings, fmt.Sprintf("the SBOM holds no component but its subject: Syft found nothing to describe in %s (%s)", t.path, paramAllowEmpty))
	}
	if n := len(report.MissingSupplier); n > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d dependencies have no supplier the generator could name (NOASSERTION)", n, report.Dependencies))
	}
	sum := sha256.Sum256(written)
	digest := hex.EncodeToString(sum[:])
	sbomPath := path.Join(dest, sbomFile)
	for key, value := range map[string]any{statDest: dest, statSBOM: sbomPath, statSHA256: digest, statComponents: report.Components, statMissingSupplier: len(report.MissingSupplier), statScanner: syftImage} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordWarnings(rc, warnings)
	}

	if err := writeSBOM(ctx, ssh, dest, written, digest); err != nil {
		return fail(err)
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "file.remove",
		Params:      map[string]any{"path": dest, "recurse": true},
		Description: "Remove the SBOM directory this task wrote.",
	}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, recordWarnings(rc, warnings)
}

// destFree refuses a destination that exists, or whose parent does not.
func destFree(ctx context.Context, ssh *remoteexec.Conn, dest string) error {
	info, err := remotefile.Stat(ctx, ssh, dest)
	if err != nil {
		return err
	}
	if info.Exists() {
		return fmt.Errorf("%s exists; an SBOM is written into a new directory and never replaces one", dest)
	}
	parent, err := remotefile.Stat(ctx, ssh, path.Dir(dest))
	if err != nil {
		return err
	}
	if parent.Kind != remotefile.KindDirectory {
		return fmt.Errorf("%s is not a directory on the device", path.Dir(dest))
	}
	return nil
}

// writeSBOM creates dest and writes the SBOM and its SHA256SUMS into it,
// holding the written bytes to their digest, and removes the directory
// again if any of that fails.
func writeSBOM(ctx context.Context, ssh *remoteexec.Conn, dest string, sbom []byte, digest string) error {
	res, err := ssh.Run(ctx, "mkdir -m 0755 -- "+remoteexec.QuoteArg(dest))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("creating %s: %s", dest, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	undo := func(cause error) error {
		// On a context of its own, so a task whose context ended (often
		// why the write failed) still removes what it made: a directory
		// left behind would refuse every rerun.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := remotefile.Remove(cctx, ssh, dest, true); err != nil {
			return fmt.Errorf("%w; and %s could not be removed: %v", cause, dest, err)
		}
		return cause
	}
	sbomPath := path.Join(dest, sbomFile)
	if err := remotefile.Write(ctx, ssh, sbomPath, sbom); err != nil {
		return undo(err)
	}
	sumsPath := path.Join(dest, sumsFile)
	if err := remotefile.Write(ctx, ssh, sumsPath, []byte(digest+"  "+sbomFile+"\n")); err != nil {
		return undo(err)
	}
	// Write leaves each file mktemp's 0600; these are reports meant to be
	// read, as the directory holding them is. Both paths are absolute, so
	// neither reads as an option.
	res, err = ssh.Run(ctx, "chmod 0644 "+remoteexec.QuoteArg(sbomPath)+" "+remoteexec.QuoteArg(sumsPath))
	if err != nil {
		return undo(err)
	}
	if res.ExitCode != 0 {
		return undo(fmt.Errorf("making the SBOM readable: %s", remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256)))
	}
	onDevice, _, err := remotefile.Checksum(ctx, ssh, sbomPath)
	if err != nil {
		return undo(err)
	}
	if onDevice != digest {
		return undo(fmt.Errorf("the SBOM on the device hashes to %s, not %s", onDevice, digest))
	}
	return nil
}

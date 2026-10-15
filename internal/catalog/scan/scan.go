// Package scan implements scan.vuln, scan.sbom and scan.secrets: what a
// release needs to know about what it ships, asked on the device that
// holds it (docs/design/scan.md). Each scanner runs in a fresh container
// on the device's own engine, from an image pinned by its multi-platform
// index digest here, with what it scans mounted read-only and no network
// unless a scan needs one; its report comes back on standard output and
// is read by pkg/osvreport, pkg/sbomcheck or this package.
package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/scanignore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The scanners, each pinned by its multi-platform index digest. Moving
// one is a release decision: its tests run against the pinned image.
const (
	osvScannerImage = "ghcr.io/google/osv-scanner:v2.6.0@sha256:afd838850ac1a0fcc15ff4a041dc9ba11123c3f0d2666217a5f0fcf9222b55fa"
	syftImage       = "anchore/syft:v1.52.0@sha256:500e2d872ac019436926e8322b4fc1f39441d94d21f6f4046c6ff29b30e8cb02"
	gitleaksImage   = "zricethezav/gitleaks:v8.30.0@sha256:691af3c7c5a48b16f187ce3446d5f194838f91238f27270ed36eef6359a574d9"
)

// The methods' names.
const (
	fqcnVuln    = "scan.vuln"
	fqcnSBOM    = "scan.sbom"
	fqcnSecrets = "scan.secrets"
)

// Parameters the methods share.
const (
	paramDir          = "dir"
	paramImageArchive = "image_archive"
	paramIgnore       = "ignore"
	paramTimeout      = "timeout"
	paramPull         = "pull"

	statScanner = "scanner"
)

// Bounds every scan shares.
const (
	defaultTimeout = 15 * time.Minute
	maxTimeout     = 2 * time.Hour
	maxReportBytes = 64 << 20
	maxListed      = 50
)

// requiredCapabilities are every scan's: SSH commands for the paths and
// the ignore file, and the device's engine for the scanner.
var requiredCapabilities = []capability.Name{capability.NameCommandExec, capability.NameContainerEngine}

// target is what a scan reads: one directory or one image archive on the
// device, mounted read-only at mountPath.
type target struct {
	param, path string
	dir         bool
}

// mountPath is where a target appears in the scanner's container.
const mountPath = "/scan"

// targetParam reads dir or image_archive, exactly one, refusing a path no
// file could have.
func targetParam(params map[string]any, allowArchive bool) (target, error) {
	dir, archive := sdk.StringParam(params, paramDir), sdk.StringParam(params, paramImageArchive)
	switch {
	case dir != "" && archive != "":
		return target{}, fmt.Errorf("give %s or %s, not both", paramDir, paramImageArchive)
	case dir == "" && archive == "" && allowArchive:
		return target{}, fmt.Errorf("give %s (a directory on the device) or %s (an image archive on the device)", paramDir, paramImageArchive)
	case dir == "" && archive == "":
		return target{}, fmt.Errorf("give %s, a directory on the device", paramDir)
	case archive != "" && !allowArchive:
		return target{}, fmt.Errorf("%s scans a directory, not %s", fqcnSecrets, paramImageArchive)
	}
	t := target{param: paramDir, path: dir, dir: true}
	if archive != "" {
		t = target{param: paramImageArchive, path: archive}
	}
	if err := absolutePath(t.param, t.path); err != nil {
		return target{}, err
	}
	return t, nil
}

// absolutePath refuses a path that is not absolute and clean.
func absolutePath(key, p string) error {
	if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n,") {
		return fmt.Errorf("%s: %q is not an absolute, clean path", key, p)
	}
	return nil
}

// check refuses a target that is absent or not the kind its parameter
// names.
func (t target) check(ctx context.Context, ssh *remoteexec.Conn) error {
	info, err := remotefile.Stat(ctx, ssh, t.path)
	if err != nil {
		return err
	}
	want := remotefile.KindFile
	if t.dir {
		want = remotefile.KindDirectory
	}
	if info.Kind != want {
		return fmt.Errorf("%s: %s is a %s on the device, not a %s", t.param, t.path, info.Kind, want)
	}
	return nil
}

// mount binds the target read-only into the scanner's container.
func (t target) mount() containerengine.ReadOnlyMount {
	if t.dir {
		return containerengine.ReadOnlyMount{Source: t.path, Target: mountPath}
	}
	return containerengine.ReadOnlyMount{Source: t.path, Target: mountPath + "/" + path.Base(t.path)}
}

// inContainer is the target as the scanner sees it.
func (t target) inContainer() string { return t.mount().Target }

// onDevice maps a path the scanner reported back to the device's.
func (t target) onDevice(reported string) string {
	if !t.dir {
		return t.path
	}
	if rel, ok := strings.CutPrefix(reported, mountPath+"/"); ok {
		return path.Join(t.path, rel)
	}
	return reported
}

// relative is a path the scanner reported, relative to the scanned
// directory.
func (t target) relative(reported string) string {
	if rel, ok := strings.CutPrefix(reported, mountPath+"/"); ok {
		return rel
	}
	return strings.TrimPrefix(reported, mountPath)
}

// runOptions are a scanner run's own settings.
type runOptions struct {
	image   string
	cmd     []string
	mounts  []containerengine.ReadOnlyMount
	env     []string
	network string
	timeout time.Duration
	pull    bool
}

// scannerRun is what a scanner left: its exit status, its report, and the
// end of its standard error.
type scannerRun struct {
	exitCode int
	report   []byte
	errors   *gate.Tail
}

// errReportTooLarge is a report past maxReportBytes.
var errReportTooLarge = errors.New("the scanner's report is larger than this reads")

// capped is a buffer that refuses to grow past max.
type capped struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		c.overflow = true
		return 0, errReportTooLarge
	}
	return c.buf.Write(p)
}

// openEngine opens the device's engine for running a scanner, naming why
// a device without one cannot.
func openEngine(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*containerengine.Session, error) {
	if !device.HasCapability(capability.NameContainerEngine) {
		return nil, fmt.Errorf("%s runs its scanner on the device's container engine, and none was onboarded on %q (run pleiades onboard)", fqcn, device.Name())
	}
	return containerengine.Open(ctx, rc, device, params, fqcn, containerengine.RunOnceOps...)
}

// run runs a scanner to its end and keeps its report, refusing one past
// the bound rather than reading part of it.
func run(ctx context.Context, engine *containerengine.Session, o runOptions) (scannerRun, error) {
	report := &capped{max: maxReportBytes}
	errs := gate.NewTail(15, 4096)
	res, err := engine.RunOnce(ctx, containerengine.RunOnceOptions{
		Image: o.image, Cmd: o.cmd, Mounts: o.mounts, Env: o.env, Network: o.network,
		Timeout: o.timeout, Pull: o.pull, Output: report, Errors: errs,
		Labels: map[string]string{"org.pleiades.scan": path.Base(strings.SplitN(o.image, ":", 2)[0])},
	})
	if report.overflow {
		return scannerRun{}, fmt.Errorf("%w (%d bytes)", errReportTooLarge, maxReportBytes)
	}
	if err != nil {
		return scannerRun{}, err
	}
	if res.TimedOut {
		return scannerRun{}, fmt.Errorf("the scanner ran past its %s timeout and was stopped", o.timeout)
	}
	return scannerRun{exitCode: res.ExitCode, report: report.buf.Bytes(), errors: errs}, nil
}

// failure is a scanner's own error, with the end of what it said.
func (r scannerRun) failure(name string) error {
	return fmt.Errorf("%s exited %d:\n%s", name, r.exitCode, r.errors.String())
}

// defaultNetwork is the network an engine puts a container on when told
// nothing: the one an online scan joins.
func defaultNetwork(engine *containerengine.Session) string {
	if engine.Kind == capability.EnginePodman {
		return "podman"
	}
	return "bridge"
}

// loadIgnore reads the ignore file a task names, if it names one.
func loadIgnore(ctx context.Context, ssh *remoteexec.Conn, params map[string]any) (scanignore.File, error) {
	p := sdk.StringParam(params, paramIgnore)
	if p == "" {
		return scanignore.File{}, nil
	}
	if err := absolutePath(paramIgnore, p); err != nil {
		return scanignore.File{}, err
	}
	data, _, err := remotefile.ReadText(ctx, ssh, p, scanignore.MaxFileBytes)
	if err != nil {
		return scanignore.File{}, err
	}
	f, err := scanignore.Parse(data)
	if err != nil {
		return scanignore.File{}, fmt.Errorf("%s: %w", p, err)
	}
	return f, nil
}

// ignoreWarnings says what an ignore file did not do: entries past their
// date, which suppress nothing, and entries no finding met, which can go.
func ignoreWarnings(j *scanignore.Judge) []string {
	var out []string
	for _, e := range j.Expired() {
		out = append(out, fmt.Sprintf("ignore entry %s has expired and suppresses nothing; fix the finding or renew it with a reason", e))
	}
	for _, e := range j.Unused() {
		out = append(out, fmt.Sprintf("ignore entry %s matched nothing and can be removed", e))
	}
	return out
}

// durationParam reads a duration written as Go writes one ("30s", "15m"),
// def when absent, refused past max or below a second.
func durationParam(params map[string]any, key string, def, max time.Duration) (time.Duration, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return def, nil
	}
	text, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("%s is a duration such as \"15m\"", key)
	}
	d, err := time.ParseDuration(text)
	if err != nil || d < time.Second || d > max {
		return 0, fmt.Errorf("%s: %q is not a duration from 1s to %s", key, text, max)
	}
	return d, nil
}

// listed is lines for a failed task's error: at most maxListed, the rest
// counted.
func listed(lines []string) string {
	if len(lines) > maxListed {
		lines = append(lines[:maxListed:maxListed], fmt.Sprintf("... and %d more", len(lines)-maxListed))
	}
	return "\n  " + strings.Join(lines, "\n  ")
}

// recordWarnings records a run's warnings, when it has any.
func recordWarnings(rc sdk.RunbookContext, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	return sdk.RecordWarnings(rc, warnings)
}

// today is the day an ignore file is judged on, in UTC.
var today = func() time.Time { return time.Now().UTC() }

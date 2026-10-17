// scan.vuln: OSV-Scanner over an image archive, a directory or
// lockfiles, gated on severity and fixability.
package scan

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/osvreport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vuln's own parameters and returns.
const (
	paramLockfiles   = "lockfiles"
	paramOfflineDB   = "offline_db"
	paramFailOn      = "fail_on"
	paramFixableOnly = "fixable_only"

	statFindings = "findings"
	statFailing  = "failing"
	statCounts   = "counts"
)

// maxLockfiles bounds how many lockfiles one task names.
const maxLockfiles = 100

// maxRecordedFindings bounds how many findings the result lists; the
// counts cover every one.
const maxRecordedFindings = 1000

// OSV-Scanner's exit statuses: findings found, and no packages found.
const (
	osvFound      = 1
	osvNoPackages = 128
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnVuln,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: requiredCapabilities,
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It reads what it scans, mounted read-only into the scanner's container, and writes nothing.",
			},
			Doc: vulnDoc(),
		},
		Invoke: Vuln,
		Check:  CheckVuln,
	})
}

// threshold reads fail_on: the least severity that fails the task.
func threshold(params map[string]any) (osvreport.Rating, error) {
	name := sdk.StringParam(params, paramFailOn)
	switch name {
	case "":
		return osvreport.High, nil
	case "any":
		return osvreport.Unknown, nil
	}
	r, err := osvreport.ParseRating(name)
	if err != nil || r == osvreport.Unknown {
		return 0, fmt.Errorf("%s is critical, high, medium, low or any, not %q", paramFailOn, name)
	}
	return r, nil
}

// gated is a finding's rating as the gate weighs it: unknown as high, since
// an advisory nobody has scored is not evidence it is harmless.
func gated(r osvreport.Rating) osvreport.Rating {
	if r == osvreport.Unknown {
		return osvreport.High
	}
	return r
}

// Vuln implements scan.vuln.
func Vuln(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return vuln(ctx, rc, device, params, collection.ModeExecute)
}

// CheckVuln is scan.vuln's check: it only reads, so it is the scan a run
// makes, sending what a run sends, except that it never pulls the
// scanner's image.
func CheckVuln(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return vuln(ctx, rc, device, params, collection.ModeCheck)
}

// vuln is Vuln's and CheckVuln's one body.
func vuln(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnVuln, err)
	}
	lockfiles, _, err := sdk.StringSlice(params, paramLockfiles)
	if err != nil {
		return fail(err)
	}
	var t target
	if len(lockfiles) == 0 {
		if t, err = targetParam(params, true); err != nil {
			return fail(fmt.Errorf("%w, or %s", err, paramLockfiles))
		}
	} else if sdk.StringParam(params, paramDir) != "" || sdk.StringParam(params, paramImageArchive) != "" {
		return fail(fmt.Errorf("give %s, %s or %s, only one", paramDir, paramImageArchive, paramLockfiles))
	}
	if len(lockfiles) > maxLockfiles {
		return fail(fmt.Errorf("%s names %d files, over %d", paramLockfiles, len(lockfiles), maxLockfiles))
	}
	for _, l := range lockfiles {
		if err := absolutePath(paramLockfiles, l); err != nil {
			return fail(err)
		}
	}
	least, err := threshold(params)
	if err != nil {
		return fail(err)
	}
	fixableOnly, err := sdk.BoolParamOr(params, paramFixableOnly, false)
	if err != nil {
		return fail(err)
	}
	offline := sdk.StringParam(params, paramOfflineDB)
	if offline != "" {
		if err := absolutePath(paramOfflineDB, offline); err != nil {
			return fail(err)
		}
	}
	timeout, err := durationParam(params, paramTimeout, defaultTimeout, maxTimeout)
	if err != nil {
		return fail(err)
	}
	pull, err := sdk.BoolParamOr(params, paramPull, true)
	if err != nil {
		return fail(err)
	}
	allowEmpty, err := sdk.BoolParamOr(params, paramAllowEmpty, false)
	if err != nil {
		return fail(err)
	}

	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnVuln)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()
	var mounts []containerengine.ReadOnlyMount
	cmd := []string{"scan", "source", "--format", "json"}
	byContainer := map[string]string{}
	switch {
	case len(lockfiles) > 0:
		for i, l := range lockfiles {
			lt := target{param: paramLockfiles, path: l}
			if err := lt.check(ctx, ssh); err != nil {
				return fail(err)
			}
			inside := mountPath + "/" + strconv.Itoa(i) + "/" + path.Base(l)
			mounts = append(mounts, containerengine.ReadOnlyMount{Source: l, Target: inside})
			byContainer[inside] = l
			cmd = append(cmd, "-L", inside)
		}
	case t.dir:
		if err := t.check(ctx, ssh); err != nil {
			return fail(err)
		}
		// OSV-Scanner reads an osv-scanner.toml beside each lockfile it
		// finds, at any depth, as a list of vulnerabilities to ignore.
		found, err := scannerConfigs(ctx, ssh, t.path, []string{"osv-scanner.toml"}, false)
		if err != nil {
			return fail(err)
		}
		if len(found) > 0 {
			return fail(configRefusal("OSV-Scanner", found))
		}
		mounts = append(mounts, t.mount())
		cmd = append(cmd, "-r", t.inContainer())
	default:
		if err := t.check(ctx, ssh); err != nil {
			return fail(err)
		}
		mounts = append(mounts, t.mount())
		cmd = []string{"scan", "image", "--archive", "--format", "json", t.inContainer()}
	}
	var env []string
	if offline != "" {
		info, err := remotefile.Stat(ctx, ssh, offline)
		if err != nil {
			return fail(err)
		}
		if info.Kind != remotefile.KindDirectory {
			return fail(fmt.Errorf("%s: %s is not a directory on the device", paramOfflineDB, offline))
		}
		mounts = append(mounts, containerengine.ReadOnlyMount{Source: offline, Target: "/db"})
		env = append(env, "OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=/db")
		cmd = append(cmd, "--offline-vulnerabilities", "--offline")
	}
	// An empty configuration of this run's own replaces any osv-scanner.toml
	// what is scanned carries, so none is read even where none was found
	// above (inside an image archive, or written after the search).
	cmd = append(cmd, "--config", "/dev/null")
	ignore, err := loadIgnore(ctx, ssh, params)
	if err != nil {
		return fail(err)
	}

	engine, err := openEngine(ctx, rc, device, params, fqcnVuln)
	if err != nil {
		return collection.Result{}, err
	}
	network := ""
	if offline == "" {
		network = defaultNetwork(engine)
	}
	ran, err := run(ctx, engine, runOptions{image: osvScannerImage, cmd: cmd, mounts: mounts, env: env, network: network, timeout: timeout, pull: pull, check: mode == collection.ModeCheck})
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statScanner, osvScannerImage); err != nil {
		return collection.Result{}, err
	}
	var warnings []string
	var findings []osvreport.Finding
	switch ran.exitCode {
	case 0, osvFound:
		if findings, err = osvreport.Parse(ran.report); err != nil {
			return fail(fmt.Errorf("%s's report: %w", scannerLabel("OSV-Scanner", osvScannerImage), err))
		}
	case osvNoPackages:
		// A scan that saw nothing is no evidence of no vulnerability: a
		// wrong path, or files the scanner could not read, look the same.
		scanned := t.path
		if len(lockfiles) > 0 {
			scanned = strings.Join(lockfiles, ", ")
		}
		if !allowEmpty {
			return fail(fmt.Errorf("OSV-Scanner found no package to check in %s, so nothing was scanned; set %s if that is expected", scanned, paramAllowEmpty))
		}
		warnings = append(warnings, fmt.Sprintf("OSV-Scanner found no package to check in %s (%s)", scanned, paramAllowEmpty))
	default:
		return fail(ran.failure(scannerLabel("OSV-Scanner", osvScannerImage)))
	}

	judge := ignore.Judge(today())
	counts := map[string]any{}
	entries := []any{}
	var failing []string
	for _, f := range findings {
		counts[f.Rating.String()] = intOf(counts[f.Rating.String()]) + 1
		entry := map[string]any{
			"package": f.Package, "binary": f.Binary, "version": f.Version, "ecosystem": f.Ecosystem, "id": f.ID,
			"aliases": strs(f.Aliases), "severity": f.Rating.String(), "score": f.Score, "fixed": strs(f.Fixed),
			"source": sourceOnDevice(t, byContainer, f.Source),
		}
		if e, ok := judge.Ignored(f.Names()...); ok {
			entry["ignored"] = e.Reason
		} else if gated(f.Rating) >= least && (!fixableOnly || len(f.Fixed) > 0) {
			fix := "no fix known"
			if len(f.Fixed) > 0 {
				fix = "fixed in " + strings.Join(f.Fixed, ", ")
			}
			// Each part is the scanner's text, bounded on its own so a long
			// name cannot crowd out the advisory it is exposed to.
			failing = append(failing, fmt.Sprintf("%s: %s (%s); %s", reported(f.Label()), reported(f.ID), f.Rating, reported(fix)))
		}
		if len(entries) < maxRecordedFindings {
			entries = append(entries, entry)
		}
	}
	warnings = append(warnings, ignoreWarnings(judge)...)
	for key, value := range map[string]any{statFindings: entries, statFailing: len(failing), statCounts: counts} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	if err := recordWarnings(rc, warnings); err != nil {
		return collection.Result{}, err
	}
	if len(failing) > 0 {
		return fail(fmt.Errorf("%d known vulnerabilit(y/ies) at or above %s%s", len(failing), least, listed(failing)))
	}
	return collection.Result{Changed: false}, nil
}

// sourceOnDevice maps the path OSV-Scanner read a package from back to
// the device's.
func sourceOnDevice(t target, byContainer map[string]string, reported string) string {
	if p, ok := byContainer[reported]; ok {
		return p
	}
	switch {
	case t.path != "" && !t.dir:
		// A path inside the image, named within the archive it came from.
		return t.path + ":" + reported
	case t.path != "":
		return t.onDevice(reported)
	}
	return reported
}

// intOf is a count, 0 when absent.
func intOf(v any) int {
	n, _ := v.(int)
	return n
}

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

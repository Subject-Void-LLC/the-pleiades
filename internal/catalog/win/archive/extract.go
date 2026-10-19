// Package archive implements win.archive.extract: an archive already on a
// Windows host unpacked into a folder there, over WinRM, by the tar.exe
// Windows ships (which reads zip as well as tar).
package archive

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const fqcn = "win.archive.extract"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcn,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportWinRM},
			RequiredCapabilities: []capability.Name{capability.NameWindowsShell},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{Reversible: false, Notes: "Extracting can write any number of files under dest, " +
				"some replacing files dest already held, and this method does not list them as it goes; an undo would have to delete " +
				"exactly what was added and restore what was replaced, which it cannot know, as archive.extract cannot."},
			Doc: extractDoc(),
		},
		Invoke: Extract,
		Check:  CheckExtract,
	})
}

// Parameter and stat names.
const (
	paramSrc     = "src"
	paramDest    = "dest"
	paramCreates = "creates"
	paramRemove  = "remove"
	paramTimeout = "timeout"

	statDest      = "dest"
	statExtracted = "extracted"
)

// defaultTimeout is how long an extraction may take when the task does
// not say.
const defaultTimeout = 600

// tarPath is the tar.exe Windows ships, by absolute path.
const tarPath = `C:\Windows\System32\tar.exe`

// The exit codes the script gives for each way it can fail.
const (
	exitTar       = 20
	exitNoSrc     = 22
	exitDestIsNot = 23
)

// run is how the script reaches the host; a test replaces it.
var run = winrmexec.Run

// pathPattern is an absolute drive path with no quote, wildcard, control
// character or trailing separator, as win.file.download's.
var pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^'"*?<>|\x00-\x1f\x7f]*[^'"*?<>|\\\x00-\x1f\x7f ]$`)

// driveRoot is a drive's root folder, which dest may be: an archive that
// holds its own top folder (Go's holds go\) is extracted there.
var driveRoot = regexp.MustCompile(`^[A-Za-z]:\\$`)

// archiveSuffixes are the archives this method extracts.
var archiveSuffixes = []string{".zip", ".tar", ".tar.gz", ".tgz"}

// request is one task's validated parameters.
type request struct {
	src, dest, creates string
	remove             bool
	timeout            time.Duration
}

// absolute reports whether p is an absolute drive path with no ".." segment.
func absolute(p string) bool {
	return pathPattern.MatchString(p) && !strings.Contains(p, `\..\`) && !strings.HasSuffix(p, `\..`)
}

// readRequest validates a task's parameters.
func readRequest(params map[string]any) (request, error) {
	var r request
	var err error
	if r.src, err = sdk.RequiredStringParam(params, paramSrc); err != nil {
		return r, err
	}
	if !absolute(r.src) {
		return r, fmt.Errorf("src %q is not an absolute path such as C:\\Windows\\Temp\\go.zip, without quotes, wildcards or a \"..\" segment", r.src)
	}
	known := false
	for _, s := range archiveSuffixes {
		known = known || strings.HasSuffix(strings.ToLower(r.src), s)
	}
	if !known {
		return r, fmt.Errorf("src %q is not a .zip, .tar, .tar.gz or .tgz archive", r.src)
	}
	if r.dest, err = sdk.RequiredStringParam(params, paramDest); err != nil {
		return r, err
	}
	if !absolute(r.dest) && !driveRoot.MatchString(r.dest) {
		return r, fmt.Errorf("dest %q is not an absolute folder or a drive's root, without quotes, wildcards or a \"..\" segment", r.dest)
	}
	if r.creates, err = sdk.OptionalStringParam(params, paramCreates); err != nil {
		return r, err
	}
	if r.creates != "" && !absolute(r.creates) {
		return r, fmt.Errorf("creates %q is not an absolute path", r.creates)
	}
	if r.remove, err = sdk.BoolParamOr(params, paramRemove, false); err != nil {
		return r, err
	}
	seconds, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return r, err
	}
	if !set {
		seconds = defaultTimeout
	}
	if seconds <= 0 {
		return r, fmt.Errorf("timeout must be a positive number of seconds")
	}
	r.timeout = time.Duration(seconds) * time.Second
	return r, nil
}

// script is the PowerShell the host runs. Every value is one the
// parameters' patterns admit inside single quotes, where PowerShell reads
// nothing as syntax. tar.exe's own errors reach the task's output, which
// is why the error preference is relaxed around it. It answers key=value
// lines: skip (creates was there), missing or notdir (a check's unchecked
// answers), dest_existed, and extracted.
func (r request) script(check bool) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$src = '%s'
$dest = '%s'
$creates = '%s'
$remove = $%t
$check = $%t
if ($creates -ne '' -and (Test-Path -LiteralPath $creates)) { 'skip=creates'; exit 0 }
if (-not (Test-Path -LiteralPath $src -PathType Leaf)) { if ($check) { 'missing=src'; exit 0 }; exit %d }
$existed = Test-Path -LiteralPath $dest
if ($existed -and -not (Test-Path -LiteralPath $dest -PathType Container)) { if ($check) { 'notdir=dest'; exit 0 }; exit %d }
"dest_existed=$existed"
if ($check) { exit 0 }
if (-not $existed) { New-Item -ItemType Directory -Force -Path $dest | Out-Null }
$ErrorActionPreference = 'Continue'
& '%s' -xf $src -C $dest
$code = $LASTEXITCODE
$ErrorActionPreference = 'Stop'
if ($code -ne 0) { "tar=$code"; exit %d }
if ($remove) { Remove-Item -LiteralPath $src -Force }
'extracted=true'
`, r.src, r.dest, r.creates, r.remove, check, exitNoSrc, exitDestIsNot, tarPath, exitTar)
}

// Extract implements win.archive.extract.
func Extract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return extract(ctx, rc, device, params, collection.ModeExecute)
}

// CheckExtract is win.archive.extract's check: it reads creates, src and
// dest, and extracts nothing.
func CheckExtract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return extract(ctx, rc, device, params, collection.ModeCheck)
}

func extract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	r, err := readRequest(params)
	if err != nil {
		return fail(err)
	}
	shell, ok1 := device.(capability.WindowsShellCapable)
	winrm, ok2 := device.(capability.WinRMCapable)
	if device == nil || !ok1 || !ok2 {
		return fail(fmt.Errorf("the device is not a Windows host reached over WinRM"))
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return fail(err)
	}
	opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: r.timeout, PowerShellPath: shell.PowerShellPath()}, devicetls.For(device))
	res, err := run(ctx, winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}, auth, winrmexec.ShellPowerShell, r.script(mode == collection.ModeCheck), opts)
	if err != nil {
		return fail(err)
	}
	out := answers(res.Stdout)
	switch res.ExitCode {
	case 0:
	case exitNoSrc:
		return fail(fmt.Errorf("%s is not a file on %s", r.src, device.Name()))
	case exitDestIsNot:
		return fail(fmt.Errorf("%s is on %s and is not a folder", r.dest, device.Name()))
	case exitTar:
		return fail(fmt.Errorf("tar.exe could not extract %s (exit %s): %s", r.src, out["tar"], strings.TrimSpace(res.Stderr)))
	default:
		return fail(fmt.Errorf("the host's script exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout)))
	}
	switch {
	case out["missing"] == "src":
		return collection.Result{}, collection.CannotCheck(fmt.Sprintf("%s is not on the host yet; an earlier task in the run may put it there", r.src))
	case out["notdir"] == "dest":
		return collection.Result{}, collection.CannotCheck(fmt.Sprintf("%s is not a folder now; an earlier task in the run may change that", r.dest))
	}
	skipped := out["skip"] == "creates"
	if !skipped && out["dest_existed"] == "" {
		return fail(fmt.Errorf("the host did not report what it found: %q", res.Stdout))
	}
	changed := !skipped
	if mode == collection.ModeExecute && changed && out["extracted"] != "true" {
		return fail(fmt.Errorf("the host did not report the extraction: %q", res.Stdout))
	}
	existed := skipped || out["dest_existed"] == "True"
	for k, v := range map[string]any{statDest: r.dest, statExtracted: changed} {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{"dest_exists": existed}, After: map[string]any{"dest_exists": true}}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: changed}, nil
}

// answers reads the script's key=value lines.
func answers(stdout string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		if key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			out[key] = value
		}
	}
	return out
}

func extractDoc() collection.Doc {
	return collection.Doc{
		Summary: "Extracts a .zip, .tar or .tar.gz already on a Windows host into a folder there, over WinRM.",
		Description: "Extracts src into dest, making dest when it does not exist, with the tar.exe Windows ships, which reads zip as well as tar and refuses an entry whose path is absolute or climbs out with \"..\". This is community.windows.win_unzip, and archive.extract for a Windows host: src must already be on the host. " +
			"Idempotency is opt-in, as archive.extract's: naming creates skips extraction when that path is already there; naming none means every run extracts again and reports a change. remove deletes src once it is extracted. " +
			"Every path is absolute and held to a pattern with no quote, wildcard or \"..\" before WinRM is reached. A check reads creates, src and dest and extracts nothing; a src not there yet, or a dest that is not a folder, makes the call unchecked rather than failed, since an earlier task may change either.",
		Params: []collection.Param{
			{Name: paramSrc, Type: "string", Required: true, Description: "The archive on the host, an absolute path ending .zip, .tar, .tar.gz or .tgz."},
			{Name: paramDest, Type: "string", Required: true, Description: "The folder to extract into, an absolute path or a drive's root (C:\\), made when it does not exist."},
			{Name: paramCreates, Type: "string", Description: "An absolute path whose existence means the extraction already happened; when it is there, nothing is extracted and the task reports no change."},
			{Name: paramRemove, Type: "bool", Default: "false", Description: "Delete src once it has been extracted."},
			{Name: paramTimeout, Type: "int", Default: "600", Description: "How many seconds the extraction may take."},
		},
		Returns: []collection.ReturnField{
			{Name: statDest, Type: "string", Returned: "always", Description: "The folder extracted into."},
			{Name: statExtracted, Type: "bool", Returned: "always", Description: "Whether this task extracted (or, in a check, would)."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "Whether dest existed before this task and after it."},
		},
		Examples: []collection.Example{
			{Name: "Unpack Go, once", RunbookYAML: "- name: Unpack Go into C:\\go\n  win.archive.extract:\n    src: C:\\Windows\\Temp\\go1.26.9.windows-amd64.zip\n    dest: C:\\\n    creates: C:\\go\\bin\\go.exe\n"},
		},
		SeeAlso: []string{"win.file.download", "archive.extract"},
	}
}

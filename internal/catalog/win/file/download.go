// Package file implements the win.file.* methods: files on a Windows host,
// reached over WinRM. This file is "win.file.download": a file fetched
// onto the host and kept only when its SHA-256 matches.
package file

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "win.file.download",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("WindowsShellCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "A download writes a file, and a file it replaces is not kept; nothing here removes a file yet, so neither a new file nor a replaced one can be undone."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Downloads a file onto a Windows host over HTTPS and keeps it only if its SHA-256 matches.",
				Description: "Makes sure path on the host holds the file url serves, verified by sha256. This is ansible.windows.win_get_url with a checksum it will not run without. A file already at path with that digest reports no change and nothing is downloaded. Otherwise the host fetches url with the curl.exe Windows ships, over HTTPS only, redirects included, into a file beside path, and moves it into place only once its digest matches; a mismatch leaves path as it was and fails, naming the digest it got. A file at path with another digest is replaced. The download is the host's own, so url must be reachable from it; nothing passes through the machine running Pleiades. A check reads path's digest and downloads nothing.",
				Params: []collection.Param{
					{Name: "url", Type: "string", Required: true, Description: "The https:// URL to fetch. It may not hold a quote, a space or a control character.", Format: collection.ParamFormatURL},
					{Name: "path", Type: "string", Required: true, Description: "The absolute path on the host to write, as G:\\iso\\ubuntu.ova. Its folder must exist. It may not hold a quote, a wildcard or a control character."},
					{Name: "sha256", Type: "string", Required: true, Description: "The file's SHA-256, 64 hex digits, as the publisher lists it (Ubuntu's SHA256SUMS, for one)."},
					{Name: "timeout", Type: "int", Default: "1800", Description: "How many seconds the download may take."},
				},
				Returns: []collection.ReturnField{
					{Name: "path", Type: "string", Returned: "always", Description: "The file this task made sure of."},
					{Name: "sha256", Type: "string", Returned: "always", Description: "Its SHA-256, lower case."},
					{Name: "size_bytes", Type: "int", Returned: "always", Description: "Its size."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The SHA-256 at path before this task and after it, empty where there was no file."},
				},
				Examples: []collection.Example{
					{Name: "Fetch an Ubuntu cloud image", RunbookYAML: "- name: Fetch the Ubuntu 24.04 cloud image\n  win.file.download:\n    url: https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-amd64.ova\n    path: G:\\PleiadesLab\\media\\ubuntu-24.04-server-cloudimg-amd64.ova\n    sha256: 513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.import_ova"},
			},
		},
		Invoke: Download,
		Check:  CheckDownload,
	})
}

// Parameter and stat names.
const (
	paramURL     = "url"
	paramPath    = "path"
	paramSHA256  = "sha256"
	paramTimeout = "timeout"

	statPath   = "path"
	statSHA256 = "sha256"
	statSize   = "size_bytes"
)

// defaultTimeout is how long a download may take when the task does not say.
const defaultTimeout = 1800

// curlPath is the curl.exe Windows ships, by absolute path.
const curlPath = `C:\Windows\System32\curl.exe`

// The exit codes the download script gives for each way it can fail.
const (
	exitFetch    = 20
	exitMismatch = 21
	exitNoFolder = 22
)

// run is how the script reaches the host; a test replaces it.
var run = winrmexec.Run

// urlPattern is an https URL a single-quoted PowerShell string holds
// verbatim: no quote, space, backtick or control character. maxURL
// bounds its length.
var urlPattern = regexp.MustCompile(`^https://[^\s'"` + "`" + `\x00-\x1f\x7f]+$`)

// maxURL is the longest url accepted.
const maxURL = 2048

// pathPattern is an absolute drive path with no quote, wildcard, control
// character or trailing separator.
var pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^'"*?<>|\x00-\x1f\x7f]*[^'"*?<>|\\\x00-\x1f\x7f ]$`)

// digestPattern is a SHA-256 in hex.
var digestPattern = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

// Download implements "win.file.download".
func Download(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDownload(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDownload is "win.file.download"'s check: it reads path's digest
// and downloads nothing.
func CheckDownload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDownload(ctx, rc, device, params, collection.ModeCheck)
}

// download is one task's validated request.
type download struct {
	url, path, sha256 string
	timeout           time.Duration
}

// readDownload validates a task's parameters.
func readDownload(params map[string]any) (download, error) {
	var d download
	var err error
	if d.url, err = sdk.RequiredStringParam(params, paramURL); err != nil {
		return download{}, err
	}
	if len(d.url) > maxURL || !urlPattern.MatchString(d.url) {
		return download{}, fmt.Errorf("url %q is not an https:// URL without quotes, spaces or control characters", d.url)
	}
	if d.path, err = sdk.RequiredStringParam(params, paramPath); err != nil {
		return download{}, err
	}
	if !pathPattern.MatchString(d.path) || strings.Contains(d.path, `\..\`) || strings.HasSuffix(d.path, `\..`) {
		return download{}, fmt.Errorf("path %q is not an absolute path such as G:\\iso\\image.ova, without quotes, wildcards or a \"..\" segment", d.path)
	}
	if d.sha256, err = sdk.RequiredStringParam(params, paramSHA256); err != nil {
		return download{}, err
	}
	if !digestPattern.MatchString(d.sha256) {
		return download{}, fmt.Errorf("sha256 is not 64 hex digits")
	}
	d.sha256 = strings.ToLower(d.sha256)
	seconds, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return download{}, err
	}
	if !set {
		seconds = defaultTimeout
	}
	if seconds <= 0 {
		return download{}, fmt.Errorf("timeout must be a positive number of seconds")
	}
	d.timeout = time.Duration(seconds) * time.Second
	return d, nil
}

// script is the PowerShell the host runs. Every value is one the
// parameters' patterns admit inside single quotes, where PowerShell reads
// nothing as syntax. curl's own errors reach the task's output, which is
// why the error preference is relaxed around it: under Stop, the first
// line a native program writes to stderr would end the script.
func (d download) script(check bool) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$url = '%s'
$path = '%s'
$want = '%s'
$check = $%t
function Get-Sha256([string]$p) { (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant() }
$before = ''
if (Test-Path -LiteralPath $path -PathType Leaf) { $before = Get-Sha256 $path }
"before=$before"
if ($before -eq $want) { "after=$before"; "size=$((Get-Item -LiteralPath $path).Length)"; exit 0 }
if ($check) { exit 0 }
if (-not (Test-Path -LiteralPath (Split-Path -Parent $path) -PathType Container)) { exit %d }
$part = "$path.pleiades-part"
$ErrorActionPreference = 'Continue'
& '%s' --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --output $part $url
$fetched = $LASTEXITCODE
$ErrorActionPreference = 'Stop'
if ($fetched -ne 0) { Remove-Item -LiteralPath $part -Force -ErrorAction SilentlyContinue; "curl=$fetched"; exit %d }
$got = Get-Sha256 $part
if ($got -ne $want) { Remove-Item -LiteralPath $part -Force; "got=$got"; exit %d }
Move-Item -LiteralPath $part -Destination $path -Force
"after=$got"
"size=$((Get-Item -LiteralPath $path).Length)"
`, d.url, d.path, d.sha256, check, exitNoFolder, curlPath, exitFetch, exitMismatch)
}

// runDownload downloads url to path unless path already holds the file.
func runDownload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "win.file.download"
	d, err := readDownload(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", fqcn)
	}
	shell, ok1 := device.(capability.WindowsShellCapable)
	winrm, ok2 := device.(capability.WinRMCapable)
	if !ok1 || !ok2 {
		return collection.Result{}, fmt.Errorf("%s: device %q does not implement %s and %s", fqcn, device.Name(), capability.NameWindowsShell, capability.NameWinRM)
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: d.timeout, PowerShellPath: shell.PowerShellPath()}, devicetls.For(device))
	res, err := run(ctx, winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}, auth, winrmexec.ShellPowerShell, d.script(mode == collection.ModeCheck), opts)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	out := answers(res.Stdout)
	switch res.ExitCode {
	case 0:
	case exitNoFolder:
		return collection.Result{}, fmt.Errorf("%s: the folder %s does not exist on %s", fqcn, d.path[:strings.LastIndex(d.path, `\`)+1], device.Name())
	case exitFetch:
		return collection.Result{}, fmt.Errorf("%s: curl could not fetch %s (exit %s): %s", fqcn, d.url, out["curl"], strings.TrimSpace(res.Stderr))
	case exitMismatch:
		return collection.Result{}, fmt.Errorf("%s: %s has SHA-256 %s, not %s; nothing was written to %s", fqcn, d.url, out["got"], d.sha256, d.path)
	default:
		return collection.Result{}, fmt.Errorf("%s: the host's script exited %d: %s", fqcn, res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	before, ok := out["before"]
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: the host did not report the file's digest: %q", fqcn, res.Stdout)
	}
	changed := before != d.sha256
	after := out["after"]
	if mode == collection.ModeCheck && changed {
		after = d.sha256
	}
	stats := map[string]any{statPath: d.path, statSHA256: d.sha256}
	if size, err := strconv.ParseInt(out["size"], 10, 64); err == nil {
		stats[statSize] = size
	}
	for _, key := range []string{statPath, statSHA256, statSize} {
		if value, ok := stats[key]; ok {
			if err := rc.SetStat(key, value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{statSHA256: before}, After: map[string]any{statSHA256: after}}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
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

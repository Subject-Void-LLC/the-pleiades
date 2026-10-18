// The "win.file.copy" method: a project file copied to a Windows host over
// WinRM, kept only when its SHA-256 matches.
package file

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
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
		Name: "win.file.copy",
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
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "A copy writes a file, and a file it replaces is not kept; nothing here removes a file yet, so neither a new file nor a replaced one can be undone."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Copies a file from the project to a Windows host over WinRM and keeps it only if its SHA-256 matches.",
				Description: "Makes sure path on the host holds the project file src, byte for byte. This is ansible.windows.win_copy for one file. The file is read from the project (at most 128 MiB; never from .pleiades, never through a link) and its SHA-256 worked out here. A file already at path with that digest reports no change and nothing is sent. Otherwise the file travels on the WinRM command's standard input as base64, never on a command line, into a file beside path, and is moved into place only once its digest matches; a mismatch leaves path as it was and fails. A file at path with another digest is replaced. Only the command line reads a project's files, so a Walk-tier Runner refuses src. A check reads path's digest and sends nothing.",
				Params: []collection.Param{
					{Name: "src", Type: "string", Required: true, Description: "The file to copy, a slash-separated path relative to the project directory, as dist/tree.tar.gz."},
					{Name: "path", Type: "string", Required: true, Description: "The absolute path on the host to write, as C:\\pleiades\\tree.tar.gz. Its folder must exist. It may not hold a quote, a wildcard or a control character."},
					{Name: "timeout", Type: "int", Default: "1800", Description: "How many seconds the copy may take."},
				},
				Returns: []collection.ReturnField{
					{Name: "path", Type: "string", Returned: "always", Description: "The file this task made sure of."},
					{Name: "sha256", Type: "string", Returned: "always", Description: "Its SHA-256, lower case."},
					{Name: "size_bytes", Type: "int", Returned: "always", Description: "Its size."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The SHA-256 at path before this task and after it, empty where there was no file."},
				},
				Examples: []collection.Example{
					{Name: "Send a build to a Windows host", RunbookYAML: "- name: Copy the tools archive to the host\n  win.file.copy:\n    src: dist/tools.zip\n    path: C:\\ops\\tools.zip\n"},
				},
				SeeAlso: []string{"win.file.download", "file.copy"},
			},
		},
		Invoke: Copy,
		Check:  CheckCopy,
	})
}

// paramSrc names the project file to copy.
const paramSrc = "src"

// maxCopy bounds the file copied: it is read whole here, and sent whole.
const maxCopy = 128 << 20

// copyLine is how many bytes of the file one base64 line on stdin carries:
// a multiple of three, so no line but the last is padded, and well within
// what PowerShell reads as a line.
const copyLine = 48 << 10

// runWithStdin is how the write reaches the host; a test replaces it.
var runWithStdin = winrmexec.RunWithStdin

// Copy implements "win.file.copy".
func Copy(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCopy(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCopy is "win.file.copy"'s check: it reads path's digest and sends
// nothing.
func CheckCopy(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCopy(ctx, rc, device, params, collection.ModeCheck)
}

// copyRequest is one task's validated request, with the file it copies.
type copyRequest struct {
	src, path, sha256 string
	data              []byte
	timeout           time.Duration
}

// readCopy validates a task's parameters and reads src from the project.
func readCopy(rc sdk.RunbookContext, params map[string]any) (copyRequest, error) {
	var c copyRequest
	var err error
	if c.src, err = sdk.RequiredStringParam(params, paramSrc); err != nil {
		return copyRequest{}, err
	}
	if path.IsAbs(c.src) || path.Clean(c.src) != c.src || strings.HasPrefix(c.src, "../") || c.src == ".." {
		return copyRequest{}, fmt.Errorf("src %q is not a clean path inside the project, as dist/tools.zip", c.src)
	}
	if c.path, err = sdk.RequiredStringParam(params, paramPath); err != nil {
		return copyRequest{}, err
	}
	if !pathPattern.MatchString(c.path) || strings.Contains(c.path, `\..\`) || strings.HasSuffix(c.path, `\..`) {
		return copyRequest{}, fmt.Errorf("path %q is not an absolute path such as C:\\ops\\tools.zip, without quotes, wildcards or a \"..\" segment", c.path)
	}
	seconds, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return copyRequest{}, err
	}
	if !set {
		seconds = defaultTimeout
	}
	if seconds <= 0 {
		return copyRequest{}, errors.New("timeout must be a positive number of seconds")
	}
	c.timeout = time.Duration(seconds) * time.Second
	files, ok := rc.(sdk.ProjectFiles)
	if !ok {
		return copyRequest{}, sdk.ErrNoProjectFiles
	}
	if c.data, err = files.ReadProjectFile(c.src, maxCopy); err != nil {
		return copyRequest{}, fmt.Errorf("src: %w", err)
	}
	sum := sha256.Sum256(c.data)
	c.sha256 = hex.EncodeToString(sum[:])
	return c, nil
}

// readScript reports path's digest and size; it changes nothing.
func (c copyRequest) readScript() string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$path = '%s'
if (Test-Path -LiteralPath $path -PathType Leaf) {
  "before=$((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant())"
  "size=$((Get-Item -LiteralPath $path).Length)"
} else { "before=" }
`, c.path)
}

// writeScript reads the file from standard input, one base64 line at a
// time so it is never held whole, into a file beside path, and moves that
// into place only when its digest is the one asked for.
func (c copyRequest) writeScript() string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$path = '%s'
$want = '%s'
if (-not (Test-Path -LiteralPath (Split-Path -Parent $path) -PathType Container)) { exit %d }
$part = "$path.pleiades-part"
$out = [IO.File]::Open($part, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
try {
  while ($null -ne ($line = [Console]::In.ReadLine())) {
    if ($line.Length -gt 0) { $bytes = [Convert]::FromBase64String($line); $out.Write($bytes, 0, $bytes.Length) }
  }
} finally { $out.Dispose() }
$got = (Get-FileHash -LiteralPath $part -Algorithm SHA256).Hash.ToLowerInvariant()
if ($got -ne $want) { Remove-Item -LiteralPath $part -Force; "got=$got"; exit %d }
Move-Item -LiteralPath $part -Destination $path -Force
"after=$got"
"size=$((Get-Item -LiteralPath $path).Length)"
`, c.path, c.sha256, exitNoFolder, exitMismatch)
}

// stdin is the file as base64 lines.
func (c copyRequest) stdin() string {
	var b strings.Builder
	b.Grow(base64.StdEncoding.EncodedLen(len(c.data)) + len(c.data)/copyLine + 1)
	for i := 0; i < len(c.data); i += copyLine {
		b.WriteString(base64.StdEncoding.EncodeToString(c.data[i:min(i+copyLine, len(c.data))]))
		b.WriteByte('\n')
	}
	return b.String()
}

// runCopy copies src to path unless path already holds it.
func runCopy(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "win.file.copy"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if device == nil {
		return fail(errors.New("needs a target device"))
	}
	shell, ok1 := device.(capability.WindowsShellCapable)
	winrm, ok2 := device.(capability.WinRMCapable)
	if !ok1 || !ok2 {
		return fail(fmt.Errorf("device %q does not implement %s and %s", device.Name(), capability.NameWindowsShell, capability.NameWinRM))
	}
	c, err := readCopy(rc, params)
	if err != nil {
		return fail(err)
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return fail(err)
	}
	target := winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}
	opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: c.timeout, PowerShellPath: shell.PowerShellPath()}, devicetls.For(device))
	res, err := run(ctx, target, auth, winrmexec.ShellPowerShell, c.readScript(), opts)
	if err != nil {
		return fail(err)
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("reading %s exited %d: %s", c.path, res.ExitCode, strings.TrimSpace(res.Stderr)))
	}
	out := answers(res.Stdout)
	before, ok := out["before"]
	if !ok {
		return fail(fmt.Errorf("the host did not report the file's digest: %q", res.Stdout))
	}
	after := before
	changed := before != c.sha256
	if changed {
		after = c.sha256
	}
	if changed && mode == collection.ModeExecute {
		res, err := runWithStdin(ctx, target, auth, winrmexec.ShellPowerShell, c.writeScript(), c.stdin(), opts)
		if err != nil {
			return fail(err)
		}
		out = answers(res.Stdout)
		switch res.ExitCode {
		case 0:
		case exitNoFolder:
			return fail(fmt.Errorf("the folder %s does not exist on %s", c.path[:strings.LastIndex(c.path, `\`)+1], device.Name()))
		case exitMismatch:
			return fail(fmt.Errorf("the copy at %s has SHA-256 %s, not %s; nothing was written to it", c.path, out["got"], c.sha256))
		default:
			return fail(fmt.Errorf("the host's script exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)))
		}
		if out["after"] != c.sha256 {
			return fail(fmt.Errorf("the host reported %q after the copy, not %s", out["after"], c.sha256))
		}
	}
	stats := map[string]any{statPath: c.path, statSHA256: c.sha256, statSize: int64(len(c.data))}
	if size, err := strconv.ParseInt(out["size"], 10, 64); err == nil && !changed {
		stats[statSize] = size
	}
	for _, key := range []string{statPath, statSHA256, statSize} {
		if err := rc.SetStat(key, stats[key]); err != nil {
			return fail(err)
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{statSHA256: before}, After: map[string]any{statSHA256: after}}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: changed}, nil
}

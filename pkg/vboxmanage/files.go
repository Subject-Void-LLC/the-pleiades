// Files on the host: writing one from bytes Pleiades made, reading the end
// of one, and removing one. Each is a short PowerShell script whose data
// travels on its standard input, never on a command line.
package vboxmanage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// The first line of each script, which names what it does.
const (
	tagUpload = "# vboxmanage: upload"
	tagRead   = "# vboxmanage: read"
	tagRemove = "# vboxmanage: remove"
)

// maxUpload bounds what Upload sends: enough for a seed image, far short
// of an install image, which the host fetches itself.
const maxUpload = 16 << 20

// Upload writes data to path on the host. The bytes travel on the
// script's standard input as base64; the host checks their SHA-256
// before anything is written, writes them beside path, and moves them
// into place, so path never holds a partial file.
func (h Host) Upload(ctx context.Context, path string, data []byte) error {
	if err := CheckPath("file", path); err != nil {
		return err
	}
	if len(data) > maxUpload {
		return fmt.Errorf("vboxmanage: %d bytes is more than Upload sends", len(data))
	}
	sum := sha256.Sum256(data)
	script := tagUpload + `
$ErrorActionPreference = 'Stop'
$path = '` + path + `'
$want = '` + hex.EncodeToString(sum[:]) + `'
$data = [Convert]::FromBase64String([Console]::In.ReadToEnd().Trim())
$sha = [Security.Cryptography.SHA256]::Create()
$got = -join ($sha.ComputeHash($data) | ForEach-Object { $_.ToString('x2') })
if ($got -ne $want) { "got=$got"; exit 21 }
$part = "$path.pleiades-part"
[IO.File]::WriteAllBytes($part, $data)
Move-Item -LiteralPath $part -Destination $path -Force
"written=$($data.Length)"
`
	out, err := h.powerShell(ctx, script, base64.StdEncoding.EncodeToString(data))
	if err != nil {
		return err
	}
	if out.ExitCode == 21 {
		return fmt.Errorf("vboxmanage: %s arrived damaged (%s)", path, strings.TrimSpace(out.Stdout))
	}
	if out.ExitCode != 0 || !strings.Contains(out.Stdout, fmt.Sprintf("written=%d", len(data))) {
		return fmt.Errorf("vboxmanage: writing %s: %s", path, firstErrorLine(out))
	}
	return nil
}

// ErrNoFile is wrapped by ReadTail when the file does not exist.
var ErrNoFile = errors.New("vboxmanage: no such file")

// ReadTail returns up to the last limit bytes of the file at path, read
// while another process (a running VM writing its console) may still be
// writing it.
func (h Host) ReadTail(ctx context.Context, path string, limit int) ([]byte, error) {
	if err := CheckPath("file", path); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("vboxmanage: read limit %d", limit)
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = '%s'
$max = %d
if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { exit 3 }
$fs = [IO.File]::Open($path, 'Open', 'Read', 'ReadWrite')
try {
  $start = [Math]::Max([int64]0, $fs.Length - $max)
  $null = $fs.Seek($start, 'Begin')
  $buf = New-Object byte[] ($fs.Length - $start)
  $n = $fs.Read($buf, 0, $buf.Length)
} finally { $fs.Close() }
[Convert]::ToBase64String($buf, 0, $n)
`, tagRead, path, limit)
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	if out.ExitCode == 3 {
		return nil, fmt.Errorf("%w: %s", ErrNoFile, path)
	}
	if out.ExitCode != 0 {
		return nil, fmt.Errorf("vboxmanage: reading %s: %s", path, firstErrorLine(out))
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.Stdout))
	if err != nil {
		return nil, fmt.Errorf("vboxmanage: reading %s: the host's answer is not base64", path)
	}
	return data, nil
}

// Remove deletes the file at path, and then the folder holding it when
// folder is true and the folder is left empty. A file that is not there
// is not an error.
func (h Host) Remove(ctx context.Context, path string, folder bool) error {
	if err := CheckPath("file", path); err != nil {
		return err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = '%s'
$folder = $%t
if (Test-Path -LiteralPath $path -PathType Leaf) { Remove-Item -LiteralPath $path -Force }
$parent = Split-Path -Parent $path
if ($folder -and (Test-Path -LiteralPath $parent -PathType Container) -and -not (Get-ChildItem -LiteralPath $parent -Force | Select-Object -First 1)) { Remove-Item -LiteralPath $parent -Force }
"removed"
`, tagRemove, path, folder)
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("vboxmanage: removing %s: %s", path, firstErrorLine(out))
	}
	return nil
}

// powerShell runs script on the host through the runner.
func (h Host) powerShell(ctx context.Context, script, stdin string) (Output, error) {
	if h.Runner == nil {
		return Output{}, errors.New("vboxmanage: the host has no runner")
	}
	out, err := h.Runner.PowerShell(ctx, script, stdin)
	if err != nil {
		return Output{}, fmt.Errorf("vboxmanage: running PowerShell: %w", err)
	}
	return out, nil
}

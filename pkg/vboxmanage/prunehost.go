// The host calls a prune makes: the default machine folder, the folders and
// links under it, removing a folder, and forgetting a disk by UUID.
package vboxmanage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// The first line of the folder scripts, which names what each does.
const (
	tagFolders      = "# vboxmanage: folders"
	tagLinks        = "# vboxmanage: links"
	tagRemoveFolder = "# vboxmanage: remove folder"
)

// findLinks is a PowerShell function both folder scripts define: Find-Links
// writes the full path of every symbolic link, junction and mount point
// under a folder (each a reparse point), descending into the folders that
// are none and never into one, so a link is reported rather than followed.
// It reads with .NET's own enumeration, which sees hidden and system files
// and descends only where this function says to.
const findLinks = `function Find-Links([IO.DirectoryInfo]$top) {
  $todo = [Collections.Generic.Queue[IO.DirectoryInfo]]::new()
  $todo.Enqueue($top)
  while ($todo.Count -gt 0) {
    foreach ($e in $todo.Dequeue().EnumerateFileSystemInfos()) {
      if ($e.Attributes -band [IO.FileAttributes]::ReparsePoint) { $e.FullName }
      elseif ($e -is [IO.DirectoryInfo]) { $todo.Enqueue($e) }
    }
  }
}
`

// exitHoldsLink is the exit status of a removal refused because the folder
// is a link or holds one.
const exitHoldsLink = 4

// MachineFolder returns the account's default machine folder, as list
// systemproperties names it.
func (h Host) MachineFolder(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "list", "systemproperties")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "Default machine folder:"); ok {
			if folder := strings.TrimSpace(v); folder != "" {
				return folder, nil
			}
		}
	}
	return "", errors.New("vboxmanage: list systemproperties names no default machine folder")
}

// MachineFolders returns the folders directly inside root that hold a
// VM's settings file (a .vbox, or the .vbox-prev VirtualBox keeps beside
// it), or nothing at all, as a delete that removed a VM's disks but not
// its folder leaves one, as full paths; a root that is not there has none.
// Only such a folder is ever a prune's to remove, so a folder of anything
// else under the machine folder is never touched.
func (h Host) MachineFolders(ctx context.Context, root string) ([]string, error) {
	if err := CheckPath("folder", root); err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = %s
if (-not (Test-Path -LiteralPath $path -PathType Container)) { exit 3 }
Get-ChildItem -LiteralPath $path -Directory -Force | Where-Object { (Get-ChildItem -LiteralPath $_.FullName -File -Force | Where-Object { $_.Name -like '*.vbox' -or $_.Name -like '*.vbox-prev' } | Select-Object -First 1) -or -not (Get-ChildItem -LiteralPath $_.FullName -Force | Select-Object -First 1) } | ForEach-Object { $_.FullName }
`, tagFolders, winrmexec.QuotePS(root))
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	if out.ExitCode == 3 {
		return nil, nil
	}
	if out.ExitCode != 0 {
		return nil, fmt.Errorf("vboxmanage: listing %s: %s", root, firstErrorLine(out))
	}
	var folders []string
	for _, line := range strings.Split(out.Stdout, "\n") {
		if f := strings.TrimSpace(line); f != "" {
			folders = append(folders, f)
		}
	}
	return folders, nil
}

// Links returns the full path of every symbolic link, junction and mount
// point under root, none of them followed; a root that is not there has
// none. PlanPrune keeps whatever is at or under one.
func (h Host) Links(ctx context.Context, root string) ([]string, error) {
	if err := CheckPath("folder", root); err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = %s
if (-not (Test-Path -LiteralPath $path -PathType Container)) { exit 3 }
%sFind-Links (Get-Item -LiteralPath $path -Force)
`, tagLinks, winrmexec.QuotePS(root), findLinks)
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	if out.ExitCode == 3 {
		return nil, nil
	}
	if out.ExitCode != 0 {
		return nil, fmt.Errorf("vboxmanage: looking for links under %s: %s", root, firstErrorLine(out))
	}
	var links []string
	for _, line := range strings.Split(out.Stdout, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			links = append(links, l)
		}
	}
	return links, nil
}

// RemoveFolder deletes the folder at path and everything in it. A folder
// that is not there is not an error. One that is a link, or holds one
// anywhere inside, is refused and nothing in it is removed: what a link
// reaches can be anywhere, so it is not the machine folder's to delete.
// The refusal does not rest on how a recursive delete treats a link, which
// differs between tools and versions (Windows PowerShell 5.1.26100 was
// measured on 2026-11-13 to remove a junction without following it).
// PlanPrune leaves such a folder out; this is the same rule held again at
// the moment of removal.
func (h Host) RemoveFolder(ctx context.Context, path string) error {
	if err := CheckPath("folder", path); err != nil {
		return err
	}
	script := fmt.Sprintf(`%s
$ErrorActionPreference = 'Stop'
$path = %s
%sif (Test-Path -LiteralPath $path -PathType Container) {
  $top = Get-Item -LiteralPath $path -Force
  if ($top.Attributes -band [IO.FileAttributes]::ReparsePoint) { "link=$($top.FullName)"; exit %d }
  $link = Find-Links $top | Select-Object -First 1
  if ($link) { "link=$link"; exit %d }
  Remove-Item -LiteralPath $path -Recurse -Force
}
"removed"
`, tagRemoveFolder, winrmexec.QuotePS(path), findLinks, exitHoldsLink, exitHoldsLink)
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return err
	}
	if out.ExitCode == exitHoldsLink {
		return fmt.Errorf("vboxmanage: %s is a link or holds one (%s), so nothing in it is removed: a link can name a folder anywhere", path, farside.Text(strings.TrimPrefix(strings.TrimSpace(out.Stdout), "link=")))
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("vboxmanage: removing %s: %s", path, firstErrorLine(out))
	}
	return nil
}

// CloseDisk forgets the disk with uuid, deleting its file too when remove
// is true. VirtualBox refuses a disk a machine uses or one with children.
func (h Host) CloseDisk(ctx context.Context, uuid string, remove bool) error {
	if err := CheckUUID(uuid); err != nil {
		return err
	}
	args := []string{"closemedium", "disk", uuid}
	if remove {
		args = append(args, "--delete")
	}
	_, err := h.run(ctx, args...)
	return err
}

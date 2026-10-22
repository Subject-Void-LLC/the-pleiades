// `pleiades doc --create-skill`: writes SKILL.md, a skill an AI agent
// reads to learn how to drive The Pleiades, built from the same registry
// the rest of `pleiades doc` reads, so the methods it lists are exactly
// the ones this binary runs.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// skillFile is the file --create-skill writes, in the current directory.
const skillFile = "SKILL.md"

// skillMarker opens a line of every SKILL.md this command writes, just
// after its frontmatter and title. A SKILL.md without it there is
// somebody else's and is never overwritten; one with it is this command's
// own and is replaced, since rewriting it after an upgrade is the normal
// way to keep it current.
const skillMarker = "<!-- written by pleiades doc --create-skill"

// skillMarkerWithin is how many lines into the file the marker must start
// a line. The marker's own place is line 10; anywhere further down, it is
// most likely the skill's text quoted inside somebody else's file.
const skillMarkerWithin = 20

// createSkill writes SKILL.md into dir from entries and returns the path
// it wrote. Only built-in methods are listed: an external Collection's
// descriptions are third-party text, and a skill is text an agent follows
// as instructions, so putting them there would hand that third party a
// way to instruct the agent.
func createSkill(dir string, entries []catalogEntry) (string, error) {
	path := filepath.Join(dir, skillFile)
	// Lstat, not Stat: a link named SKILL.md would be replaced by the rename
	// below rather than written through, leaving the file it points at
	// stale while the command said it wrote the skill, and a dangling one
	// would read as nothing there at all.
	info, err := os.Lstat(path)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return "", fmt.Errorf("pleiades doc: %s is not a regular file (a link, a directory or a device); move it aside first", path)
	case err == nil:
		existing, err := os.ReadFile(path) // #nosec G304 -- a fixed file name in the directory the command was run from
		if err != nil {
			return "", fmt.Errorf("pleiades doc: reading %s: %w", path, err)
		}
		if !writtenBySkill(string(existing)) {
			return "", fmt.Errorf("pleiades doc: %s exists and was not written by --create-skill; move it aside first", path)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("pleiades doc: reading %s: %w", path, err)
	}
	// Write beside the target and rename over it, so a reader never sees
	// half a file and a failed write leaves the old one whole.
	tmp, err := os.CreateTemp(dir, ".SKILL.md.*")
	if err != nil {
		return "", fmt.Errorf("pleiades doc: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has happened
	if _, err := tmp.WriteString(skillMarkdown(entries)); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("pleiades doc: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("pleiades doc: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("pleiades doc: %w", err)
	}
	return path, nil
}

// writtenBySkill reports whether text is a SKILL.md this command wrote:
// it opens with this skill's frontmatter and a line near the top starts
// with the marker. Notes that only quote the skill further down are not.
func writtenBySkill(text string) bool {
	if !strings.HasPrefix(text, "---\nname: pleiades\n") {
		return false
	}
	for i, line := range strings.SplitN(text, "\n", skillMarkerWithin+1) {
		if i == skillMarkerWithin {
			break
		}
		if strings.HasPrefix(line, skillMarker) {
			return true
		}
	}
	return false
}

// skillMarkdown renders the skill: a frontmatter an agent's skill loader
// reads, how to work with the CLI safely, how a runbook is written, and
// one line for every implemented built-in method, grouped by namespace.
func skillMarkdown(entries []catalogEntry) string {
	var b strings.Builder
	b.WriteString(skillIntro)
	fmt.Fprintf(&b, "%s from pleiades %s. Run it again after an upgrade; it replaces this file. -->\n\n", skillMarker, version)
	b.WriteString(skillGuide)

	methods := make([]catalogEntry, 0, len(entries))
	for _, e := range entries {
		// Built-in, implemented methods only: a declared one refuses every
		// call, and an external one's text is not this binary's to vouch for.
		if e.Origin == "" && e.Manifest.Status == collection.StatusImplemented {
			methods = append(methods, e)
		}
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].FQCN < methods[j].FQCN })

	fmt.Fprintf(&b, "## Methods\n\nThe %d methods built into this binary. Check: whether `--mode check` can predict the method without changing anything. Undo: whether a run can record an undo for `pleiades rollback`. External Collections are not listed here; `pleiades doc --list` shows every method this project can call.\n", len(methods))
	namespace := ""
	for _, e := range methods {
		if ns, _, _ := strings.Cut(e.FQCN, "."); ns != namespace {
			namespace = ns
			fmt.Fprintf(&b, "\n### %s\n\n| Method | What it does | Check | Undo |\n| --- | --- | --- | --- |\n", namespace)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", e.FQCN, tableCell(e.Manifest.Doc.Summary), yesNo(e.Manifest.SupportsCheck), yesNo(e.Manifest.Reversibility.Reversible))
	}
	return b.String()
}

// tableCell makes text safe inside one Markdown table cell: one line, and
// no bare pipe to end the cell early.
func tableCell(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "(no summary yet)"
	}
	return strings.ReplaceAll(text, "|", `\|`)
}

// yesNo is a table's answer to a yes-or-no column.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// skillIntro is the skill's frontmatter and title. The frontmatter comes
// first in the file, where a skill loader looks for it.
const skillIntro = `---
name: pleiades
description: Drive The Pleiades automation CLI. Run one method against devices with pleiades adhoc --json, read a method's contract with pleiades doc, write and validate runbooks, check before changing, and roll a run back. Use when automating devices, servers, switches or clouds through pleiades, or when reading or writing a Pleiades runbook.
---

# The Pleiades

The Pleiades runs typed, namespaced methods (` + "`exec.command`, `pkg.apt.install`, `net.ios.config`" + `) against the devices in a
project's inventory, from the project directory. Every method declares what a device must be able to do, which
transports it reaches it over, whether it can be checked without changing anything, and whether it can be undone.
`

// skillGuide is how an agent should work with the CLI, and how a runbook
// is written. TestSkillGuide_NamesOnlyCommandsAndFlagsThisBinaryHas holds
// every command and flag it names to internal/clispec, and
// TestCLI_DocCreateSkillTeachesARunbookThatValidatesAndRuns validates its
// runbook through the real binary, warnings included, and runs it.
const skillGuide = "## Working safely\n\n" +
	"1. Read a method before calling it: `pleiades doc <method>` prints its parameters, what it returns and examples;\n" +
	"   `pleiades doc --json <method>` is the same contract as JSON. Never guess a parameter name.\n" +
	"2. Call one method once with `pleiades adhoc <hosts> <method> key=value ... --json`. `key:=yaml` passes a typed\n" +
	"   value (`argv:='[uname, -r]'`). `<hosts>` is a device name or a tag. Do not write a throwaway runbook for this.\n" +
	"3. Check before you change: add `--mode check` to `adhoc` or `run`. It reports what each task would change and\n" +
	"   changes nothing. A method with no check support is named as unchecked, and an incomplete check exits 3.\n" +
	"4. Read results from `--json`, one JSON document on standard output whose exit status matches the text view.\n" +
	"5. A run that changed something can usually be undone: `pleiades journal list`, then\n" +
	"   `pleiades rollback <run-id> --mode check`, then the same without `--mode check`.\n" +
	"6. Never put a secret in a parameter, a runbook or a command line. A device's credential is stored once\n" +
	"   (`pleiades add-credential <device> --username <name> --password-stdin`) and reaches its methods on its own.\n" +
	"7. A generic device starts discovered and runs nothing until `pleiades onboard <device>` records what it proved.\n\n" +
	"## Runbooks\n\n" +
	"A runbook is a YAML file under `runbooks/`. Validate it with `pleiades validate runbooks/<file>.yaml`, then run it\n" +
	"with `pleiades run runbooks/<file>.yaml --json`. How a runbook is written (`pleiades validate` warns when one\n" +
	"breaks either of the first two):\n\n" +
	"- A task names its method as its key, with the parameters under it. Never `fqcn:` with `params:`.\n" +
	"- The file opens with a comment saying what it does, its blast radius, what it changes, and how to undo it.\n" +
	"- `register: <name>` keeps a task's result; a later `when:` reads it as `result.<name>`.\n" +
	"- A list of one item is still written as a list.\n\n" +
	"```yaml\n" +
	skillRunbook +
	"```\n\n"

// skillRunbook is the runbook the skill teaches by. It reads only, so a
// reader who runs it as written changes nothing.
const skillRunbook = `# What this does: reads the kernel version on every web host, and the OS
# release where the kernel command succeeded.
# Blast radius: the hosts tagged web, read only.
# Changes: nothing; both commands only read.
# Undo: nothing to undo.
id: kernel_report
hosts: web
tasks:
  - name: Read the kernel version
    exec.command:
      cmd: uname -r
    register: kernel

  - name: Read the OS release where the kernel answered
    exec.command:
      cmd: cat /etc/os-release
    when:
      - result.kernel.rc == 0
`

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
	"slices"
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
	// The version check an agent can make itself: the skill describes the
	// binary that wrote it, and a different binary may differ.
	fmt.Fprintf(&b, "This skill describes pleiades %s. If `pleiades version` prints another version, run\n`pleiades doc --create-skill` again in this file's directory before relying on it.\n\n", version)
	b.WriteString(skillGuide)

	methods := make([]catalogEntry, 0, len(entries))
	for _, e := range entries {
		// Built-in, implemented methods only: a declared one refuses every
		// call, and an external one's text is not this binary's to vouch for.
		if e.Origin == "" && e.Manifest.Status == collection.StatusImplemented {
			methods = append(methods, e)
		}
	}
	// The safe path for a network change, taught only where the gate it
	// starts from is a method this binary has.
	if slices.ContainsFunc(methods, func(e catalogEntry) bool { return e.FQCN == "net.batfish.check" }) {
		b.WriteString(skillNetwork)
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
const skillGuide = "## Starting in a project\n\n" +
	"A command works on the project in the current directory, or the one its `--dir` names.\n\n" +
	"1. `pleiades init` makes a project: `inventory.yaml`, `runbooks/sample.yaml` and a README.\n" +
	"2. `pleiades add-host web01 --type linux_server --tags web --set host=10.0.0.5` adds a device. `--type` is its\n" +
	"   device type (`linux_server`, `windows_server`, `cisco_switch`, `container_host`, `generic_ssh` and others);\n" +
	"   `--tags` are what a runbook's `hosts:` and `adhoc`'s `<hosts>` can name besides the device itself. SSH on\n" +
	"   another port is `--set port=2222`. A `linux_server` added by `--type` runs commands and file methods, but\n" +
	"   package and account methods (`pkg.apt.*`, `pkg.dnf.*`, `identity.*`) need its family: add a Debian or Ubuntu\n" +
	"   server with `--classify linux_server,debian_family` and a RHEL, Rocky or Fedora one with\n" +
	"   `--classify linux_server,rhel_family`, in place of `--type`. Choose when adding it: `set-host` cannot change it.\n" +
	"3. `pleiades add-credential` stores a device's login, encrypted. Get the password from the person you work for:\n" +
	"   - as a sealed file, which never shows the password and needs no shell redirect:\n" +
	"     `pleiades add-credential web01 --username <name> --password-file <file>`. One sealed to this project's key\n" +
	"     (`pleiades seal-key` prints the public key the person seals to with `pleiades seal --to`) opens with it;\n" +
	"     one sealed to a passphrase also needs `--passphrase`, which the person types, or `--passphrase-stdin`.\n" +
	"   - or as a plaintext file: `pleiades add-credential web01 --username <name> --password-stdin < <file>`.\n" +
	"   Never write a password into a file of your own or a command line. It lands in `.pleiades/credentials.yaml`\n" +
	"   beside `.pleiades/master.key`, the key that decrypts it: never commit, copy or share `.pleiades/`.\n" +
	"4. An SSH device's host key must be trusted before the first connection, and an unknown key is refused.\n" +
	"   `pleiades trust-host web01 --first-connect` connects to the device's `host` and `port` and trusts every key\n" +
	"   it offers, unverified: ask first. Keys go in the file `PLEIADES_KNOWN_HOSTS` names, or else in\n" +
	"   `$HOME/.ssh/known_hosts` as pleiades sees `$HOME`, never in the project; `trust-host` prints the file it\n" +
	"   wrote, and every later connection reads the same one.\n" +
	"5. A `generic_*` device starts discovered and runs nothing until `pleiades onboard <device>` records what it\n" +
	"   proved it can do.\n" +
	"6. `pleiades adhoc web01 facts.gather --json` is a safe first call: it reads and changes nothing.\n\n" +
	"## Working safely\n\n" +
	"1. Read a method before calling it: `pleiades doc <method>` prints its parameters, what it returns and examples;\n" +
	"   `pleiades doc --json <method>` is the same contract as JSON. Never guess a parameter name.\n" +
	"2. Call one method once with `pleiades adhoc <hosts> <method> key=value ... --json`. `key:=yaml` passes a typed\n" +
	"   value (`argv:='[uname, -r]'`). `<hosts>` is a device name or a tag. Do not write a throwaway runbook for this.\n" +
	"3. Check before you change: add `--mode check` to `adhoc` or `run`. It reports what each task would change and\n" +
	"   changes nothing. A method with no check support is named as unchecked, and an incomplete check exits 3.\n" +
	"   A check reads each task against the device as it is now, so a task needing what an earlier one makes (a\n" +
	"   user whose group is created first) is unchecked, with the reason. Once the earlier task covers it, accept\n" +
	"   that gap by name: `--allow-unchecked <method>`.\n" +
	"4. Read results from `--json`, one JSON document on standard output whose exit status matches the text view.\n" +
	"   What each task returned is under `tasks[].stats`, with or without a `register:`.\n" +
	"5. Target the narrowest set the request allows: a device's own name, not its tag, when the work is about\n" +
	"   one device.\n" +
	"6. A run that changed something can usually be undone: `pleiades journal list`, then\n" +
	"   `pleiades rollback <run-id> --mode check`, then the same without `--mode check`. A check of a rollback\n" +
	"   sees the device as it is now, before any of the undo runs, so a warning that a later step would keep\n" +
	"   something may not hold once the steps before it have run.\n" +
	"7. To look at a device without changing it, use a method that only reads (`facts.gather`, `file.stat`) or\n" +
	"   any method with `--mode check`, which is never journaled. A command method (`exec.command`) reports\n" +
	"   changed on every run and is journaled, so a later rollback has to be told to step over it.\n" +
	"8. Never put a secret in a parameter, a runbook or a command line. A device's credential is stored once\n" +
	"   (`pleiades add-credential <device> --username <name> --password-file <file>`) and reaches its methods on its own.\n" +
	"9. `pleiades <command> --help` lists every flag a command takes, and exits 1 after printing it, which is not a\n" +
	"   failure. A command that takes a name needs one first: `pleiades add-host x --help` (it adds nothing).\n\n" +
	"## When something fails\n\n" +
	"- `outcome.status` in the `--json` report is `complete` only when everything succeeded. Otherwise read\n" +
	"  `outcome.message`, the `validation` list (what stopped the run before it started) and each task's `error`.\n" +
	"- A failed task with `\"unreached\": true` never reached its device: nothing ran there, so running it again is\n" +
	"  safe. A refused login is not unreached; fix the credential instead of retrying.\n" +
	"- A task's `status` is one of `ok`, `changed`, `would_change`, `checked_only`, `failed`, `held`, `skipped` and\n" +
	"  `unchecked`. `held` waits for a person's decision; do not work around it.\n" +
	"- A check that could not cover every task exits 3 and names the unchecked tasks. Treat their effect as unknown.\n" +
	"- After a failed run, `pleiades journal show <run-id>` says what each task did, and what can be undone.\n\n" +
	"## Keep a journal\n\n" +
	"Keep your own notes in `AGENT_JOURNAL.md` at the project's root, so a later session, yours or another agent's,\n" +
	"starts where you stopped instead of from nothing.\n\n" +
	"1. Read it first, if it exists: what the person asked for, what is done, what is left, and which runs to undo.\n" +
	"2. Write down the request as the person gave it, and each decision you make with its reason.\n" +
	"3. After every `adhoc` or `run`, append an entry: the date, the command, each task's `status`, and the outputs a\n" +
	"   later step needs (a version, an address, a path a task made). A run that is not a check also has a `run_id` in\n" +
	"   its `--json` report: write it, say whether the run changed anything, and mark it when it is rolled back.\n" +
	"   `pleiades rollback <run-id>` and `pleiades journal show <run-id>` take it; undo runs newest first, and\n" +
	"   only runs that changed something: a run that changed nothing, or only read, needs no undo.\n" +
	"4. End each session with what is left, so the next one can start from it.\n" +
	"5. Never write a password, token, key or the contents of a credential or sealed file into it; name the file.\n" +
	"   `pleiades journal list` is pleiades' own record of what ran; yours records why, and what comes next.\n\n" +
	"## Runbooks\n\n" +
	"A runbook is a YAML file under `runbooks/`. Validate it with `pleiades validate runbooks/<file>.yaml`, then run it\n" +
	"with `pleiades run runbooks/<file>.yaml --json`. How a runbook is written (`pleiades validate` warns when one\n" +
	"breaks either of the first two):\n\n" +
	"- A task names its method as its key, with the parameters under it. Never `fqcn:` with `params:`.\n" +
	"- The file opens with a comment saying what it does, its blast radius, what it changes, and how to undo it.\n" +
	"- `register: <name>` keeps a task's result; a later `when:` reads it as `result.<name>`.\n" +
	"- A list of one item is still written as a list.\n" +
	"- A command method (`exec.command`, `exec.shell`) cannot tell what its command did, so it reports `changed` on\n" +
	"  every run, even one that only reads, and a check of it is unchecked. To read a device, prefer a method that\n" +
	"  only reads, such as `facts.gather` with a `filter` list naming the facts (`ansible_kernel`,\n" +
	"  `ansible_distribution*`); a report needs no `register:`. The example below uses commands to show one.\n" +
	"- A file `file.copy` creates is owned by the account the task logs in as, mode 0600 (an existing file keeps\n" +
	"  its own), and a directory `file.directory` makes takes the device's umask, unless the task names `owner`,\n" +
	"  `group` and `mode`: name them for anything another account must read.\n\n" +
	"```yaml\n" +
	skillRunbook +
	"```\n\n"

// skillNetwork is how an agent changes a network: the model first, a
// rehearsal on the same OS image, then the change in a block that undoes
// itself, verified live. TestSkillGuide_NamesOnlyCommandsAndFlagsThisBinaryHas
// holds the commands and flags it names to internal/clispec too.
const skillNetwork = "## Changing a network\n\n" +
	"A routing or switching change goes through three lines of defence, in this order, whatever the network runs on\n" +
	"(real devices, containerlab, GNS3, CML2):\n\n" +
	"1. The model. Read each router as it runs (`net.frr.facts` with `include_config: true`, registered), write the\n" +
	"   candidate as a whole configuration file in the project, and run `net.batfish.check` on a `batfish` device with\n" +
	"   the running configurations, the candidate, assertions (`ospf_established`, `bgp_established`, `route`,\n" +
	"   `no_route`, `reach` with `accepted_by` for a virtual address, `routes_match`, `question`) and scenarios with\n" +
	"   links or nodes down. `pleiades doc net.batfish.check` has the whole contract. A failed check stops the change:\n" +
	"   report its `assertions`, `unrecognized` and `not_modelled`, and change nothing.\n" +
	"2. A rehearsal on lab devices running the same OS image as production. An FRR router is no stand-in for a Cisco\n" +
	"   change, and a model of one is no rehearsal of the other.\n" +
	"3. The change, in a block with `rollback_on_failure: true`: `net.frr.config` with `src:` the checked file and\n" +
	"   `expect_sha256:` the digest the check returned for it, then `wait.routes`, `net.trace`, and `routes_match`\n" +
	"   against facts read again, so the live network is held to the model it passed.\n\n" +
	"Rules:\n\n" +
	"- Never start Batfish, and never deploy or destroy a lab, without the person's say-so: each spends this\n" +
	"  machine's memory and CPU. A missing Batfish service or lab is reported and you ask; never skip the model.\n" +
	"- A model that agrees is evidence, not proof. It holds no runtime state (an IP SLA probe's answer, BFD's timing)\n" +
	"  and none of what its result lists under `not_modelled`, so the rehearsal and the live verification still run.\n" +
	"- Accept an unrecognised line or an unmodelled feature (`accept_unrecognized`) only with a reason the person\n" +
	"  would agree with, never to make a check pass.\n" +
	"- `pleiades add-host batfish --type batfish --set api_base=http://127.0.0.1:9996` adds the service.\n" +
	"  `pleiades import-containerlab --topology <lab dir>/topology-data.json --lab <name>` adds a containerlab lab's\n" +
	"  nodes; with `--username`, `--key`, `--trust-first-connect` and `--onboard` it also stores their login, trusts\n" +
	"  their host keys unverified (ask first) and onboards them. A lab makes new host keys on every deploy, so set\n" +
	"  `PLEIADES_KNOWN_HOSTS` to a file of the lab's own first, in every shell; the import refuses without it.\n" +
	"- `pleiades import-gns3 --server <url> --project <name>` and `pleiades import-cml --server <url> --lab <title>`\n" +
	"  add a GNS3 project's or a CML lab's nodes from their own APIs; a password comes from `--password-stdin` or\n" +
	"  `PLEIADES_GNS3_PASSWORD`/`PLEIADES_CML_PASSWORD`, never a flag. `import-cml` takes a node's address only from\n" +
	"  what the controller observed or from `--address NODE=IP`: never guess one from a configuration, ask.\n" +
	"- A device with no SSH (a GNS3 console, a lab router before SSH is enabled) is reached over Telnet, which sends\n" +
	"  its login in cleartext: a task sets `insecure_telnet: true` only when the person has accepted that for that\n" +
	"  device; never add it to make a task pass. The device's login and enable secret are stored with\n" +
	"  `pleiades add-credential` (`--enable-password-stdin` for the enable secret), never written in a runbook.\n" +
	"- Batfish runs as a container (`container.run` with `memory`, its port on 127.0.0.1 only): the runbook is in\n" +
	"  `docs/16-network-verification.md`, under Running Batfish. It takes a minute or two to answer.\n" +
	"- The account `net.frr.config` logs in as needs the `frrvty` group and write access to `/var/run/frr`; the method\n" +
	"  checks that before any change, and says so when it is missing.\n\n"

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

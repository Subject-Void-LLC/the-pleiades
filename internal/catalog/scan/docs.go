// The reference documentation of the scan methods, which
// internal/forge/catalogdata holds an identical copy of
// (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
package scan

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters the methods share.
var (
	hostKeyParam  = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}
	timeoutParam  = collection.Param{Name: paramTimeout, Type: "string", Default: "15m", Description: "How long the scanner may run, up to 2h, after which its container is killed and removed."}
	pullParam     = collection.Param{Name: paramPull, Type: "bool", Default: "true", Description: "Pull the scanner's pinned image when the engine does not have it, in a run: a check never pulls, and says it cannot check without the image. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs the image on its engine under that digest first."}
	ignoreParam   = collection.Param{Name: paramIgnore, Type: "string", Description: "An ignore file on the device: a YAML list of {id, reason, until}, every field required, until a date (YYYY-MM-DD, in UTC) no more than 366 days after the day of the run, since an end years away is no end. An entry suppresses its finding through its until day; past it, it suppresses nothing and the run warns, and an entry that matched nothing is warned about so the file shrinks as fixes land."}
	scannerReturn = collection.ReturnField{Name: statScanner, Type: "string", Returned: "always", Description: "The scanner's image, pinned by digest."}
	warnReturn    = collection.ReturnField{Name: sdk.StatWarnings, Type: "list of string", Returned: "when there is one", Description: "Ignore entries expired or unused, and anything else the run must say."}
)

// isolationNote is how every scanner runs.
const isolationNote = " The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket."

// vulnDoc is scan.vuln's reference documentation.
func vulnDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Lists the known vulnerabilities in an image archive, a directory or lockfiles, and fails at a severity.",
		Description: "Runs OSV-Scanner over an image archive (docker save), a directory (recursively, every lockfile and manifest it recognizes) or named lockfiles on the device, and reports each package exposed to each group of aliased advisories: its severity from the highest CVSS score (critical from 9.0, high from 7.0, medium from 4.0, low below), and the versions that fix it. A vulnerability at or above fail_on fails the task, and with fixable_only only one a fixed version exists for; one with no score is gated as high, since an advisory nobody has scored is not evidence it is harmless. Online, the scanner joins the engine's default network and sends osv.dev the names and versions of the packages it found; with offline_db it joins none and reads OSV-Scanner's local databases from the device. A directory holding an osv-scanner.toml at any depth, which OSV-Scanner would read as a list of vulnerabilities to ignore with no reason or end, refuses the scan, and none is read in any mode; a directory the search cannot read whole is refused too. A lockfile OSV-Scanner could not read or extract, which it leaves out with only a line on its standard error, fails the scan, named: what was not read was not scanned. It only reads, so a check is the scan a run makes and sends what a run sends (give offline_db for one that sends nothing), except that a check never pulls the scanner's image: without it, the check says it cannot check." + isolationNote,
		Params: []collection.Param{
			{Name: paramDir, Type: "string", Description: "A directory on the device to scan, recursively, whatever its .gitignore files list (see respect_gitignore). One of dir, image_archive and lockfiles."},
			{Name: paramImageArchive, Type: "string", Description: "An image archive on the device, as docker save or podman save writes one."},
			{Name: paramLockfiles, Type: "list of string", Description: "Lockfiles on the device to scan, each recognized by its name (package-lock.json, Cargo.lock, go.mod, requirements.txt and the rest OSV-Scanner reads)."},
			{Name: paramFailOn, Type: "string", Default: "high", Description: "The least severity that fails the task: critical, high, medium, low, or any (every finding, scored or not)."},
			{Name: paramFixableOnly, Type: "bool", Default: "false", Description: "Fail only on vulnerabilities a fixed version exists for; the rest are still reported."},
			{Name: paramGitignore, Type: "bool", Default: "false", Description: "Let the .gitignore files in dir narrow the scan, as git sees the tree. By default they do not: the runbook decides what is scanned, never the tree, so a directory a .gitignore lists (a vendored tree, a build output) is scanned all the same. OSV-Scanner itself never reads lockfiles inside node_modules either way, since the root lockfile already lists what npm installed there. Only with dir."},
			{Name: paramAllowEmpty, Type: "bool", Default: "false", Description: "Pass, with a warning, when OSV-Scanner finds no package to check. Without it that fails the task, since a scan that saw nothing (a wrong path, or files the scanner could not read) is no evidence of no vulnerability."},
			{Name: paramOfflineDB, Type: "string", Description: "A directory on the device holding OSV-Scanner's local databases (osv-scalibr/<ecosystem>/all.zip, as osv-scanner --download-offline-databases writes them); the scan then joins no network, and refuses an ecosystem it has no database for."},
			ignoreParam, timeoutParam, pullParam, hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statFindings, Type: "list of dict", Returned: "on success", Description: "Up to 1000 findings: package, version, ecosystem, id, aliases, severity, score, fixed, source (the file on the device it was found in), and ignored (the reason) when an ignore entry suppressed it."},
			{Name: statFailing, Type: "int", Returned: "on success", Description: "How many findings fail the gate, which is zero for a task that succeeded."},
			{Name: statCounts, Type: "dict", Returned: "on success", Description: "How many findings at each severity."},
			scannerReturn, warnReturn,
		},
		Examples: []collection.Example{
			{Name: "Gate a release on fixable high vulnerabilities", RunbookYAML: "- name: Known vulnerabilities\n  scan.vuln:\n    image_archive: /srv/release/app.tar\n    fail_on: high\n    fixable_only: true\n    ignore: /srv/app/scan-ignore.yaml\n"},
		},
		SeeAlso: []string{fqcnSBOM, "build.lock.verify"},
	}
}

// sbomDoc is scan.sbom's reference documentation.
func sbomDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Writes an SPDX 2.3 SBOM of an image archive or a directory, held to the NTIA minimum elements, with its SHA256SUMS.",
		Description: "Runs Syft over an image archive or a directory on the device with no network, names the SBOM's subject with the task's name and version, records the task's supplier on the subject alone (never on a dependency, whose supplier the operator does not speak for), and holds the result to the NTIA minimum elements: the document's author, timestamp and namespace, a subject it describes that is a package it holds, and every component's name, version, purl or CPE and a dependency relationship. An SBOM holding no component but its subject fails unless allow_empty. A dependency's supplier, which a generator rarely knows, is counted and warned about, and fails the task only with strict_suppliers. The SBOM (sbom.spdx.json) and a SHA256SUMS in sha256sum's format are written into dest, a new directory, so sha256sum -c verifies it anywhere; the undo removes the directory. A check generates and validates the SBOM as a run does, and writes and pulls nothing: without the scanner's image, the check says it cannot check." + isolationNote,
		Params: []collection.Param{
			{Name: paramDir, Type: "string", Description: "A directory on the device to describe. One of dir and image_archive."},
			{Name: paramImageArchive, Type: "string", Description: "An image archive on the device, as docker save or podman save writes one."},
			{Name: paramDest, Type: "string", Required: true, Description: "The directory to write, which must not exist, in one that does."},
			{Name: paramName, Type: "string", Required: true, Description: "The subject's name: what is being shipped."},
			{Name: paramVersion, Type: "string", Required: true, Description: "The subject's version."},
			{Name: paramSupplier, Type: "string", Required: true, Description: "Who supplies the subject, as SPDX writes it: \"Organization: name\" or \"Person: name\"."},
			{Name: paramStrictSuppliers, Type: "bool", Default: "false", Description: "Fail when a dependency has no supplier, rather than counting it."},
			{Name: paramAllowEmpty, Type: "bool", Default: "false", Description: "Write, with a warning, an SBOM holding no component but its subject. Without it that fails the task, since an SBOM of nothing (a wrong path, or files Syft could not read) meets every element and says nothing."},
			timeoutParam, pullParam, hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statDest, Type: "string", Returned: "always", Description: "The directory written."},
			{Name: statSBOM, Type: "string", Returned: "always", Description: "The SBOM's path on the device."},
			{Name: statSHA256, Type: "string", Returned: "always", Description: "The SBOM's SHA-256 in hex, as SHA256SUMS records it."},
			{Name: statComponents, Type: "int", Returned: "always", Description: "How many components the SBOM holds, the subject included."},
			{Name: statMissingSupplier, Type: "int", Returned: "always", Description: "How many dependencies have no supplier."},
			scannerReturn, warnReturn,
		},
		Examples: []collection.Example{
			{Name: "Describe a release's image", RunbookYAML: "- name: SBOM\n  scan.sbom:\n    image_archive: /srv/release/app.tar\n    dest: /srv/release/sbom-2.4.1\n    name: app\n    version: 2.4.1\n    supplier: \"Organization: Example Co\"\n"},
		},
		SeeAlso: []string{fqcnVuln, "file.remove"},
	}
}

// secretsDoc is scan.secrets' reference documentation.
func secretsDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Finds credentials leaked into a directory, naming each by file, line and rule, never by its value.",
		Description: "Runs Gitleaks over a directory on the device with its default rules and no network, and fails on every finding an ignore entry does not suppress, each named by its file (relative to dir), line, rule and fingerprint (file:rule:line, the id an ignore entry names). Gitleaks runs with --redact, so no value is in its report, and this reads only a finding's file, line and rule, so no value could be kept even if one were. A finding means rotating the credential, not only removing it. A file or directory Gitleaks could not read, which it skips with only a warning, fails the scan, named: what was not read was not scanned. A .gitleaks.toml or .gitleaksignore at the directory's root, which Gitleaks would read as its own rules or ignores, refuses the scan, and an inline gitleaks:allow comment suppresses nothing: what is scanned never decides what is reported. It only reads, so a check is the scan a run makes, except that a check never pulls the scanner's image: without it, the check says it cannot check." + isolationNote,
		Params: []collection.Param{
			{Name: paramDir, Type: "string", Required: true, Description: "The directory on the device to scan."},
			ignoreParam, timeoutParam, pullParam, hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statFindings, Type: "list of dict", Returned: "on success", Description: "Up to 1000 findings: file, line, rule, fingerprint, and ignored (the reason) when an ignore entry suppressed it."},
			{Name: statFailing, Type: "int", Returned: "on success", Description: "How many findings no ignore entry suppressed, which is zero for a task that succeeded."},
			scannerReturn, warnReturn,
		},
		Examples: []collection.Example{
			{Name: "No credential in the release tree", RunbookYAML: "- name: Leaked credentials\n  scan.secrets:\n    dir: /srv/release/app\n    ignore: /srv/app/scan-ignore.yaml\n"},
		},
		SeeAlso: []string{fqcnVuln},
	}
}

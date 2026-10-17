// This file holds the scan section of the catalog: scan.sbom,
// scan.secrets and scan.vuln, what a release ships, asked on the device
// that holds it. The Doc literals are copies of internal/catalog/scan's
// docs.go, kept equal by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var scanCollections = []collectionscaffold.Config{
	{
		Name:          "scan.sbom",
		Capabilities:  []capability.Name{capability.NameCommandExec, capability.NameContainerEngine},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Writes an SPDX 2.3 SBOM of an image archive or a directory, held to the NTIA minimum elements, with its SHA256SUMS.",
			Description: "Runs Syft over an image archive or a directory on the device with no network, names the SBOM's subject with the task's name and version, records the task's supplier on the subject alone (never on a dependency, whose supplier the operator does not speak for), and holds the result to the NTIA minimum elements: the document's author, timestamp and namespace, a subject it describes that is a package it holds, and every component's name, version, purl or CPE and a dependency relationship. An SBOM holding no component but its subject fails unless allow_empty. A dependency's supplier, which a generator rarely knows, is counted and warned about, and fails the task only with strict_suppliers. The SBOM (sbom.spdx.json) and a SHA256SUMS in sha256sum's format are written into dest, a new directory, so sha256sum -c verifies it anywhere; the undo removes the directory. A check generates and validates the SBOM as a run does and writes nothing. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.",
			Params: []collection.Param{
				{Name: "dir", Type: "string", Description: "A directory on the device to describe. One of dir and image_archive."},
				{Name: "image_archive", Type: "string", Description: "An image archive on the device, as docker save or podman save writes one."},
				{Name: "dest", Type: "string", Required: true, Description: "The directory to write, which must not exist, in one that does."},
				{Name: "name", Type: "string", Required: true, Description: "The subject's name: what is being shipped."},
				{Name: "version", Type: "string", Required: true, Description: "The subject's version."},
				{Name: "supplier", Type: "string", Required: true, Description: "Who supplies the subject, as SPDX writes it: \"Organization: name\" or \"Person: name\"."},
				{Name: "strict_suppliers", Type: "bool", Default: "false", Description: "Fail when a dependency has no supplier, rather than counting it."},
				{Name: "allow_empty", Type: "bool", Default: "false", Description: "Write, with a warning, an SBOM holding no component but its subject. Without it that fails the task, since an SBOM of nothing (a wrong path, or files Syft could not read) meets every element and says nothing."},
				{Name: "timeout", Type: "string", Default: "15m", Description: "How long the scanner may run, up to 2h, after which its container is killed and removed."},
				{Name: "pull", Type: "bool", Default: "true", Description: "Pull the scanner's pinned image when the engine does not have it. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs it loaded first."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The directory written."},
				{Name: "sbom", Type: "string", Returned: "always", Description: "The SBOM's path on the device."},
				{Name: "sha256", Type: "string", Returned: "always", Description: "The SBOM's SHA-256 in hex, as SHA256SUMS records it."},
				{Name: "components", Type: "int", Returned: "always", Description: "How many components the SBOM holds, the subject included."},
				{Name: "missing_supplier", Type: "int", Returned: "always", Description: "How many dependencies have no supplier."},
				{Name: "scanner", Type: "string", Returned: "always", Description: "The scanner's image, pinned by digest."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "Ignore entries expired or unused, and anything else the run must say."},
			},
			Examples: []collection.Example{
				{Name: "Describe a release's image", RunbookYAML: "- name: SBOM\n  scan.sbom:\n    image_archive: /srv/release/app.tar\n    dest: /srv/release/sbom-2.4.1\n    name: app\n    version: 2.4.1\n    supplier: \"Organization: Example Co\"\n"},
			},
			SeeAlso: []string{"scan.vuln", "file.remove"},
		},
	},
	{
		Name:          "scan.secrets",
		Capabilities:  []capability.Name{capability.NameCommandExec, capability.NameContainerEngine},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Finds credentials leaked into a directory, naming each by file, line and rule, never by its value.",
			Description: "Runs Gitleaks over a directory on the device with its default rules and no network, and fails on every finding an ignore entry does not suppress, each named by its file (relative to dir), line, rule and fingerprint (file:rule:line, the id an ignore entry names). Gitleaks runs with --redact, so no value is in its report, and this reads only a finding's file, line and rule, so no value could be kept even if one were. A finding means rotating the credential, not only removing it. A file or directory Gitleaks could not read, which it skips with only a warning, fails the scan, named: what was not read was not scanned. A .gitleaks.toml or .gitleaksignore at the directory's root, which Gitleaks would read as its own rules or ignores, refuses the scan, and an inline gitleaks:allow comment suppresses nothing: what is scanned never decides what is reported. It only reads, so a check of it is the task itself. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.",
			Params: []collection.Param{
				{Name: "dir", Type: "string", Required: true, Description: "The directory on the device to scan."},
				{Name: "ignore", Type: "string", Description: "An ignore file on the device: a YAML list of {id, reason, until}, every field required, until a date (YYYY-MM-DD). An entry suppresses its finding through its until day; past it, it suppresses nothing and the run warns, and an entry that matched nothing is warned about so the file shrinks as fixes land."},
				{Name: "timeout", Type: "string", Default: "15m", Description: "How long the scanner may run, up to 2h, after which its container is killed and removed."},
				{Name: "pull", Type: "bool", Default: "true", Description: "Pull the scanner's pinned image when the engine does not have it. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs it loaded first."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "findings", Type: "list of dict", Returned: "on success", Description: "Up to 1000 findings: file, line, rule, fingerprint, and ignored (the reason) when an ignore entry suppressed it."},
				{Name: "failing", Type: "int", Returned: "on success", Description: "How many findings no ignore entry suppressed, which is zero for a task that succeeded."},
				{Name: "scanner", Type: "string", Returned: "always", Description: "The scanner's image, pinned by digest."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "Ignore entries expired or unused, and anything else the run must say."},
			},
			Examples: []collection.Example{
				{Name: "No credential in the release tree", RunbookYAML: "- name: Leaked credentials\n  scan.secrets:\n    dir: /srv/release/app\n    ignore: /srv/app/scan-ignore.yaml\n"},
			},
			SeeAlso: []string{"scan.vuln"},
		},
	},
	{
		Name:          "scan.vuln",
		Capabilities:  []capability.Name{capability.NameCommandExec, capability.NameContainerEngine},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Lists the known vulnerabilities in an image archive, a directory or lockfiles, and fails at a severity.",
			Description: "Runs OSV-Scanner over an image archive (docker save), a directory (recursively, every lockfile and manifest it recognizes) or named lockfiles on the device, and reports each package exposed to each group of aliased advisories: its severity from the highest CVSS score (critical from 9.0, high from 7.0, medium from 4.0, low below), and the versions that fix it. A vulnerability at or above fail_on fails the task, and with fixable_only only one a fixed version exists for; one with no score is gated as high, since an advisory nobody has scored is not evidence it is harmless. Online, the scanner joins the engine's default network to reach osv.dev; with offline_db it joins none and reads OSV-Scanner's local databases from the device. A directory holding an osv-scanner.toml at any depth, which OSV-Scanner would read as a list of vulnerabilities to ignore with no reason or end, refuses the scan, and none is read in any mode; a directory the search cannot read whole is refused too. It only reads, so a check of it is the task itself. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.",
			Params: []collection.Param{
				{Name: "dir", Type: "string", Description: "A directory on the device to scan, recursively. One of dir, image_archive and lockfiles."},
				{Name: "image_archive", Type: "string", Description: "An image archive on the device, as docker save or podman save writes one."},
				{Name: "lockfiles", Type: "list of string", Description: "Lockfiles on the device to scan, each recognized by its name (package-lock.json, Cargo.lock, go.mod, requirements.txt and the rest OSV-Scanner reads)."},
				{Name: "fail_on", Type: "string", Default: "high", Description: "The least severity that fails the task: critical, high, medium, low, or any (every finding, scored or not)."},
				{Name: "fixable_only", Type: "bool", Default: "false", Description: "Fail only on vulnerabilities a fixed version exists for; the rest are still reported."},
				{Name: "allow_empty", Type: "bool", Default: "false", Description: "Pass, with a warning, when OSV-Scanner finds no package to check. Without it that fails the task, since a scan that saw nothing (a wrong path, or files the scanner could not read) is no evidence of no vulnerability."},
				{Name: "offline_db", Type: "string", Description: "A directory on the device holding OSV-Scanner's local databases (osv-scalibr/<ecosystem>/all.zip, as osv-scanner --download-offline-databases writes them); the scan then joins no network, and refuses an ecosystem it has no database for."},
				{Name: "ignore", Type: "string", Description: "An ignore file on the device: a YAML list of {id, reason, until}, every field required, until a date (YYYY-MM-DD). An entry suppresses its finding through its until day; past it, it suppresses nothing and the run warns, and an entry that matched nothing is warned about so the file shrinks as fixes land."},
				{Name: "timeout", Type: "string", Default: "15m", Description: "How long the scanner may run, up to 2h, after which its container is killed and removed."},
				{Name: "pull", Type: "bool", Default: "true", Description: "Pull the scanner's pinned image when the engine does not have it. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs it loaded first."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "findings", Type: "list of dict", Returned: "on success", Description: "Up to 1000 findings: package, version, ecosystem, id, aliases, severity, score, fixed, source (the file on the device it was found in), and ignored (the reason) when an ignore entry suppressed it."},
				{Name: "failing", Type: "int", Returned: "on success", Description: "How many findings fail the gate, which is zero for a task that succeeded."},
				{Name: "counts", Type: "dict", Returned: "on success", Description: "How many findings at each severity."},
				{Name: "scanner", Type: "string", Returned: "always", Description: "The scanner's image, pinned by digest."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "Ignore entries expired or unused, and anything else the run must say."},
			},
			Examples: []collection.Example{
				{Name: "Gate a release on fixable high vulnerabilities", RunbookYAML: "- name: Known vulnerabilities\n  scan.vuln:\n    image_archive: /srv/release/app.tar\n    fail_on: high\n    fixable_only: true\n    ignore: /srv/app/scan-ignore.yaml\n"},
			},
			SeeAlso: []string{"scan.sbom", "build.lock.verify"},
		},
	},
}

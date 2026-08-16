// Command helm-lint proves the Helm chart in helm/the-pleiades renders, that
// what it renders is hardened, and that the configurations it is supposed to
// refuse really are refused.
//
// WHY THIS EXISTS RATHER THAN JUST `helm lint`. `helm lint` checks that a
// chart is well formed: the YAML parses, the values match values.schema.json,
// the required Chart.yaml fields are present. It has no opinion whatsoever
// about what the templates produce, so a chart that renders a root container
// with no probes and an image tagged latest passes it cleanly. Every rule this
// tool adds is a mistake this repository has already made once, in a chart or
// in an image:
//
//   - An image tagged latest, or with no tag, or pinned by digest. The digest
//     case is the surprising one: a digest-pinned reference does NOT resolve
//     against a side-loaded image even when the local daemon reports that
//     exact digest, so digest pinning breaks every air-gapped install, every
//     `kind load docker-image`, and the exact install path the documentation
//     gate requires.
//   - A container with no probes, or with liveness and readiness pointed at
//     one path. The stock chart shipped the second: /healthz and /readyz
//     answer different questions, and pointing liveness at the readiness
//     endpoint turns a database blip into a restart loop across every replica.
//   - A container that is not demonstrably non-root, with a numeric uid.
//     Kubernetes cannot verify a non-numeric image USER and refuses the pod
//     with CreateContainerConfigError, which Compose cannot see at all, so
//     this class of bug ships happily until somebody installs into a cluster.
//   - A hostPath volume anywhere, which is how a pod reaches the node's
//     container runtime socket. That is the escape the chart's own Ansible
//     refusal is about, expressed here as a rule rather than a paragraph.
//
// And the negative half, which is the part a linter usually cannot do: the
// refusals in templates/_validations.tpl are asserted by really running helm
// against a values set that should be refused and requiring both a non-zero
// exit and the reason in the message. A refusal nobody exercises is a comment.
//
// Usage: go run ./tools/helm-lint
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// chartDir is the one chart this repository ships, relative to the repository
// root.
const chartDir = "helm/the-pleiades"

// minimumHelmMajor is the Helm major version this tool and this chart were
// written and verified against.
//
// Pinned to a major rather than an exact build because that is the boundary
// where `helm template`'s output contract and the lint rule set change. A
// newer Helm 4 patch is fine; Helm 3 is not tested here, and silently checking
// with an untested renderer would make a pass mean less than it appears to.
const minimumHelmMajor = 4

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "helm-lint:", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	chart := filepath.Join(repoRoot, chartDir)
	if _, err := os.Stat(filepath.Join(chart, "Chart.yaml")); err != nil {
		return fmt.Errorf("no chart at %s: %w", chartDir, err)
	}

	if err := checkHelmVersion(); err != nil {
		return err
	}

	var findings []finding
	containers := 0

	for _, p := range profiles {
		args := append([]string{"lint", "--strict", chart}, valuesFor(p)...)
		if out, err := runHelm(args); err != nil {
			return fmt.Errorf("helm lint failed for profile %q:\n%s", p.name, out)
		}

		rendered, err := runHelm(append([]string{"template", "helm-lint", chart}, valuesFor(p)...))
		if err != nil {
			return fmt.Errorf("helm template failed for profile %q:\n%s", p.name, rendered)
		}
		objects, err := decodeManifests(rendered)
		if err != nil {
			return fmt.Errorf("parsing the render of profile %q: %w", p.name, err)
		}
		containers += countContainers(objects)
		findings = append(findings, checkManifests(p.name, objects, p.probeScheme)...)
		findings = append(findings, checkNames(p.name, objects)...)
		findings = append(findings, checkIngressPaths(p.name, objects)...)
		findings = append(findings, checkDisruptionBudgets(p.name, objects)...)
		findings = append(findings, requirePodDisruptionBudgets(p.name, objects, p.wantDisruptionBudgets)...)
		findings = append(findings, checkRetainedDataStamp(p.name, objects)...)
	}

	renders, err := checkNamesAtEveryLegalReleaseLength(chart, &findings)
	if err != nil {
		return err
	}

	// The one rule that reads Go source: a container is allowed to ship
	// without probes only while the written reason for that still describes
	// the binary inside it. See probewaiver.go.
	stale, err := checkProbeWaiversStillHold(repoRoot)
	if err != nil {
		return err
	}
	findings = append(findings, stale...)

	for _, r := range refusals {
		args := []string{"template", "helm-lint", chart}
		if !r.omitBase {
			args = append(args, baseValues...)
		}
		args = append(args, r.values...)

		out, err := runHelm(args)
		switch {
		case err == nil:
			findings = append(findings, finding{
				profile: "refusals", object: r.name,
				message: "rendered successfully. This configuration is supposed to be refused, and a refusal nobody exercises is a comment.",
			})
		case !strings.Contains(out, r.wantMessage):
			findings = append(findings, finding{
				profile: "refusals", object: r.name,
				message: fmt.Sprintf("was refused, but the message does not mention %q, so the reason has gone missing from it. Helm said:\n%s", r.wantMessage, indent(out)),
			})
		}
	}

	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "helm-lint: %d finding(s) in %s:\n\n", len(findings), chartDir)
		for _, f := range findings {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		return errors.New("helm-lint check failed")
	}

	// The probe count is still stated as "%d of %d" with the waived count
	// beside it, even though objects.go's waiver table is now empty and the
	// second number reads 0. Printing "all containers carry both probes"
	// instead would be a claim about the table rather than about the render,
	// and the day somebody adds a waiver back the summary has to say so
	// without anybody remembering to change this line.
	fmt.Printf("helm-lint: %s renders in %d configuration(s); %d containers checked (non-root numeric uid, read-only root, no capabilities, RuntimeDefault seccomp, tagged image), %d of them carrying both probes and %d waived by a written reason this run re-checked against the source it depends on; %d configurations refused; %d renders at release-name lengths %v with every object name asserted distinct and legal for its own kind, including the pod names, revision-hash labels and DNS records Kubernetes derives from a StatefulSet's\n",
		chartDir, len(profiles), containers, containers-len(profiles)*len(probeWaivers), len(profiles)*len(probeWaivers), len(refusals), renders, nameProbeLengths)
	return nil
}

// checkNamesAtEveryLegalReleaseLength renders the chart once per release-name
// length in nameProbeLengths (twice, for the fullname helper's two branches)
// and appends what the names prove.
//
// It returns the number of renders so the summary line can state it. A count
// of zero would mean this loop quietly did nothing, which is precisely the
// failure a summary line printing "all names distinct" would hide.
func checkNamesAtEveryLegalReleaseLength(chart string, findings *[]finding) (int, error) {
	renders := 0
	for _, length := range nameProbeLengths {
		for _, release := range releaseNamesOfLength(length) {
			// The default values, deliberately: they render all four
			// workloads, which is what a name collision needs in order to be
			// visible at all.
			args := append([]string{"template", release, chart}, baseValues...)
			rendered, err := runHelm(args)
			if err != nil {
				return renders, fmt.Errorf("helm template failed for a release name of %d characters (%s):\n%s", length, release, rendered)
			}
			objects, err := decodeManifests(rendered)
			if err != nil {
				return renders, fmt.Errorf("parsing the render for release name %q: %w", release, err)
			}
			renders++
			*findings = append(*findings, checkNames(fmt.Sprintf("release name of %d characters (%s)", length, release), objects)...)
		}
	}
	return renders, nil
}

// valuesFor is one profile's full argument list: the three values the chart
// requires, plus that profile's own.
func valuesFor(p profile) []string {
	return append(append([]string{}, baseValues...), p.values...)
}

// checkHelmVersion refuses to run against a Helm this chart was not verified
// with, and refuses to skip when helm is missing.
//
// Skipping would be worse than failing. A check that quietly does nothing when
// its tool is absent reports success on a machine where it proved nothing, and
// the first time anybody notices is when a chart bug reaches a cluster.
func checkHelmVersion() error {
	out, err := runHelm([]string{"version", "--short"})
	if err != nil {
		return fmt.Errorf("helm is not runnable (%v). Install Helm %d or newer: https://helm.sh/docs/intro/install/", err, minimumHelmMajor)
	}

	// `helm version --short` prints something like "v4.2.4+g3900f43".
	version := strings.TrimPrefix(strings.TrimSpace(out), "v")
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return fmt.Errorf("could not read a major version out of %q", strings.TrimSpace(out))
	}
	if major < minimumHelmMajor {
		return fmt.Errorf("helm %s is older than the version this chart is verified against (%d.x). A pass here would not mean what it appears to", strings.TrimSpace(out), minimumHelmMajor)
	}
	return nil
}

// runHelm invokes helm and returns its combined output, which is where helm
// writes a template error.
func runHelm(args []string) (string, error) {
	// #nosec G204 -- "helm" is a fixed literal and every argument comes from
	// profiles.go's own fixed profile and refusal tables plus the chart path
	// derived from os.Getwd, never from user input.
	cmd := exec.Command("helm", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// decodeManifests splits one `helm template` output into its objects.
func decodeManifests(rendered string) ([]manifest, error) {
	var objects []manifest
	decoder := yaml.NewDecoder(strings.NewReader(rendered))
	for {
		var obj manifest
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			return objects, nil
		}
		if err != nil {
			return nil, err
		}
		// A document holding only comments decodes to an empty object. Helm's
		// output is full of them (every "# Source:" header sits in one).
		if obj.Kind == "" {
			continue
		}
		objects = append(objects, obj)
	}
}

// countContainers reports how many containers the checks actually looked at,
// so the success line states a number rather than an adjective.
func countContainers(objects []manifest) int {
	total := 0
	for _, obj := range objects {
		if !workloadKinds[obj.Kind] {
			continue
		}
		total += len(obj.Spec.Template.Spec.Containers) + len(obj.Spec.Template.Spec.InitContainers)
	}
	return total
}

// indent shifts a block of helm output right so it reads as quoted text inside
// a finding rather than as this tool's own output.
func indent(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = "      " + line
	}
	return strings.Join(lines, "\n")
}

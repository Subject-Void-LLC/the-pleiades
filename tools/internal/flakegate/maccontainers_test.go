// Tests that every container package is either run on the Mac against Podman or
// has a written reason to stay on the machine that runs the rest of the gate.
package flakegate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// macContainerPackages captures the Makefile's list of container packages that
// run on the Mac.
var macContainerPackages = regexp.MustCompile(`(?ms)^MAC_CONTAINER_PACKAGES :=(.*?)\n\n`)

// staysOnLinux says, for each container package that does not run on the Mac,
// why. Reasons are from the container-test mapping of 2026-10-26 and from the
// trial run on a Mac under Podman; a reason that stops being true is the
// moment to move a package, with a run on the Mac that shows it passing.
var staysOnLinux = map[string]string{
	"cmd/controller":                      "its setup release gate uses compose; not confirmed to run under Podman",
	"cmd/pleiades":                        "privileged k3s, docker-in-docker and network-lab fixtures, a systemd cgroup gate and the Docker socket",
	"cmd/runner":                          "privileged fixtures and the Docker socket; external Collections load on Linux only",
	"internal/adapters/legacy":            "starts Docker-only fixtures",
	"internal/adapters/native":            "docker-in-docker and Podman host fixtures, and the Docker socket",
	"internal/archtest":                   "listed as container-dependent for a reason not yet read; move only after reading it",
	"internal/catalog/cloud/aws/ec2":      "LocalStack needs LOCALSTACK_AUTH_TOKEN, which the Mac does not hold",
	"internal/catalog/cloud/aws/s3":       "LocalStack needs LOCALSTACK_AUTH_TOKEN, which the Mac does not hold",
	"internal/catalog/container":          "asserts Docker's identity on /var/run/docker.sock; Podman answers differently",
	"internal/catalog/container/engine":   "asserts Docker's identity on /var/run/docker.sock; Podman answers differently",
	"internal/catalog/container/image":    "uses the Docker socket directly",
	"internal/catalog/container/network":  "uses the Docker socket directly",
	"internal/catalog/container/registry": "uses the Docker socket directly",
	"internal/catalog/container/volume":   "uses the Docker socket directly",
	"internal/catalog/file":               "listed as container-dependent for a reason not yet read; move only after reading it",
	"internal/catalog/net":                "privileged FRR and Open vSwitch routers on user-defined bridges",
	"internal/catalog/net/batfish":        "Batfish is a JVM service for amd64 and runs privileged routers",
	"internal/catalog/net/frr":            "privileged FRR routers on user-defined bridges",
	"internal/catalog/net/ovs":            "Open vSwitch kernel module in a privileged container",
	"internal/catalog/scan":               "uses the Docker socket directly",
	"internal/catalog/wait":               "privileged network-lab fixtures",
	"internal/ent/migrate/gen":            "its files are excluded by build constraints on macOS (setup failed there)",
	"internal/inventory/onboard":          "k3s, docker-in-docker and network-lab fixtures",
	"internal/inventory/plugins":          "LocalStack needs LOCALSTACK_AUTH_TOKEN, which the Mac does not hold",
	"internal/inventory/plugins/aws":      "LocalStack needs LOCALSTACK_AUTH_TOKEN, which the Mac does not hold",
	"internal/testsupport":                "its tests start docker-in-docker, Podman, k3s and network-lab fixtures",
	"pkg/awscloud":                        "LocalStack needs LOCALSTACK_AUTH_TOKEN, which the Mac does not hold",
	"pkg/batfish":                         "Batfish is a JVM service for amd64",
	"pkg/containerengine":                 "uses the Docker socket directly",
	"pkg/gns3":                            "needs NET_ADMIN and a GNS3 server image",
	"pkg/iso9660":                         "privileged, for loop devices",
	"pkg/kubeapi":                         "privileged k3s",
	"tests/e2e":                           "builds the shipped images and runs compose and kind",
}

// TestEveryContainerPackageIsClassified holds the Mac's list to the Makefile's
// container list: every package the Mac runs is a container package, none is
// also written down as staying on Linux, and every other container package
// has a reason for staying. A container package added to
// DOCKER_DEPENDENT_PACKAGES fails here until it is placed, so a new one is
// never left out of both halves of the container pass (FAILURE_PATTERNS 424: a
// part nobody runs reads as a part that passed).
func TestEveryContainerPackageIsClassified(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "Makefile")))
	if err != nil {
		t.Fatal(err)
	}
	all := dockerDependentPackages.FindSubmatch(raw)
	mac := macContainerPackages.FindSubmatch(raw)
	if all == nil || mac == nil {
		t.Fatal("the Makefile no longer assigns DOCKER_DEPENDENT_PACKAGES and MAC_CONTAINER_PACKAGES as blocks; update this test with it")
	}
	const prefix = "github.com/Subject-Void-LLC/the-pleiades/"
	docker := map[string]bool{}
	for _, p := range packagePath.FindAllString(string(all[1]), -1) {
		docker[strings.TrimPrefix(p, prefix)] = true
	}
	onMac := map[string]bool{}
	for _, p := range packagePath.FindAllString(string(mac[1]), -1) {
		onMac[strings.TrimPrefix(p, prefix)] = true
	}
	if len(docker) == 0 || len(onMac) == 0 {
		t.Fatalf("parsed %d container packages and %d for the Mac, so this test would pass vacuously", len(docker), len(onMac))
	}
	for p := range onMac {
		if !docker[p] {
			t.Errorf("%s is in MAC_CONTAINER_PACKAGES but not in DOCKER_DEPENDENT_PACKAGES", p)
		}
		if why, stays := staysOnLinux[p]; stays {
			t.Errorf("%s runs on the Mac and is also written down as staying on Linux (%s): pick one", p, why)
		}
	}
	for p := range docker {
		if !onMac[p] && staysOnLinux[p] == "" {
			t.Errorf("%s is a container package that neither runs on the Mac nor has a reason to stay on Linux: add it to MAC_CONTAINER_PACKAGES after a run on the Mac, or to staysOnLinux with its reason", p)
		}
	}
	for p := range staysOnLinux {
		if !docker[p] {
			t.Errorf("staysOnLinux names %s, which is not a container package: the entry is stale", p)
		}
	}
}

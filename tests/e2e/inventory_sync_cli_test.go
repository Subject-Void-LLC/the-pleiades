//go:build integration

package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// This gate proves `pleiades inventory sync --plugin aws` works, through
// the real binary, against a real (emulated) AWS API, from a project
// directory holding nothing but the files a user would have.
//
// It exists because that exact command was broken from the day the AWS
// plugin landed until this branch, and nothing caught it. The plugin had
// a package suite, a LocalStack integration suite and a shared
// conformance suite, all green, and every one of them constructed the
// plugin with constructor options no production code passed. The CLI
// built it through the registry's own constructor instead, so region was
// "" and the credential store was nil, and Connect refused every time.
// See FAILURE_PATTERNS.md.
//
// The lesson RULE 0 already states, in the one shape that was still
// missing here: a suite that constructs its subject differently from the
// product is testing an arrangement no user can reach. So this gate
// composes nothing. It shells out to the binary with the flags a person
// would type and reads the file they would read afterwards.
func TestInventorySyncAWSThroughTheRealCLI(t *testing.T) {
	token := testsupport.LocalStackToken(t)

	ctx := context.Background()
	ctr, err := localstack.Run(ctx, testsupport.LocalStackImage,
		testcontainers.WithEnv(map[string]string{"LOCALSTACK_AUTH_TOKEN": token}),
		testsupport.LocalStackReady(),
	)
	if err != nil {
		t.Fatalf("starting localstack: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	endpoint := localStackEndpoint(t, ctr)

	// One real EC2 instance for the sync to find. LocalStack accepts any
	// non-empty static credential, and this container is torn down with
	// the test, so the key pair below is a fixture rather than a secret.
	const (
		region    = "us-east-1"
		accessKey = "test"
		secretKey = "test"
		hostName  = "pleiades-cli-sync-gate"
	)
	client, err := awscloud.New(region, accessKey, secretKey, "", awscloud.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("awscloud.New: %v", err)
	}
	if _, err := client.RunInstance(ctx, hostName, "ami-12345678", "t2.micro"); err != nil {
		t.Fatalf("RunInstance: %v", err)
	}

	bin := buildPleiadesCLI(t)
	project := t.TempDir()
	if err := inv.WriteHosts(filepath.Join(project, "inventory.yaml"), nil); err != nil {
		t.Fatalf("creating the empty project inventory: %v", err)
	}

	// The credential the plugin resolves, stored the way a user stores
	// it. The name defaults to the plugin's own, so no --credential flag
	// is needed here and none is passed, which is the documented common
	// case rather than a shortcut this test takes.
	runCLI(t, bin, project, "add-credential", "aws",
		"--username", accessKey, "--password", secretKey, "--dir", project)

	out := runCLI(t, bin, project, "inventory", "sync",
		"--plugin", "aws",
		"--set", "region="+region,
		"--endpoint", endpoint,
		"--dir", project)

	if !strings.Contains(out, "added") {
		t.Fatalf("sync output did not report a reconciliation:\n%s", out)
	}

	// The file a user would open afterwards, read through the same
	// parser the platform uses rather than by grepping YAML text.
	hosts, err := inv.ReadHosts(filepath.Join(project, "inventory.yaml"))
	if err != nil {
		t.Fatalf("reading the synced inventory: %v", err)
	}

	var foundInstance bool
	for _, h := range hosts {
		if h.Name == hostName {
			foundInstance = true
		}
	}
	if !foundInstance {
		t.Fatalf("the synced inventory does not contain the EC2 instance %q; it holds %d host(s): %+v", hostName, len(hosts), hosts)
	}
}

// TestInventorySyncAWSWithoutARegionFailsWithAUsableMessage proves the
// other half: the refusal a user gets when they forget the setting names
// the setting, rather than naming a Go constructor option they have no
// way to call.
//
// It needs no container, because it never reaches AWS. That is the
// point: syncplugin.Open refuses before anything dials.
func TestInventorySyncAWSWithoutARegionFailsWithAUsableMessage(t *testing.T) {
	bin := buildPleiadesCLI(t)
	project := t.TempDir()

	cmd := exec.Command(bin, "inventory", "sync", "--plugin", "aws", "--dir", project)
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("sync with no region succeeded, want a refusal:\n%s", out)
	}

	text := string(out)
	if !strings.Contains(text, "region") {
		t.Errorf("refusal does not name the missing setting:\n%s", text)
	}
	// The old message named a Go function nobody outside this module can
	// call. Asserting its absence is what keeps the fix from regressing
	// into a differently-worded version of the same dead end.
	if strings.Contains(text, "WithRegion") {
		t.Errorf("refusal still points at a constructor option a user cannot call:\n%s", text)
	}
}

// buildPleiadesCLI builds the real command binary into a temp directory.
//
// It builds its own rather than reusing harness_test.go's TestMain, which
// builds only the controller and runner: adding a third target there
// would slow every e2e run for two tests, and this one is skipped
// entirely without a LocalStack token anyway.
func buildPleiadesCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pleiades")
	build := exec.Command("go", "build", "-o", path, "github.com/Subject-Void-LLC/the-pleiades/cmd/pleiades")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building cmd/pleiades: %v\n%s", err, out)
	}
	return path
}

// runCLI runs one pleiades subcommand in dir and returns its combined
// output, failing the test on a non-zero exit.
func runCLI(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// localStackEndpoint resolves the container's mapped API address.
func localStackEndpoint(t *testing.T, ctr *localstack.LocalStackContainer) string {
	t.Helper()
	ctx := context.Background()
	port, err := ctr.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatalf("localstack mapped port: %v", err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("localstack host: %v", err)
	}
	return "http://" + host + ":" + port.Port()
}

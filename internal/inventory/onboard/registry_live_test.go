// Live test for registry accounts: a real private registry, reached through
// an engine session, receives its own account and no other host's.
package onboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registryImage is the private registry the test pushes to and pulls from.
const registryImage = "registry:2.8.3"

// registryRC is a run holding a device's credential and one registry
// account, answering for that account's host only, as both tiers' run
// contexts do (wire.RegistryCredential.For).
type registryRC struct {
	liveRC
	registry *wire.RegistryCredential
}

func (r registryRC) RegistryCredential(host string) (string, string, bool) {
	return r.registry.For(host)
}

// TestRegistryAuth_OnlyItsOwnHostReceivesTheAccount pushes to and pulls
// from a real private registry through a session: with the account the
// registry accepts both, anonymously it refuses, and an account stored for
// another host is never sent. Nothing is written to the engine host.
func TestRegistryAuth_OnlyItsOwnHostReceivesTheAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine and a real registry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	net, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = net.Remove(context.Background()) })

	hash, err := bcrypt.GenerateFromPassword([]byte("registry-secret-9"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          registryImage,
			Networks:       []string{net.Name},
			NetworkAliases: map[string][]string{net.Name: {"registry"}},
			Env: map[string]string{
				"REGISTRY_AUTH":                "htpasswd",
				"REGISTRY_AUTH_HTPASSWD_REALM": "pleiades test",
				"REGISTRY_AUTH_HTPASSWD_PATH":  "/auth/htpasswd",
			},
			Files: []testcontainers.ContainerFile{{
				Reader: strings.NewReader("deployer:" + string(hash) + "\n"), ContainerFilePath: "/auth/htpasswd", FileMode: 0o644,
			}},
			WaitingFor: wait.ForLog("listening on").WithStartupTimeout(testsupport.ContainerStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		if reg != nil {
			_ = reg.Terminate(context.Background())
		}
		t.Fatalf("starting the registry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Terminate(context.Background()) })

	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{
		Env:      map[string]string{"DOCKERD_EXTRA_ARGS": "--insecure-registry=registry:5000"},
		Networks: []string{net.Name},
	})
	trustFixture(t, d)
	d.StartContainer(t, liveGateImage, "")
	props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
	secrets := passwordFor(testsupport.DockerHostUser)
	device, _ := onboarded(t, container.TypeHost, props, secrets)

	const ref = "registry:5000/pleiades/alpine:gate"
	open := func(rc registryRC) *containerengine.Session {
		s, err := containerengine.Open(ctx, rc, device, nil, "test.registry",
			containerengine.OpImageTag, containerengine.OpImagePush, containerengine.OpImagePull, containerengine.OpImageRemove)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(s.Close)
		return s
	}
	push := func(rc registryRC) error {
		s := open(rc)
		auth, err := containerengine.RegistryAuth(rc, ref)
		if err != nil {
			return err
		}
		resp, err := s.API.ImagePush(ctx, ref, client.ImagePushOptions{RegistryAuth: auth})
		if err != nil {
			return err
		}
		for msg, err := range resp.JSONMessages(ctx) {
			if err != nil {
				return err
			}
			if msg.Error != nil {
				return fmt.Errorf("%s", msg.Error.Message)
			}
		}
		return nil
	}
	pull := func(rc registryRC) error {
		s := open(rc)
		auth, err := containerengine.RegistryAuth(rc, ref)
		if err != nil {
			return err
		}
		resp, err := s.API.ImagePull(ctx, ref, client.ImagePullOptions{RegistryAuth: auth})
		if err != nil {
			return err
		}
		return resp.Wait(ctx)
	}

	right := registryRC{liveRC: liveRC{secrets: secrets}, registry: &wire.RegistryCredential{Host: "registry:5000", Username: "deployer", Password: "registry-secret-9"}}
	anonymous := registryRC{liveRC: liveRC{secrets: secrets}}
	elsewhere := registryRC{liveRC: liveRC{secrets: secrets}, registry: &wire.RegistryCredential{Host: "ghcr.io", Username: "deployer", Password: "registry-secret-9"}}

	s := open(right)
	if _, err := s.API.ImageTag(ctx, client.ImageTagOptions{Source: liveGateImage, Target: ref}); err != nil {
		t.Fatalf("ImageTag: %v", err)
	}
	if err := push(anonymous); err == nil {
		t.Fatal("an anonymous push to a registry that requires an account succeeded")
	}
	if err := push(elsewhere); err == nil {
		t.Fatal("a push carrying another host's account succeeded, so the account reached a registry it does not name")
	}
	if err := push(right); err != nil {
		t.Fatalf("push with the registry's own account: %v", err)
	}
	if _, err := s.API.ImageRemove(ctx, ref, client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("ImageRemove: %v", err)
	}
	if err := pull(anonymous); err == nil {
		t.Fatal("an anonymous pull from a registry that requires an account succeeded")
	}
	if err := pull(right); err != nil {
		t.Fatalf("pull with the registry's own account: %v", err)
	}
	// The account went in the request's header and nowhere on the host: no
	// CLI credential file, and neither the password nor the header's
	// encoded value anywhere the engine or a login could have written it,
	// the engine's own state and logs included.
	for _, path := range []string{"/root/.docker/config.json", "/home/testuser/.docker/config.json"} {
		if out, err := d.RootExec(ctx, "test", "-e", path); err == nil {
			t.Errorf("%s exists on the engine host: %s", path, out)
		}
	}
	header, err := containerengine.RegistryAuth(right, ref)
	if err != nil {
		t.Fatal(err)
	}
	// Regular files only: a recursive grep opens FIFOs too, and reading one
	// blocks until the test's deadline. The size is in k because the host's
	// busybox find refuses an M suffix, and an error here would read as
	// "nothing found" (which the canary below is for).
	search := func(needle string) string {
		out, err := d.RootExec(ctx, "sh", "-c", `find /etc /root /home /tmp /var /run -type f -size -65536k -exec grep -lF -- "$1" {} + 2>/dev/null || true`, "sh", needle)
		if err != nil {
			t.Fatalf("searching the engine host: %v", err)
		}
		return strings.TrimSpace(out)
	}
	// The search must be able to find something, or an empty answer below
	// proves nothing: a canary planted in the engine's own state directory,
	// a separate volume in this image, is found first.
	if _, err := d.RootExec(ctx, "sh", "-c", `printf 'canary-7f3a' > /var/lib/docker/pleiades-canary`); err != nil {
		t.Fatal(err)
	}
	if found := search("canary-7f3a"); !strings.Contains(found, "/var/lib/docker/pleiades-canary") {
		t.Fatalf("the disk search did not find a planted canary (answered %q), so it cannot show the account's absence", found)
	}
	for _, needle := range []string{"registry-secret-9", header} {
		if found := search(needle); found != "" {
			t.Errorf("the registry account is on the engine host's disk, in:\n%s", found)
		}
	}
}

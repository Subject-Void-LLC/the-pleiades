// Live tests for onboarding a Kubernetes cluster: a real k3s API server,
// reached with a token and with a client certificate, at the oldest and
// newest supported minors.
package onboard

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestKubernetesProbe_EachSupportedMinor onboards a cluster at the oldest
// and newest supported minors with the administrator's client
// certificate, and a namespace's ServiceAccount with a token: each records
// the server's version and kinds, and its own rules, and only the
// administrator sees the nodes.
func TestKubernetesProbe_EachSupportedMinor(t *testing.T) {
	if testing.Short() {
		t.Skip("starts real k3s servers")
	}
	for _, image := range []string{testsupport.K3sOldestImage, testsupport.K3sNewestImage} {
		t.Run(image, func(t *testing.T) {
			k := testsupport.StartK3s(t, testsupport.K3sOptions{Image: image})
			minor := strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(image, "rancher/k3s:"), "-", 2)[0], "v")
			minor = minor[:strings.LastIndex(minor, ".")]
			props := map[string]inventory.PropertyValue{
				kubernetes.APIServerProperty: k.Server,
				devicetls.CAPEMProperty:      k.CAPEM,
			}

			admin := map[string]string{wire.SecretCertificatePEM: k.ClientCert, wire.SecretPrivateKeyPEM: k.ClientKey}
			_, got := onboarded(t, kubernetes.TypeCluster, props, admin)
			if !slices.Contains(got.Capabilities, capability.NameKubernetes) {
				t.Fatalf("capabilities %v lack KubernetesCapable", got.Capabilities)
			}
			if v, _ := got.Facts[FactKubernetesVersion].(string); !strings.HasPrefix(v, "v"+minor+".") {
				t.Errorf("version %q, want v%s.x", v, minor)
			}
			kinds, _ := got.Facts[FactKinds].([]any)
			// Every served version, not only the preferred one: autoscaling
			// serves HorizontalPodAutoscaler at v1 and v2, and a manifest
			// at either is served.
			for _, want := range []string{"apps/v1 Deployment", "v1 Pod", "batch/v1 CronJob",
				"autoscaling/v1 HorizontalPodAutoscaler", "autoscaling/v2 HorizontalPodAutoscaler"} {
				if !slices.Contains(kinds, any(want)) {
					t.Errorf("kinds lack %q", want)
				}
			}
			if _, truncated := got.Facts[FactKindsTruncated]; truncated {
				t.Errorf("a k3s server's %d kinds were recorded as truncated", len(kinds))
			}
			if nodes, _ := got.Facts[FactNodes].([]any); len(nodes) != 1 {
				t.Errorf("the administrator saw %d nodes, want the one node: %v", len(nodes), got.Facts[FactNodes])
			}

			ctx := context.Background()
			if _, err := k.Kubectl(ctx, "create", "namespace", "team"); err != nil {
				t.Fatal(err)
			}
			if _, err := k.Kubectl(ctx, "create", "role", "deployer", "-n", "team", "--verb=get,list,create,patch", "--resource=deployments.apps,configmaps"); err != nil {
				t.Fatal(err)
			}
			token := k.ServiceAccountToken(t, "team", "deployer", "")
			// ServiceAccountToken binds a cluster role; this one is bound to
			// the namespace's own role instead.
			if _, err := k.Kubectl(ctx, "create", "rolebinding", "deployer-role", "-n", "team", "--role=deployer", "--serviceaccount=team:deployer"); err != nil {
				t.Fatal(err)
			}
			props[kubernetes.NamespaceProperty] = "team"
			_, got = onboarded(t, kubernetes.TypeCluster, props, map[string]string{wire.SecretBearerToken: token})
			rules := fmt.Sprint(got.Facts[FactRules])
			if !strings.Contains(rules, "get,list,create,patch on apps/deployments") {
				t.Errorf("the namespace credential's rules %s lack its role", rules)
			}
			if _, saw := got.Facts[FactNodes]; saw {
				t.Errorf("a namespace credential recorded nodes: %v", got.Facts[FactNodes])
			}
		})
	}
}

// TestKubernetesProbe_RefusesWhatIsNotItsCredential proves a refused token
// grants nothing, and a password or an SSH key is refused by name before
// the server is asked.
func TestKubernetesProbe_RefusesWhatIsNotItsCredential(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})
	props := map[string]inventory.PropertyValue{kubernetes.APIServerProperty: k.Server, devicetls.CAPEMProperty: k.CAPEM}
	prober, _ := Lookup(kubernetes.TypeCluster)
	device := build(t, kubernetes.TypeCluster, props)
	for name, tc := range map[string]struct {
		secrets map[string]string
		want    string
	}{
		"a token the server does not know": {map[string]string{wire.SecretBearerToken: "not-a-real-token"}, "refused this credential"},
		"a password":                       {map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "x"}, "password"},
		"an SSH key":                       {map[string]string{wire.SecretPrivateKeyPEM: "-----BEGIN OPENSSH PRIVATE KEY-----"}, "SSH key"},
		"nothing":                          {map[string]string{}, "no credential"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prober.Probe(context.Background(), device, tc.secrets); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Probe = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

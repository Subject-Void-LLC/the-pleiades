// Tests of Open, the one way a Kubernetes method reaches its cluster, and of
// the namespace grammar every namespace a task or a record names is held to.
package kubeapi

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// openDevice is a cluster device that says whether onboarding granted it
// KubernetesCapable.
type openDevice struct {
	clusterDevice
	onboarded bool
}

func (d openDevice) HasCapability(name capability.Name) bool {
	return d.onboarded && name == capability.NameKubernetes
}

// tokenRC is a run that hands a method one cluster token.
type tokenRC struct{ secrets map[string]string }

func (r tokenRC) InjectSecrets() map[string]string { return r.secrets }
func (tokenRC) SetStat(string, any) error          { return nil }
func (tokenRC) EmitFact(string, any) error         { return nil }

// TestOpen refuses a task with no target and a cluster onboarding never
// granted, refuses a namespace parameter that is not a namespace name, and
// otherwise builds a session for the device's API server, in the task's
// namespace, with the token the run handed over.
func TestOpen(t *testing.T) {
	cluster := openDevice{clusterDevice: clusterDevice{fakeItem: fakeItem{name: "c1"}, server: "https://10.0.0.1:6443"}, onboarded: true}
	rc := tokenRC{secrets: map[string]string{wire.SecretBearerToken: "open-token-1234"}}
	ctx := context.Background()

	if _, err := Open(ctx, rc, nil, nil, "k8s.test"); err == nil || !strings.Contains(err.Error(), "no target device") {
		t.Errorf("no target = %v", err)
	}
	unonboarded := cluster
	unonboarded.onboarded = false
	if _, err := Open(ctx, rc, unonboarded, nil, "k8s.test"); err == nil || !strings.Contains(err.Error(), "pleiades onboard c1") {
		t.Errorf("a cluster never onboarded = %v", err)
	}
	if _, err := Open(ctx, rc, cluster, map[string]any{ParamNamespace: "Team_A"}, "k8s.test"); err == nil || !strings.Contains(err.Error(), "not a Kubernetes namespace name") {
		t.Errorf("a hostile namespace = %v", err)
	}
	s, err := Open(ctx, rc, cluster, map[string]any{ParamNamespace: "apps"}, "k8s.test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Namespace != "apps" || s.Config.Host != "https://10.0.0.1:6443" || s.Config.BearerToken != "open-token-1234" {
		t.Errorf("session namespace %q, host %q, token %q", s.Namespace, s.Config.Host, s.Config.BearerToken)
	}
	if s, err := Open(ctx, rc, cluster, nil, "k8s.test"); err != nil || s.Namespace != "default" {
		t.Errorf("no namespace parameter = %v, %v; want the cluster's own", s, err)
	}
}

// TestValidNamespace holds the RFC 1123 label grammar against the values a
// runbook author or a record could write: a path, a flag, upper case,
// whitespace, a separator, a non-ASCII letter, and one character too long.
func TestValidNamespace(t *testing.T) {
	for _, ok := range []string{"default", "team-a", "a", "a1", "1a", "a--b", strings.Repeat("a", 63)} {
		if !ValidNamespace(ok) {
			t.Errorf("ValidNamespace(%q) = false, want it accepted", ok)
		}
	}
	for _, bad := range []string{
		"", "-a", "a-", "-", "Team_A", "TEAM", "a_b", "a.b", "../kube-system", "a/b", " a", "a ", "a\nb", "a\x00",
		"\u00e9quipe", "--all-namespaces", strings.Repeat("a", 64),
	} {
		if ValidNamespace(bad) {
			t.Errorf("ValidNamespace(%q) = true, want it refused", bad)
		}
	}
}

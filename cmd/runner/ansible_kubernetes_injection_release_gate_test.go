// Release gate for Phase 119c's Kubernetes credential on the Ansible path:
// a template's kubernetes_bearer_token reaches a real ansible-playbook in a
// real container as AWX's own K8S_AUTH_* environment, which kubernetes.core
// reads, with the authority in the file that environment names.
package main_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The control values the Kubernetes run injects. The token is a secret
// input, so the playbook reports whether it arrived rather than printing it.
const (
	kubernetesGateHost  = "https://k8s.release-gate.example:6443"
	kubernetesGateToken = "release-gate-cluster-token-0001"
	kubernetesGateCA    = "-----BEGIN CERTIFICATE-----\nRELEASEGATECA\n-----END CERTIFICATE-----"
)

// kubernetesArrivalPlaybook reports what kubernetes.core would read, from
// inside the runner container.
const kubernetesArrivalPlaybook = `---
- hosts: all
  gather_facts: false
  tasks:
    - name: report the cluster environment
      debug:
        msg: "host={{ lookup('env', 'K8S_AUTH_HOST') }} verify={{ lookup('env', 'K8S_AUTH_VERIFY_SSL') }} token_arrived={{ lookup('env', 'K8S_AUTH_API_KEY') == '` + kubernetesGateToken + `' }}"

    - name: report the cluster authority
      debug:
        msg: "ca={{ lookup('file', lookup('env', 'K8S_AUTH_SSL_CA_CERT')) | replace('\n', '|') }}"
`

// TestInjectionReleaseGate_KubernetesTokenArrivesAsAWXSetsIt renders the
// shipped type through the real injector and builds the payload through
// the fan-out's own conversion (dispatch.ApplyInjection) for a playbook
// job, so the container receives exactly what production sends.
func TestInjectionReleaseGate_KubernetesTokenArrivesAsAWXSetsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	ct, ok := managed.Type("kubernetes_bearer_token")
	if !ok {
		t.Fatal("the catalog does not ship kubernetes_bearer_token")
	}
	injector, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	art, err := injector.Inject([]credtype.Credential{(credtype.Credential{
		ID: injectionGateCredentialID, Name: "release gate cluster", Type: ct,
		Inputs: map[string]string{"host": kubernetesGateHost, "bearer_token": kubernetesGateToken, "verify_ssl": "true", "ssl_ca_cert": kubernetesGateCA},
	}).WithDefaults()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	h := newAnsibleReleaseGateHarness(t, kubernetesArrivalPlaybook)
	payload := wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "upgrade.yml",
		Kind:         playbook.Kind,
		DeviceID:     "release-gate-device",
		DeviceName:   "sw1",
		DeviceHost:   ansibleGateNetworkAlias,
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameCiscoIOS},
		Secrets:      gateMachineSecrets(),
	}
	dispatch.ApplyInjection(&payload, art)
	if payload.Secrets[wire.SecretBearerToken] != "" {
		t.Fatal("a playbook job's device was given the cluster token as its own secret")
	}

	events := h.dispatch(t, payload)
	if final := events[len(events)-1]; final.Status != "ok" {
		t.Fatalf("final status = %q, want ok: events=%+v", final.Status, events)
	}
	assertReported(t, events, "report the cluster environment", "host="+kubernetesGateHost+" verify=True token_arrived=True")
	assertReported(t, events, "report the cluster authority", "ca=-----BEGIN CERTIFICATE-----|RELEASEGATECA|-----END CERTIFICATE-----")
}

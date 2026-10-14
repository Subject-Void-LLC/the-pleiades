// Tests that a record naming an EKS cluster authenticates with tokens
// minted from its AWS key, and that what cannot mint one is refused.
package kubeapi

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/url"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// eksDevice is a cluster record naming an EKS cluster.
type eksDevice struct {
	clusterDevice
	eks, region string
}

func (e eksDevice) KubeEKS() (string, string) { return e.eks, e.region }

// TestForDevice_AnEKSClusterSendsMintedTokens: the API server receives a
// k8s-aws-v1 token, the presigned request for this cluster signed with the
// stored access key, and never the secret key itself.
func TestForDevice_AnEKSClusterSendsMintedTokens(t *testing.T) {
	f := newFakeAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { emptyList(w) })
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
	device := eksDevice{clusterDevice: clusterDevice{fakeItem: fakeItem{name: "prod"}, server: f.srv.URL}, eks: "prod-cluster", region: "us-west-2"}
	secrets := map[string]string{wire.SecretUsername: "AKIDEKSTEST", wire.SecretPassword: "eks-secret-access-key", wire.SecretClusterCAPEM: string(ca)}
	s, err := ForDevice(device, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	var sent []string
	f.tokens.Range(func(k, _ any) bool { sent = append(sent, k.(string)); return true })
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "k8s-aws-v1.") {
		t.Fatalf("the server received %q, want one EKS token", sent)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sent[0], "k8s-aws-v1."))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "sts.us-west-2.amazonaws.com" || q.Get("Action") != "GetCallerIdentity" || !strings.HasPrefix(q.Get("X-Amz-Credential"), "AKIDEKSTEST/") || q.Get("X-Amz-SignedHeaders") != "host;x-k8s-aws-id" {
		t.Errorf("the token is not this cluster's presigned STS request: %s", raw)
	}
	if strings.Contains(string(raw), "eks-secret-access-key") {
		t.Error("the token carries the secret access key")
	}
}

// TestForDevice_AnEKSClusterNeedsAnAWSKey: an EKS record whose credential
// is a token or a certificate, or holds no key, is refused naming the fix;
// a record that names no EKS cluster still refuses a password.
func TestForDevice_AnEKSClusterNeedsAnAWSKey(t *testing.T) {
	device := eksDevice{clusterDevice: clusterDevice{fakeItem: fakeItem{name: "prod"}, server: "https://eks.example.com"}, eks: "prod-cluster", region: "us-west-2"}
	for name, secrets := range map[string]map[string]string{
		"a token":       {wire.SecretBearerToken: "t", wire.SecretUsername: "AKID", wire.SecretPassword: "s"},
		"a certificate": {wire.SecretCertificatePEM: "c", wire.SecretPrivateKeyPEM: "k"},
		"no secret key": {wire.SecretUsername: "AKID"},
		"no credential": {},
	} {
		if _, err := ForDevice(device, secrets, ""); err == nil {
			t.Errorf("%s: an EKS session was built", name)
		}
	}
	plain := eksDevice{clusterDevice: device.clusterDevice}
	if _, err := ForDevice(plain, map[string]string{wire.SecretUsername: "AKID", wire.SecretPassword: "s"}, ""); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("a password for a cluster that is not EKS: %v, want a refusal naming the password", err)
	}
}

// Tests and a fuzz target for kubeconfig import: what it keeps, and each
// setting it refuses by name.
package kubeapi

import (
	"encoding/base64"
	"strings"
	"testing"
)

// kubeconfig builds a one-context kubeconfig whose cluster and user carry
// the given extra lines.
func kubeconfig(clusterExtra, userExtra string) string {
	return `apiVersion: v1
kind: Config
current-context: dev
contexts:
- name: dev
  context: {cluster: c, user: u, namespace: team}
clusters:
- name: c
  cluster:
    server: https://10.0.0.1:6443
` + clusterExtra + `users:
- name: u
  user:
` + userExtra
}

var (
	b64CA   = base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"))
	b64Cert = base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nC\n-----END CERTIFICATE-----\n"))
	b64Key  = base64.StdEncoding.EncodeToString([]byte("-----BEGIN EC PRIVATE KEY-----\nK\n-----END EC PRIVATE KEY-----\n"))
)

// TestImport_KeepsTheFourValues imports a token context and a certificate
// context and checks exactly what each keeps.
func TestImport_KeepsTheFourValues(t *testing.T) {
	got, err := Import([]byte(kubeconfig("    certificate-authority-data: "+b64CA+"\n    tls-server-name: api.internal\n", "    token: abc.def.ghi\n")), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Context != "dev" || got.Server != "https://10.0.0.1:6443" || !strings.Contains(string(got.CAPEM), "CA") ||
		got.ServerName != "api.internal" || got.Namespace != "team" || got.Token != "abc.def.ghi" || got.ClientCertPEM != nil {
		t.Errorf("token context imported as %+v", got)
	}
	got, err = Import([]byte(kubeconfig("", "    client-certificate-data: "+b64Cert+"\n    client-key-data: "+b64Key+"\n")), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "" || !strings.Contains(string(got.ClientCertPEM), "BEGIN CERTIFICATE") || !strings.Contains(string(got.ClientKeyPEM), "PRIVATE KEY") {
		t.Errorf("certificate context imported as %+v", got)
	}
}

// TestImport_RefusesByName holds each refusal to naming what it refused.
func TestImport_RefusesByName(t *testing.T) {
	for name, tc := range map[string]struct {
		cluster, user, want string
	}{
		"exec":              {"", "    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: aws\n", "exec"},
		"eks get-token":     {"", "    exec:\n      apiVersion: client.authentication.k8s.io/v1beta1\n      command: aws\n      args: [--region, us-east-1, eks, get-token, --cluster-name, prod-eks, --output, json]\n", "eks_cluster_name=prod-eks and aws_region=us-east-1"},
		"eks region by env": {"", "    exec:\n      apiVersion: client.authentication.k8s.io/v1beta1\n      command: /usr/local/bin/aws\n      args: [eks, get-token, --cluster-name=prod-eks]\n      env: [{name: AWS_REGION, value: eu-west-1}]\n", "aws_region=eu-west-1"},
		"another program":   {"", "    exec:\n      apiVersion: client.authentication.k8s.io/v1beta1\n      command: gke-gcloud-auth-plugin\n      args: [eks, get-token, --cluster-name, x, --region, us-east-1]\n", "runs a program"},
		"auth-provider":     {"", "    auth-provider:\n      name: gcp\n", "auth-provider"},
		"a token file":      {"", "    tokenFile: /var/run/token\n", "tokenFile"},
		"a cert file":       {"", "    client-certificate: /home/me/c.pem\n    client-key: /home/me/k.pem\n", "as a file"},
		"an authority file": {"    certificate-authority: /home/me/ca.pem\n", "    token: t\n", "certificate-authority"},
		"skip verify":       {"    insecure-skip-tls-verify: true\n", "    token: t\n", "insecure-skip-tls-verify"},
		"a proxy":           {"    proxy-url: http://proxy:3128\n", "    token: t\n", "proxy-url"},
		"a password":        {"", "    username: admin\n    password: x\n", "username and password"},
		"impersonation":     {"", "    token: t\n    as: system:admin\n", "impersonates"},
		"no identity":       {"", "    {}\n", "no credential"},
		"two identities":    {"", "    token: t\n    client-certificate-data: " + b64Cert + "\n    client-key-data: " + b64Key + "\n", "more than one"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Import([]byte(kubeconfig(tc.cluster, tc.user)), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

// TestImport_RefusesAnUnknownContextNamingTheOthers checks a mistyped
// context is answered with the ones that exist.
func TestImport_RefusesAnUnknownContextNamingTheOthers(t *testing.T) {
	_, err := Import([]byte(kubeconfig("", "    token: t\n")), "prod")
	if err == nil || !strings.Contains(err.Error(), `"dev"`) {
		t.Fatalf("Import = %v, want the existing contexts named", err)
	}
}

// TestImport_RefusesAPlainHTTPServer checks the server must be https.
func TestImport_RefusesAPlainHTTPServer(t *testing.T) {
	cfg := strings.Replace(kubeconfig("", "    token: t\n"), "https://10.0.0.1:6443", "http://10.0.0.1:8080", 1)
	if _, err := Import([]byte(cfg), ""); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("Import = %v, want a refusal for a plain HTTP server", err)
	}
}

// FuzzImport proves an import never panics on any input, and that whatever
// it accepts carries an https server and exactly one identity, and nothing
// it refuses by name.
func FuzzImport(f *testing.F) {
	f.Add([]byte(kubeconfig("    certificate-authority-data: "+b64CA+"\n", "    token: abc\n")), "")
	f.Add([]byte(kubeconfig("", "    exec:\n      command: aws\n")), "dev")
	f.Add([]byte("apiVersion: v1\nkind: Config\n"), "")
	f.Add([]byte("{"), "x")
	f.Add([]byte(""), "")
	f.Fuzz(func(t *testing.T, data []byte, contextName string) {
		got, err := Import(data, contextName)
		if err != nil {
			return
		}
		if !strings.HasPrefix(got.Server, "https://") {
			t.Fatalf("accepted a server %q that is not https", got.Server)
		}
		if err := checkIdentity(Identity{Token: got.Token, ClientCertPEM: got.ClientCertPEM, ClientKeyPEM: got.ClientKeyPEM}); err != nil {
			t.Fatalf("accepted an identity it should refuse: %v", err)
		}
	})
}

// TestImport_NeverQuotesACredentialInTheServer: an import's errors reach an
// API response, a log and a terminal, so a server address carrying a
// password or a token in its query is refused without being repeated, in
// every branch that refuses it, including one net/url cannot parse, whose
// own error would repeat the raw address.
func TestImport_NeverQuotesACredentialInTheServer(t *testing.T) {
	const secret = "s3cr3tpass77"
	for _, server := range []string{
		"http://admin:" + secret + "@10.0.0.1:8080",
		"https://admin:" + secret + "@10.0.0.1:notaport",
		"https://admin:" + secret + "@10.0.0.1:6443",
		"https://10.0.0.1:6443/?token=" + secret,
		"https://10.0.0.1:6443/#" + secret,
		"https:///" + secret,
	} {
		cfg := strings.Replace(kubeconfig("", "    token: t\n"), "https://10.0.0.1:6443", server, 1)
		_, err := Import([]byte(cfg), "")
		if err == nil {
			t.Errorf("server %q was accepted", server)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("server %q: the refusal repeats the secret: %v", server, err)
		}
	}
}

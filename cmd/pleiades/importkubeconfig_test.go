// In-process tests of import-kubeconfig and add-credential --token-stdin:
// what each writes, and each refusal, read back from the project's own
// files.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
)

// clusterCAPEM is a throwaway authority, as PEM.
func clusterCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cluster ca"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// kubeconfigText is a one-context kubeconfig for server with user's lines
// and, when namespace is set, that namespace.
func kubeconfigText(server, ca, namespace, user string) string {
	ctx := "{cluster: k, user: u}"
	if namespace != "" {
		ctx = "{cluster: k, user: u, namespace: " + namespace + "}"
	}
	cluster := "    server: " + server + "\n"
	if ca != "" {
		cluster += "    certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(ca)) + "\n"
	}
	return "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: " + ctx + "\n" +
		"clusters:\n- name: k\n  cluster:\n" + cluster + "users:\n- name: u\n  user:\n" + user
}

// withStdin runs fn with standard input reading text.
func withStdin(t *testing.T, text string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved; _ = r.Close() }()
	fn()
}

// hostSpec returns the hosts file's entry for name.
func hostSpec(t *testing.T, dir, name string) (inventory.HostSpec, bool) {
	t.Helper()
	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.Name == name {
			return h, true
		}
	}
	return inventory.HostSpec{}, false
}

// TestImportKubeconfig_WritesTheClusterAndItsCredential: a token context
// from a file and a certificate context from standard input each become a
// record and a stored credential; the output names neither secret.
func TestImportKubeconfig_WritesTheClusterAndItsCredential(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	ca := clusterCAPEM(t)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfigText("https://k.example:6443", ca, "team-a", "    token: imported-token-value\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	var runErr error
	out := captureStdout(t, func() { runErr = runImportKubeconfig([]string{"prod", "--kubeconfig", path, "--dir", dir, "--json"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, `"identity": "token"`) || !strings.Contains(out, `"namespace": "team-a"`) || strings.Contains(out, "imported-token-value") {
		t.Errorf("--json printed %s", out)
	}
	spec, ok := hostSpec(t, dir, "prod")
	if !ok || spec.Type != kubernetes.TypeCluster || spec.Properties[kubernetes.APIServerProperty] != "https://k.example:6443" {
		t.Fatalf("hosts entry %+v", spec)
	}
	if stored := lookupStored(t, dir, "prod"); stored.Token != "imported-token-value" {
		t.Errorf("stored credential holds token %q", stored.Token)
	}

	// A certificate context with no authority, from standard input, as
	// text: it says the system's roots will check the server.
	cert := kubeconfigText("https://other.example", "", "",
		"    client-certificate-data: "+base64.StdEncoding.EncodeToString([]byte(ca))+"\n    client-key-data: "+base64.StdEncoding.EncodeToString([]byte("KEY-PEM-SECRET"))+"\n")
	withStdin(t, cert, func() {
		out = captureStdout(t, func() { runErr = runImportKubeconfig([]string{"other", "--kubeconfig", "-", "--dir", dir}) })
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "client certificate") || !strings.Contains(out, "system's roots") || !strings.Contains(out, "pleiades onboard other") || strings.Contains(out, "KEY-PEM-SECRET") {
		t.Errorf("text output %s", out)
	}
	if stored := lookupStored(t, dir, "other"); string(stored.PrivateKeyPEM) != "KEY-PEM-SECRET" || len(stored.CertificatePEM) == 0 {
		t.Error("the client certificate was not stored")
	}
}

// TestImportKubeconfig_Refusals: each refusal leaves the inventory as it
// was.
func TestImportKubeconfig_Refusals(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	ca := clusterCAPEM(t)
	write := func(name, body string) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	token := write("token", kubeconfigText("https://k.example", ca, "", "    token: t\n"))
	if err := runAddHost([]string{"web1", "--type", "linux_server", "--set", "host=10.0.0.1", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := runImportKubeconfig([]string{"prod", "--kubeconfig", token, "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no kubeconfig", []string{"a", "--dir", dir}, "--kubeconfig is required"},
		{"no device name", []string{"--kubeconfig", token, "--dir", dir}, "usage"},
		{"a file that is not there", []string{"a", "--kubeconfig", filepath.Join(dir, "absent"), "--dir", dir}, "reading the kubeconfig"},
		{"an exec user", []string{"a", "--kubeconfig", write("exec", kubeconfigText("https://k.example", ca, "", "    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: evil\n")), "--dir", dir}, "exec"},
		{"an authority that is not a certificate", []string{"a", "--kubeconfig", write("badca", kubeconfigText("https://k.example", "not a certificate", "", "    token: t\n")), "--dir", dir}, "not imported"},
		{"an existing cluster without --replace", []string{"prod", "--kubeconfig", token, "--dir", dir}, "--replace"},
		{"an existing device of another type", []string{"web1", "--kubeconfig", token, "--replace", "--dir", dir}, "not a kubernetes_cluster"},
		{"an unknown flag", []string{"a", "--kubeconfig", token, "--dir", dir, "--bogus"}, "bogus"},
		{"no project", []string{"a", "--kubeconfig", token, "--dir", t.TempDir()}, "pleiades init"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runImportKubeconfig(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("import = %v, want a refusal saying %q", err, tc.want)
			}
		})
	}
	if _, ok := hostSpec(t, dir, "a"); ok {
		t.Error("a refused import added a host")
	}

	// --replace keeps what the import does not own.
	spec, _ := hostSpec(t, dir, "prod")
	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	for i := range hosts {
		if hosts[i].Name == "prod" {
			hosts[i].Properties["route"] = []any{"bastion"}
		}
	}
	if err := inventory.WriteHosts(filepath.Join(dir, inventory.DefaultInventoryFilename), hosts); err != nil {
		t.Fatal(err)
	}
	moved := write("moved", kubeconfigText("https://k2.example", ca, "", "    token: t2\n"))
	if err := runImportKubeconfig([]string{"prod", "--kubeconfig", moved, "--replace", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	after, _ := hostSpec(t, dir, "prod")
	if after.ID != spec.ID || after.Properties[kubernetes.APIServerProperty] != "https://k2.example" || after.Properties["route"] == nil {
		t.Errorf("after --replace: %+v", after)
	}
}

// TestAddCredential_TokenStdin: a token read from standard input is the
// whole credential; combined with anything else, or empty, it is refused.
func TestAddCredential_TokenStdin(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	var runErr error
	withStdin(t, "cluster-token-from-stdin\n", func() {
		captureStdout(t, func() { runErr = runAddCredential([]string{"k3s", "--token-stdin", "--dir", dir}) })
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if stored := lookupStored(t, dir, "k3s"); stored.Token != "cluster-token-from-stdin" || stored.Username != "" {
		t.Errorf("stored %s", stored)
	}
	if err := runAddCredential([]string{"k3s", "--token-stdin", "--username", "u", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "whole credential") {
		t.Errorf("--token-stdin with --username: %v", err)
	}
	withStdin(t, "   \n", func() { runErr = runAddCredential([]string{"k3s", "--token-stdin", "--dir", dir}) })
	if runErr == nil || !strings.Contains(runErr.Error(), "empty token") {
		t.Errorf("an empty token: %v", runErr)
	}
}

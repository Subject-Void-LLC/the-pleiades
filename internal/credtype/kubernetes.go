// The Kubernetes credential kind: a cluster's bearer token or client
// certificate, read directly by the native path and turned into AWX's own
// environment on the Ansible path (Phase 119c).
package credtype

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The input ids a Kubernetes credential type declares. host, bearer_token,
// verify_ssl and ssl_ca_cert are AWX's own (kubernetes_bearer_token);
// client_certificate and client_key are this platform's, for a cluster that
// authenticates x509 (pleiades_kubernetes_client_certificate).
const (
	KubernetesInputHost        = "host"
	KubernetesInputBearerToken = "bearer_token"
	KubernetesInputVerifySSL   = "verify_ssl"
	KubernetesInputCACert      = "ssl_ca_cert"
	KubernetesInputClientCert  = "client_certificate"
	KubernetesInputClientKey   = "client_key"
)

// The environment AWX's kubernetes_bearer_token injector sets, which the
// kubernetes.core collection reads, plus the two kubernetes.core reads for a
// client certificate.
const (
	K8sAuthHost      = "K8S_AUTH_HOST"
	K8sAuthAPIKey    = "K8S_AUTH_API_KEY"
	K8sAuthVerifySSL = "K8S_AUTH_VERIFY_SSL"
	K8sAuthCACert    = "K8S_AUTH_SSL_CA_CERT"
	K8sAuthCertFile  = "K8S_AUTH_CERT_FILE"
	K8sAuthKeyFile   = "K8S_AUTH_KEY_FILE"
)

// KubernetesCredential is one cluster credential an artifact carries: the
// API server it is for, and a bearer token or a client certificate.
type KubernetesCredential struct {
	// CredentialID names the credential, for the files the Ansible path
	// writes (FilePath).
	CredentialID int
	// Name is the credential's name, for a message.
	Name string
	// Host is the API server the credential is for, as entered.
	Host string
	// Token is a bearer token; empty for a client certificate.
	Token string
	// ClientCertPEM and ClientKeyPEM are a client certificate and its key.
	ClientCertPEM, ClientKeyPEM string
	// CAPEM is the authority the server's certificate is checked against,
	// when the credential carries one.
	CAPEM string
	// Verify is verify_ssl: true unless the credential says false.
	Verify bool
}

// kubernetesTarget turns a Kubernetes kind's inputs into the artifact's one
// cluster credential. Like registryTarget it is code rather than data: AWX
// builds this kind's environment in Python, with a branch its data
// injectors cannot express, so the type's injector document is empty, as
// AWX exports it, and what each execution path needs is made from this one
// value (Secrets for the native path, AnsibleInjection for the Ansible
// path).
type kubernetesTarget struct{}

func (kubernetesTarget) Key() string  { return "kubernetes" }
func (kubernetesTarget) Phase() Phase { return PhaseValues }

// Apply maps a Kubernetes kind's inputs onto the artifact's cluster
// credential (KubernetesCredentialOf).
func (kubernetesTarget) Apply(req Request, art *Artifact) error {
	if req.Credential.Type.Kind != KindKubernetes {
		return nil
	}
	k, err := KubernetesCredentialOf(req.Credential)
	if err != nil {
		return err
	}
	art.kubernetes = &k
	return nil
}

// KubernetesCredentialOf reads a Kubernetes kind credential's inputs,
// refusing one with no https API server or with neither a token nor a
// whole client certificate. It is the one reading of those inputs, which
// the injector and onboarding (a cluster's stored credential) share.
func KubernetesCredentialOf(cred Credential) (KubernetesCredential, error) {
	if cred.Type.Kind != KindKubernetes {
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q is a %s credential, not a Kubernetes one", ErrInjection, cred.Name, cred.Type.Kind)
	}
	in := func(id string) string { return strings.TrimSpace(cred.Inputs[id]) }
	k := KubernetesCredential{
		CredentialID:  cred.ID,
		Name:          cred.Name,
		Host:          in(KubernetesInputHost),
		Token:         in(KubernetesInputBearerToken),
		ClientCertPEM: in(KubernetesInputClientCert),
		ClientKeyPEM:  in(KubernetesInputClientKey),
		CAPEM:         in(KubernetesInputCACert),
		Verify:        in(KubernetesInputVerifySSL) != "false",
	}
	if k.Host == "" {
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q: a Kubernetes credential needs its API server (host)", ErrInjection, cred.Name)
	}
	if _, err := apiServerHost(k.Host); err != nil {
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q: %v", ErrInjection, cred.Name, err)
	}
	certificate := k.ClientCertPEM != "" || k.ClientKeyPEM != ""
	switch {
	case k.Token != "" && certificate:
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q: a Kubernetes credential is a bearer token or a client certificate, not both", ErrInjection, cred.Name)
	case certificate && (k.ClientCertPEM == "" || k.ClientKeyPEM == ""):
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q: a client certificate needs both the certificate and its key", ErrInjection, cred.Name)
	case k.Token == "" && !certificate:
		return KubernetesCredential{}, fmt.Errorf("%w: credential %q: a Kubernetes credential needs a bearer token or a client certificate", ErrInjection, cred.Name)
	}
	return k, nil
}

// ForServerHost reports whether host, a Kubernetes credential's host
// input, names the API server apiServer (KubernetesCredential.ForServer),
// for a caller holding only the credential's visible inputs.
func ForServerHost(host, apiServer string) bool {
	return KubernetesCredential{Host: host}.ForServer(apiServer)
}

// Secrets is the credential as a cluster device's own secrets, the keys
// pkg/kubeapi reads: the token or the certificate and key, and the
// credential's authority for a cluster whose record pins none.
func (k KubernetesCredential) Secrets() map[string]string {
	out := map[string]string{}
	if k.Token != "" {
		out[wire.SecretBearerToken] = k.Token
	} else {
		out[wire.SecretCertificatePEM] = k.ClientCertPEM
		out[wire.SecretPrivateKeyPEM] = k.ClientKeyPEM
	}
	if k.CAPEM != "" {
		out[wire.SecretClusterCAPEM] = k.CAPEM
	}
	return out
}

// ForServer reports whether apiServer, a cluster's api_server, is the
// endpoint this credential is for: the same host and port, the port
// defaulting to 443. A credential is sent only to the cluster it names, as
// a registry credential is sent only to its registry.
func (k KubernetesCredential) ForServer(apiServer string) bool {
	want, err := apiServerHost(k.Host)
	if err != nil {
		return false
	}
	got, err := apiServerHost(apiServer)
	return err == nil && strings.EqualFold(want, got)
}

// apiServerHost is an API server address's host and port, the port
// defaulting to 443; a bare host:port is read as https, as AWX's host
// input is often written.
func apiServerHost(server string) (string, error) {
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("the API server %q is not a URL with a host", server)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("the API server %q is not https; a cluster is reached over TLS only", server)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Hostname()) + ":" + port, nil
}

// AnsibleInjection is what the Ansible path injects for the credential:
// AWX's kubernetes_bearer_token environment, which kubernetes.core reads,
// and the files it names.
//
// AWX sets K8S_AUTH_VERIFY_SSL to True only when verification is on AND a
// CA is supplied, writing the CA to a file; otherwise it sets False, so a
// credential asking for verification without a CA is quietly not verified.
// This platform does not copy that branch: verification is off only when
// the credential says verify_ssl false, and with no CA a cluster is
// verified against the execution environment's own roots, the way a
// cluster with a public certificate (a managed service's) is. Weakening
// TLS is an explicit choice here, never a side effect of a missing field.
func (k KubernetesCredential) AnsibleInjection() (map[string]string, []File) {
	env := map[string]string{K8sAuthHost: k.Host}
	var files []File
	if k.Token != "" {
		env[K8sAuthAPIKey] = k.Token
	} else {
		cert := File{Label: "client_certificate", Path: FilePath(k.CredentialID, "client_certificate"), Content: k.ClientCertPEM, Mode: FileMode}
		key := File{Label: "client_key", Path: FilePath(k.CredentialID, "client_key"), Content: k.ClientKeyPEM, Mode: FileMode}
		files = append(files, cert, key)
		env[K8sAuthCertFile], env[K8sAuthKeyFile] = cert.Path, key.Path
	}
	if !k.Verify {
		env[K8sAuthVerifySSL] = "False"
		return env, files
	}
	env[K8sAuthVerifySSL] = "True"
	if k.CAPEM != "" {
		ca := File{Label: "ssl_ca_cert", Path: FilePath(k.CredentialID, "ssl_ca_cert"), Content: k.CAPEM, Mode: FileMode}
		files = append(files, ca)
		env[K8sAuthCACert] = ca.Path
	}
	return env, files
}

// Kubernetes returns the cluster credential this artifact carries, and
// false when it carries none.
func (a Artifact) Kubernetes() (KubernetesCredential, bool) {
	if a.kubernetes == nil {
		return KubernetesCredential{}, false
	}
	return *a.kubernetes, true
}

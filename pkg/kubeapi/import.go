// Kubeconfig import: one context of a kubeconfig, kept as four values, with
// everything that would run a program, read a file or weaken TLS refused by
// name (Part XVIII D5).
package kubeapi

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clientcmdlatest "k8s.io/client-go/tools/clientcmd/api/latest"
)

// maxKubeconfigBytes bounds a kubeconfig an import reads: a real one is a
// few kilobytes, and an import is given one by whoever runs it.
const maxKubeconfigBytes = 1 << 20

// Imported is what an import keeps from one kubeconfig context: where the
// cluster is, the authority that vouches for it, and who to be. The token,
// or the client certificate and its key, is the credential; the rest is the
// device's record.
type Imported struct {
	// Context is the context the values came from.
	Context string
	// Server is the API server's URL.
	Server string
	// CAPEM is the cluster's authority, when the kubeconfig embeds one.
	CAPEM []byte
	// ServerName is the name the server's certificate is checked against,
	// when the kubeconfig sets one.
	ServerName string
	// Namespace is the context's namespace, when it sets one.
	Namespace string
	// Token is a bearer token; empty when the identity is a certificate.
	Token string
	// ClientCertPEM and ClientKeyPEM are a client certificate and its key.
	ClientCertPEM, ClientKeyPEM []byte
}

// Import reads the context named contextName (the kubeconfig's current
// context when empty) from data and keeps only what Imported holds. Every
// setting that would make the platform run a program (exec,
// auth-provider), read a file named in the kubeconfig (a certificate,
// key, authority or token file), skip certificate verification, go through
// a proxy, impersonate another identity, or send a password is refused by
// name: the kubeconfig is someone's local configuration, and none of those
// means the same thing on a Runner.
func Import(data []byte, contextName string) (Imported, error) {
	if len(data) == 0 {
		return Imported{}, errors.New("kubeapi: the kubeconfig is empty")
	}
	if len(data) > maxKubeconfigBytes {
		return Imported{}, fmt.Errorf("kubeapi: the kubeconfig is larger than %d bytes", maxKubeconfigBytes)
	}
	cfg := clientcmdapi.NewConfig()
	decoded, _, err := clientcmdlatest.Codec.Decode(data, &schema.GroupVersionKind{Version: clientcmdlatest.Version, Kind: "Config"}, cfg)
	if err != nil {
		return Imported{}, fmt.Errorf("kubeapi: the kubeconfig does not parse: %w", err)
	}
	cfg, ok := decoded.(*clientcmdapi.Config)
	if !ok {
		return Imported{}, errors.New("kubeapi: the kubeconfig is not a Config")
	}

	name := contextName
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return Imported{}, fmt.Errorf("kubeapi: the kubeconfig names no current context; choose one of %s", contextNames(cfg))
	}
	kctx, ok := cfg.Contexts[name]
	if !ok || kctx == nil {
		return Imported{}, fmt.Errorf("kubeapi: the kubeconfig has no context %q; it has %s", name, contextNames(cfg))
	}
	cluster, ok := cfg.Clusters[kctx.Cluster]
	if !ok || cluster == nil {
		return Imported{}, fmt.Errorf("kubeapi: context %q names cluster %q, which the kubeconfig does not define", name, kctx.Cluster)
	}
	user, ok := cfg.AuthInfos[kctx.AuthInfo]
	if !ok || user == nil {
		return Imported{}, fmt.Errorf("kubeapi: context %q names user %q, which the kubeconfig does not define", name, kctx.AuthInfo)
	}
	if err := refuseCluster(name, cluster); err != nil {
		return Imported{}, err
	}
	if err := refuseUser(name, user); err != nil {
		return Imported{}, err
	}
	if err := checkServer(cluster.Server); err != nil {
		return Imported{}, err
	}
	if kctx.Namespace != "" && !ValidNamespace(kctx.Namespace) {
		return Imported{}, fmt.Errorf("kubeapi: context %q's namespace %q is not a Kubernetes namespace name", name, kctx.Namespace)
	}

	out := Imported{
		Context:       name,
		Server:        cluster.Server,
		CAPEM:         cluster.CertificateAuthorityData,
		ServerName:    cluster.TLSServerName,
		Namespace:     kctx.Namespace,
		Token:         user.Token,
		ClientCertPEM: user.ClientCertificateData,
		ClientKeyPEM:  user.ClientKeyData,
	}
	if err := checkIdentity(Identity{Token: out.Token, ClientCertPEM: out.ClientCertPEM, ClientKeyPEM: out.ClientKeyPEM}); err != nil {
		return Imported{}, fmt.Errorf("kubeapi: context %q: %w", name, err)
	}
	return out, nil
}

// refuseCluster refuses the cluster settings an import does not take.
func refuseCluster(name string, c *clientcmdapi.Cluster) error {
	switch {
	case c.CertificateAuthority != "":
		return fmt.Errorf("kubeapi: context %q's cluster names its authority as a file (certificate-authority); an import reads no file the kubeconfig names, so embed it (`kubectl config view --raw --flatten`)", name)
	case c.InsecureSkipTLSVerify:
		return fmt.Errorf("kubeapi: context %q's cluster sets insecure-skip-tls-verify; a cluster is reached with its certificate verified, so pin its authority instead", name)
	case c.ProxyURL != "":
		return fmt.Errorf("kubeapi: context %q's cluster sets proxy-url; a proxy on the machine that wrote the kubeconfig is not one a Runner has, so reach the cluster directly or through a device's route", name)
	}
	return nil
}

// refuseUser refuses the user settings an import does not take.
func refuseUser(name string, u *clientcmdapi.AuthInfo) error {
	switch {
	case u.Exec != nil:
		if cluster, region, ok := eksExec(u.Exec); ok {
			return fmt.Errorf("kubeapi: context %q's user runs `aws eks get-token` for EKS cluster %q in %s; an import never runs a program, and this platform mints that token itself: record the cluster with eks_cluster_name=%s and aws_region=%s, and store an AWS access key as its credential", name, cluster, region, cluster, region)
		}
		return fmt.Errorf("kubeapi: context %q's user authenticates with exec (%q), which runs a program; an import never runs one, so store a token or a client certificate instead", name, u.Exec.Command)
	case u.AuthProvider != nil:
		return fmt.Errorf("kubeapi: context %q's user authenticates with auth-provider %q, which runs a program or reads local state; store a token or a client certificate instead", name, u.AuthProvider.Name)
	case u.TokenFile != "":
		return fmt.Errorf("kubeapi: context %q's user names its token as a file (tokenFile); an import reads no file the kubeconfig names, so put the token in the kubeconfig or store it with `pleiades add-credential --token-stdin`", name)
	case u.ClientCertificate != "" || u.ClientKey != "":
		return fmt.Errorf("kubeapi: context %q's user names its client certificate or key as a file; an import reads no file the kubeconfig names, so embed them (`kubectl config view --raw --flatten`)", name)
	case u.Username != "" || u.Password != "":
		return fmt.Errorf("kubeapi: context %q's user has a username and password; Kubernetes authenticates a token or a client certificate", name)
	case u.Impersonate != "" || u.ImpersonateUID != "" || len(u.ImpersonateGroups) > 0 || len(u.ImpersonateUserExtra) > 0:
		return fmt.Errorf("kubeapi: context %q's user impersonates another identity (as); a cluster credential acts as itself", name)
	}
	return nil
}

// contextNames lists a kubeconfig's contexts for a refusal, sorted.
func contextNames(cfg *clientcmdapi.Config) string {
	names := make([]string, 0, len(cfg.Contexts))
	for n := range cfg.Contexts {
		names = append(names, fmt.Sprintf("%q", n))
	}
	if len(names) == 0 {
		return "none"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// eksExec reads the EKS cluster and region an `aws eks get-token` exec
// entry names (what `aws eks update-kubeconfig` writes), without running
// it; ok is false for any other program, or when either is missing or
// malformed.
func eksExec(e *clientcmdapi.ExecConfig) (cluster, region string, ok bool) {
	if base := e.Command[strings.LastIndexAny(e.Command, `/\`)+1:]; base != "aws" && base != "aws.exe" {
		return "", "", false
	}
	getToken := false
	for i, arg := range e.Args {
		next := ""
		if i+1 < len(e.Args) {
			next = e.Args[i+1]
		}
		switch {
		case arg == "get-token" && i > 0 && e.Args[i-1] == "eks":
			getToken = true
		case arg == "--cluster-name":
			cluster = next
		case strings.HasPrefix(arg, "--cluster-name="):
			cluster = strings.TrimPrefix(arg, "--cluster-name=")
		case arg == "--region":
			region = next
		case strings.HasPrefix(arg, "--region="):
			region = strings.TrimPrefix(arg, "--region=")
		}
	}
	for _, env := range e.Env {
		if region == "" && (env.Name == "AWS_REGION" || env.Name == "AWS_DEFAULT_REGION") {
			region = env.Value
		}
	}
	if !getToken || !awscloud.ValidEKSClusterName(cluster) || !awscloud.ValidRegion(region) {
		return "", "", false
	}
	return cluster, region, true
}

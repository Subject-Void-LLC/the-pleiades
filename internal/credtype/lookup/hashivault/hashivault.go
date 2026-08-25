// Package hashivault reads a secret out of a HashiCorp Vault key/value
// store, as the first real row-backed external secret source.
//
// # Why there is no Vault client library here
//
// A KV read is one REST call: GET the path, send a token header, take one
// field out of the JSON. github.com/hashicorp/vault/api would put a large
// transitive tree (go-retryablehttp, hcl, go-hclog) on the dispatch path
// for that one call, and it would own the tls.Config that PLAN.md Section
// 17.4's "strictly enforces valid TLS chains" depends on. Owning the
// transport outright is what lets this package promise there is no way to
// turn verification off, because there is no field for it.
//
// This is deliberately NOT the same judgement Phase 100 records against
// hand-rolling BPv7, and the difference is worth stating so the two do not
// read as contradictory. That one is a binary protocol with a parser. This
// is an HTTP GET whose response is decoded by encoding/json. The rule the
// project follows is about parser surface, not about dependency counts.
//
// # Row backed only, and what that means for the string form
//
// A "<source>:<reference>" string has nowhere to put an address or a token,
// so every credential naming one such source would have to name the SAME
// Vault, configured from the Controller's environment. That is exactly the
// limitation the input-source row model exists to remove. So this source
// registers as a credtype.LookupFactory, selected by the source
// credential's type namespace, and never as a deployment-wide Lookup by
// name. internal/credtype's Lookups reports that difference specifically
// rather than as "not implemented".
package hashivault

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// Namespace is the credential type namespace this factory serves. It is
// AWX's own, so an AWX export maps onto it.
const Namespace = "hashivault_kv"

const (
	// requestTimeout bounds one read.
	//
	// This runs on the dispatch path, once per bound credential, with a
	// device fan-out waiting behind it. An unreachable Vault must fail the
	// job rather than hold the fan-out open, and the caller's own context
	// is still honoured when it is shorter.
	requestTimeout = 15 * time.Second

	// maxResponseBytes bounds one response body.
	//
	// The bound exists for the same reason the file source's does: the
	// response comes from a server named in a database row, so its size is
	// not this platform's to trust. A KV secret is far below this.
	maxResponseBytes = 1 << 20

	// maxSecretVersion bounds the version number a binding may ask for.
	// Vault's own versions are sequential and small; the bound is here so
	// a version cannot be used to smuggle arbitrary text into a query.
	maxSecretVersion = 1 << 24
)

// Errors this package returns.
var (
	// ErrConfiguration reports a source credential whose inputs cannot
	// configure a usable client. It never carries an input VALUE: the
	// token is the whole reason this type exists.
	ErrConfiguration = errors.New("hashivault: this source credential is not usable")
)

// Factory builds a Lookup from one source credential's resolved inputs.
type Factory struct{}

// Namespace returns the credential type namespace this factory serves.
func (Factory) Namespace() string { return Namespace }

// New builds a Lookup from a source credential's inputs.
//
// Every refusal here names the input rather than its value, and reaches
// whoever wrote the source credential rather than whoever launched the job.
func (Factory) New(inputs map[string]string) (credtype.Lookup, error) {
	raw := strings.TrimSpace(inputs["url"])
	if raw == "" {
		return nil, fmt.Errorf("%w: url is required", ErrConfiguration)
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: url is not a valid URL", ErrConfiguration)
	}
	// An explicit allowlist rather than a denylist. A file: or a unix:
	// scheme reaching an http.Client is a different subsystem entirely,
	// and http is permitted only because a Vault reached over a loopback
	// or a service mesh is an ordinary deployment.
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, fmt.Errorf("%w: url scheme %q is not one of https or http", ErrConfiguration, base.Scheme)
	}
	if base.Host == "" {
		return nil, fmt.Errorf("%w: url names no host", ErrConfiguration)
	}

	token := inputs["token"]
	if token == "" {
		return nil, fmt.Errorf("%w: token is required", ErrConfiguration)
	}

	version := strings.TrimSpace(inputs["api_version"])
	if version == "" {
		version = "v2"
	}
	if version != "v1" && version != "v2" {
		return nil, fmt.Errorf("%w: api_version %q is not one of v1 or v2", ErrConfiguration, version)
	}

	client, err := clientFor(inputs["cacert"])
	if err != nil {
		return nil, err
	}

	return &Lookup{
		base:      base,
		token:     token,
		namespace: strings.TrimSpace(inputs["namespace"]),
		kvVersion: version,
		client:    client,
	}, nil
}

// clientFor builds the HTTP client, verifying the chain always.
//
// There is no option to skip verification and no field that could carry
// one, which is what makes PLAN.md Section 17.4's "strictly enforces valid
// TLS chains" a property of the type rather than of a default.
func clientFor(cacert string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: this build has no usable HTTP transport", ErrConfiguration)
	}
	transport = transport.Clone()

	// The same TLS floor this module states once for every direction,
	// rather than a second opinion about acceptable versions.
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}

	if pem := strings.TrimSpace(cacert); pem != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(pem)) {
			return nil, fmt.Errorf("%w: cacert contains no PEM certificate this build could parse", ErrConfiguration)
		}
		transport.TLSClientConfig.RootCAs = pool
	}

	return &http.Client{Transport: transport, Timeout: requestTimeout}, nil
}

// Lookup reads secrets from one Vault.
type Lookup struct {
	base      *url.URL
	token     string
	namespace string
	kvVersion string
	client    *http.Client
}

// Name returns the source name a reference selects this Lookup with.
func (l *Lookup) Name() string { return Namespace }

// Resolve reads the secret the reference names.
//
// The error never carries the value, and never carries the token. It does
// carry the mount, path and key, which are pointers rather than secrets and
// are what an operator needs to fix the binding.
func (l *Lookup) Resolve(ctx context.Context, reference string) (string, error) {
	ref, err := decodeReference(reference)
	if err != nil {
		return "", err
	}

	endpoint, err := l.endpointFor(ref)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("%w: could not build a request for %s", credtype.ErrLookupReference, ref)
	}
	req.Header.Set("X-Vault-Token", l.token)
	if l.namespace != "" {
		req.Header.Set("X-Vault-Namespace", l.namespace)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		// The URL is deliberately not echoed: it is assembled from the
		// source credential and could name a customer's internal host in a
		// job record. The mount and path are enough to act on.
		return "", fmt.Errorf("%w: the Vault server could not be reached for %s", credtype.ErrLookupReference, ref)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := checkStatus(resp.StatusCode, ref); err != nil {
		return "", err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: the response for %s could not be read", credtype.ErrLookupReference, ref)
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("%w: the response for %s is larger than %d bytes",
			credtype.ErrLookupReference, ref, maxResponseBytes)
	}

	return l.valueFrom(body, ref)
}

// endpointFor builds the URL one reference reads.
//
// v2 reads through a /data/ segment that v1 does not have, which is the
// single behavioural difference between the two engine versions here and
// the reason api_version is an input rather than something to sniff.
func (l *Lookup) endpointFor(ref reference) (*url.URL, error) {
	segments := []string{"v1", ref.Mount}
	if l.kvVersion == "v2" {
		segments = append(segments, "data")
	}
	segments = append(segments, strings.Split(ref.Path, "/")...)

	// JoinPath escapes each segment, so a path element can never introduce
	// a new one. checkSegments has already refused dot segments outright,
	// so there is nothing here for JoinPath's own cleaning to resolve.
	endpoint := l.base.JoinPath(segments...)

	if ref.Version > 0 {
		if l.kvVersion != "v2" {
			return nil, fmt.Errorf("%w: %s asks for a secret version, which only a v2 key/value mount has",
				credtype.ErrLookupReference, ref)
		}
		q := endpoint.Query()
		q.Set("version", strconv.Itoa(ref.Version))
		endpoint.RawQuery = q.Encode()
	}
	return endpoint, nil
}

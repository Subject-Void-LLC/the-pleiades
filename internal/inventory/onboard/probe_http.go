// The HTTP probe: one verified, authenticated request to the base URL,
// and the OpenAPI document when the record names one.
package onboard

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// maxOpenAPIBytes bounds the OpenAPI document the probe reads.
const maxOpenAPIBytes = 4 << 20

type httpProber struct{}

func init() { Register(generic.TypeHTTP, httpProber{}) }

func (httpProber) Protocol() string { return "http" }

// openAPIPather is generic_http's accessor for the optional OpenAPI
// document's path.
type openAPIPather interface {
	OpenAPIPath() string
}

// Probe makes one authenticated GET of the base URL, and of the OpenAPI
// document when the record names one. TLS is the device's own
// (pkg/devicetls: certificates always verified, a client certificate when
// the record presents one) and no redirect is followed, so the credential
// reaches the base URL's origin and nowhere else. An answer proves the
// API; a refused credential (401 or 403) or a server error does not.
// Every weakening the record allows comes back as a warning.
func (p httpProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.HTTPAPICapable)
	if !ok {
		return Probed{}, errors.New("the device names no HTTP API")
	}
	base, err := httpapi.ValidateBaseURL(dev.HTTPBaseURL(), dev.HTTPAuth(), dev.HTTPAllowPlaintextCredentials())
	if err != nil {
		return Probed{}, err
	}
	settings := devicetls.For(device)
	var cfg *tls.Config
	if base.Scheme == "https" {
		if cfg, err = settings.Config(secrets); err != nil {
			return Probed{}, err
		}
	}
	warnings := settings.Warnings(device.Name())
	if httpapi.SendsPlaintextCredential(base, dev.HTTPAuth()) {
		warnings = append(warnings, httpapi.PlaintextWarning(device.Name()))
	}
	client := p.httpClient(cfg)

	resp, _, err := p.get(ctx, client, base, dev.HTTPAuth(), secrets, 64<<10)
	if err != nil {
		return Probed{}, err
	}
	facts := map[string]any{
		"status": strconv.Itoa(resp.StatusCode),
		"server": factText(resp.Header.Get("Server")),
	}
	if resp.TLS != nil {
		facts["tls_version"] = factText(tls.VersionName(resp.TLS.Version))
		facts["tls_cipher_suite"] = factText(tls.CipherSuiteName(resp.TLS.CipherSuite))
	}

	if pather, ok := device.(openAPIPather); ok && pather.OpenAPIPath() != "" {
		doc, err := httpapi.Join(base, pather.OpenAPIPath())
		if err != nil {
			return Probed{}, err
		}
		_, body, err := p.get(ctx, client, doc, dev.HTTPAuth(), secrets, maxOpenAPIBytes)
		if err != nil {
			return Probed{}, fmt.Errorf("the OpenAPI document: %w", err)
		}
		api, err := parseOpenAPI(body)
		if err != nil {
			return Probed{}, err
		}
		for k, v := range api {
			facts[k] = v
		}
	}
	return Probed{Capabilities: []capability.Name{capability.NameHTTPAPI}, Facts: facts, Warnings: warnings}, nil
}

// httpClient returns the probe's client: the device's TLS configuration
// (nil for an http:// base URL) on a transport of its own, and no
// redirects followed.
func (httpProber) httpClient(cfg *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = cfg
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get sends one authenticated GET and reads at most limit bytes of the
// answer, refusing a credential the server rejected and a server error.
func (httpProber) get(ctx context.Context, client *http.Client, u *url.URL, mode string, secrets map[string]string, limit int64) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	if err := httpapi.Authorize(req, mode, secrets); err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, nil, fmt.Errorf("the API refused the credential (%d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		return nil, nil, fmt.Errorf("the API answered with a server error (%d)", resp.StatusCode)
	case int64(len(body)) > limit:
		return nil, nil, fmt.Errorf("the answer is larger than %d bytes", limit)
	}
	return resp, body, nil
}

// parseOpenAPI reads an OpenAPI (or Swagger 2) document's version, title
// and declared paths.
func parseOpenAPI(body []byte) (map[string]any, error) {
	var doc struct {
		OpenAPI string                          `json:"openapi"`
		Swagger string                          `json:"swagger"`
		Info    struct{ Title, Version string } `json:"info"`
		Paths   map[string]json.RawMessage      `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("the OpenAPI document is not JSON")
	}
	version := doc.OpenAPI
	if version == "" {
		version = doc.Swagger
	}
	if version == "" {
		return nil, errors.New("the OpenAPI document names no openapi or swagger version")
	}
	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return map[string]any{
		"openapi":     factText(version),
		"title":       factText(doc.Info.Title),
		"api_version": factText(doc.Info.Version),
		"paths":       factList(paths),
	}, nil
}

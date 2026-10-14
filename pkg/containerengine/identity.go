// ReadIdentity: the engine identity onboarding pins and every session reads
// again.
package containerengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// Identity is what an engine says it is, read the way onboarding pins it.
type Identity struct {
	// ID is the identity to pin and compare (capability.EngineID's doc says
	// what it is for each engine).
	ID capability.EngineID
	// Version is the engine's release.
	Version string
}

// maxIdentityAnswer bounds the info answer read for an identity: a real one
// is a few kilobytes, and whatever answers on the socket is untrusted.
const maxIdentityAnswer = 1 << 20

// ReadIdentity reads kind's engine identity over dial, through a guard that
// allows only the one read it needs. Onboarding pins what it returns, and a
// Session compares against the pin before any other request.
func ReadIdentity(ctx context.Context, dial Dialer, kind capability.EngineKind) (Identity, error) {
	switch kind {
	case capability.EngineDocker:
		var info struct {
			ID            string `json:"ID"`
			ServerVersion string `json:"ServerVersion"`
		}
		if err := getJSON(ctx, guard(dial, []Op{OpInfo}), "/info", &info); err != nil {
			return Identity{}, err
		}
		if info.ID == "" {
			return Identity{}, errors.New("containerengine: the Docker engine reported no ID")
		}
		return Identity{ID: capability.EngineID(info.ID), Version: info.ServerVersion}, nil
	case capability.EnginePodman:
		var info struct {
			Host struct {
				Hostname string `json:"hostname"`
			} `json:"host"`
			Store struct {
				GraphRoot string `json:"graphRoot"`
				RunRoot   string `json:"runRoot"`
			} `json:"store"`
			Version struct {
				Version string `json:"Version"`
			} `json:"version"`
		}
		if err := getJSON(ctx, guard(dial, []Op{OpLibpodInfo}), "/v5.0.0/libpod/info", &info); err != nil {
			return Identity{}, err
		}
		if info.Store.GraphRoot == "" || info.Store.RunRoot == "" {
			return Identity{}, errors.New("containerengine: the Podman engine reported no container store")
		}
		return Identity{ID: podmanID(info.Host.Hostname, info.Store.GraphRoot, info.Store.RunRoot), Version: info.Version.Version}, nil
	default:
		return Identity{}, fmt.Errorf("containerengine: no identity for engine %q", kind)
	}
}

// podmanID digests a Podman store's location. Each part is length-prefixed
// so no two different locations digest alike.
func podmanID(hostname, graphRoot, runRoot string) capability.EngineID {
	h := sha256.New()
	for _, part := range []string{hostname, graphRoot, runRoot} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return capability.EngineID("podman-store:" + hex.EncodeToString(h.Sum(nil))[:32])
}

// getJSON sends one GET over dial and decodes a 2xx answer into out.
func getJSON(ctx context.Context, dial Dialer, path string, out any) error {
	client := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://engine"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("containerengine: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityAnswer+1))
	if err != nil {
		return fmt.Errorf("containerengine: reading GET %s: %w", path, err)
	}
	if len(body) > maxIdentityAnswer {
		return fmt.Errorf("containerengine: GET %s answered more than %d bytes", path, maxIdentityAnswer)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("containerengine: GET %s answered %s: %s", path, resp.Status, truncate(string(body), 512))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("containerengine: decoding GET %s: %w", path, err)
	}
	return nil
}

// truncate bounds text quoted from an engine into an error.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

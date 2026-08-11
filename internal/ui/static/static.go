// Package static embeds every asset the web UI serves, so the controller
// binary carries its own front end and reaches no network to render a page.
//
// This is the air-gap requirement made structural rather than documented.
// A CDN reference in a disconnected deployment is not a slow page, it is a
// page that never renders, and there is no configuration setting that
// rescues it at runtime. Embedding also removes the class of failure where
// a deployment's assets and its binary are different versions of the same
// application.
//
// It follows internal/api/wellknown exactly: a go:embed-only package whose
// coverage floor entry says so, with the one piece of real logic (content
// hashing) carrying its own test.
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Digests of the vendored third-party bundles, recorded from the
// upstream-published values in vendor/PROVENANCE.md.
//
// checksums_test.go asserts the embedded bytes still hash to these, which
// is what makes a silent substitution a build failure rather than a
// deployment. The constants are the reviewed values; the files are what
// actually ship, and the test is the only thing that keeps the two
// honest.
const (
	EChartsSHA256 = "bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1"
	HTMXSHA256    = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de"
)

// assets holds every file this UI serves.
//
// The vendor directory is embedded whole, licence and notice files
// included. Those are not incidental: the binary redistributes
// Apache-2.0-licensed code, and the NOTICE file has to travel with it.
//
//go:embed app.css app.js chart.js vendor
var assets embed.FS

// hashedNames maps a logical asset path to its content-hashed serving
// path, computed once at init from the embedded bytes.
var hashedNames = map[string]string{}

// contents maps a hashed serving path back to the bytes and content type
// to answer with.
var contents = map[string]servedAsset{}

type servedAsset struct {
	body        []byte
	contentType string
}

func init() {
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := assets.ReadFile(p)
		if readErr != nil {
			return readErr
		}

		sum := sha256.Sum256(body)
		hashed := hashedPath(p, hex.EncodeToString(sum[:])[:12])

		hashedNames[p] = hashed
		contents[hashed] = servedAsset{body: body, contentType: contentTypeFor(p)}
		return nil
	})
	if err != nil {
		// The files are embedded at build time, so a failure here is a
		// broken build rather than a runtime condition, and panicking at
		// process start is the only honest response: a controller that
		// cannot read its own UI must not start and serve blank pages.
		panic("static: failed to index embedded assets: " + err.Error())
	}
}

// hashedPath inserts a content hash before a file's extension, so
// app.css becomes app.<hash>.css.
//
// Cache-busting by filename rather than by query string is what lets these
// be served immutable and cached forever: a query string is advisory and
// several proxies ignore it, while a different path is a different
// resource to everything in the chain. It also needs no build step, which
// matters when the whole point of this phase was removing one.
func hashedPath(p, hash string) string {
	ext := path.Ext(p)
	return strings.TrimSuffix(p, ext) + "." + hash + ext
}

// contentTypeFor returns the type to declare for an asset.
//
// Types are declared explicitly and never sniffed. Combined with the
// nosniff header the UI sets, that is what stops a browser deciding for
// itself that some file is executable script.
func contentTypeFor(p string) string {
	switch path.Ext(p) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".md", ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// Path returns the content-hashed serving path for a logical asset name,
// e.g. Path("app.css") -> "app.9f2c1b0ae3d4.css".
//
// It panics on an unknown name because every caller is a template naming
// a file that is committed next to this one: a miss is a typo the build
// should not survive, not a runtime condition to degrade through. A
// template referencing a stale path is exactly how a page ends up
// unstyled in production and fine in development.
func Path(name string) string {
	hashed, ok := hashedNames[name]
	if !ok {
		panic(fmt.Sprintf("static: no embedded asset named %q", name))
	}
	return hashed
}

// Handler serves the embedded assets under prefix.
//
// Everything it serves is immutable by construction: a changed file has a
// different hash and therefore a different URL, so a year-long cache
// lifetime can never serve stale content. Nothing here touches the
// filesystem, so path traversal has no filesystem to reach -- an unknown
// path is a map miss and a 404.
func Handler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		name = strings.TrimPrefix(name, "/")

		asset, ok := contents[name]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", asset.contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(asset.body)
	})
}

// Read returns an embedded asset's bytes by logical name, for tests and
// for the checksum assertions.
func Read(name string) ([]byte, error) { return assets.ReadFile(name) }

// Package restconf: net.restconf.config's run. Every refusal is made before
// anything is written: the parameters, the path, the content, the module and
// the datastore against what the device's discovery said. The secret
// references are resolved just before the write, the resource is read before
// and after it, and the undo is recorded only where it is exact.
package restconf

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// configFQCN is the method's name.
const configFQCN = "net.restconf.config"

// writeRequest is one checked call.
type writeRequest struct {
	path      restconf.Path
	method    string
	asJSON    bool
	mediaType string
	datastore string
	content   string
	backup    bool
}

// Config implements the "net.restconf.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := configRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if device == nil {
		return collection.Result{}, errors.New(configFQCN + ": the task has no target device")
	}
	// The read's own verified handshakes say which certificate the device
	// presented (tls.certificate.*), as net.restconf.facts records it.
	ctx, seen := devicetls.WithPresented(ctx)
	client, warnings, err := restconf.ClientForContext(ctx, device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", configFQCN, device.Name(), err)
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	d, err := client.Discover(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", configFQCN, device.Name(), err)
	}
	if err := confirmedRoot(d); err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w; nothing was written", configFQCN, device.Name(), err)
	}
	found, err := discoveryWarnings(rc, client.Verified(), d)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if d.Learned() == nil {
		// As net.restconf.facts records it, and only beside a discovery that
		// learned something: a write past one that learned nothing records
		// no fact at all.
		certificate, err := recordCertificate(rc, device, seen)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
		}
		found = append(found, certificate...)
	}
	if err := checkAgainst(req, d); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w; nothing was sent", configFQCN, err)
	}
	res, notes, err := write(ctx, rc, client, req)
	found = append(found, notes...)
	if len(found) > 0 {
		if werr := sdk.RecordWarnings(rc, append(append([]string(nil), warnings...), found...)); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", configFQCN, device.Name(), err)
	}
	return res, nil
}

// confirmedRoot refuses a write to a root nothing confirmed: host-meta named
// none, so the conventional /restconf was taken, and neither its
// capabilities nor its YANG library answered as RESTCONF there. The reads
// that found that out sent the credential; the document is not sent after
// them to whatever serves that path.
func confirmedRoot(d restconf.Discovery) error {
	learned := d.Learned()
	if d.RootSource != restconf.RootDefault || learned == nil {
		return nil
	}
	return fmt.Errorf("host-meta named no RESTCONF root, so the conventional /restconf was taken, and nothing under it answered as RESTCONF (%w), so the write is refused rather than sent there", learned)
}

// discoveryWarnings records what the discovery learned, as every session of
// net.restconf.facts does, or says why nothing was recorded: a write goes on
// when the discovery learned nothing, since only its checks need it.
func discoveryWarnings(rc sdk.RunbookContext, verified bool, d restconf.Discovery) ([]string, error) {
	if err := d.Learned(); err != nil {
		return []string{"no discovery fact is recorded: " + err.Error()}, nil
	}
	return recordDiscovery(rc, verified, d)
}

// configRequest reads and checks the parameters, refusing every malformed
// one before anything is reached.
func configRequest(params map[string]any) (writeRequest, error) {
	var req writeRequest
	rawPath, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return req, err
	}
	if req.path, err = restconf.ParsePath(rawPath); err != nil {
		return req, fmt.Errorf("%s: %w", paramPath, err)
	}
	method, err := stringOr(params, paramMethod, "post")
	if err != nil {
		return req, err
	}
	req.method = strings.ToUpper(method)
	if !slices.Contains([]string{restconf.MethodPost, restconf.MethodPut, restconf.MethodPatch, restconf.MethodDelete}, req.method) {
		return req, fmt.Errorf("%s must be post, put, patch or delete", paramMethod)
	}
	format, err := stringOr(params, paramFormat, "json")
	if err != nil {
		return req, err
	}
	switch strings.ToLower(format) {
	case "json":
		req.asJSON, req.mediaType = true, restconf.MediaTypeJSON
	case "xml":
		req.mediaType = restconf.MediaTypeXML
	default:
		return req, fmt.Errorf("%s must be json or xml", paramFormat)
	}
	datastore, err := stringOr(params, paramDatastore, "")
	if err != nil {
		return req, err
	}
	req.datastore = strings.ToLower(datastore)
	if req.datastore != "" && req.datastore != "running" && req.datastore != "startup" {
		return req, fmt.Errorf("%s must be running or startup: RESTCONF has no commit, so an edit to candidate would stay staged for the next commit by any client", paramDatastore)
	}
	if req.backup, err = sdk.BoolParamOr(params, paramBackup, false); err != nil {
		return req, err
	}
	req.content, err = stringOr(params, paramContent, "")
	if err != nil {
		return req, err
	}
	return req, checkContent(req)
}

// checkContent holds content to the method: required but for delete, and
// then one document of the format, no larger than a write may be, holding no
// masked placeholder.
func checkContent(req writeRequest) error {
	if req.method == restconf.MethodDelete {
		if req.content != "" {
			return fmt.Errorf("%s is refused with delete, which sends no document", paramContent)
		}
		return nil
	}
	if strings.TrimSpace(req.content) == "" {
		return fmt.Errorf("%s is required for %s", paramContent, strings.ToLower(req.method))
	}
	if len(req.content) > restconf.MaxWriteBytes {
		return fmt.Errorf("%s is larger than %d bytes", paramContent, restconf.MaxWriteBytes)
	}
	if err := netcfg.RefuseMasked(paramContent, req.content); err != nil {
		return err
	}
	// The references are checked in place, unresolved: JSON that parses
	// holds each one inside a string, since {{ outside a string does not
	// parse, and XML that is well-formed holds each one as text.
	return yangdoc.CheckDocument(req.content, req.asJSON)
}

// checkAgainst refuses what the device's discovery says it cannot take: a
// module it does not implement (only when its library was read whole), and
// a datastore its library does not list.
func checkAgainst(req writeRequest, d restconf.Discovery) error {
	module := req.path[0].Module
	if served, known := d.ServesModule(module); known && !served {
		return fmt.Errorf("the device does not implement module %s, the first node of %s", module, paramPath)
	}
	if req.datastore != "" && !slices.Contains(d.Datastores, req.datastore) {
		return fmt.Errorf("the device's YANG library does not list the %s datastore (it lists %v), so it has no RFC 8527 resource to write", req.datastore, d.Datastores)
	}
	return nil
}

// resolveContent is content with each secret reference resolved from the
// run's named secrets, escaped for the format (JSON string content, or XML
// text), and the values the client and the run mask: the raw value joins the
// run's masking set as the named secret hands it out, and the escaped form
// joins it here, so an answer echoing either is masked. Content with no
// reference is returned as it is.
func resolveContent(rc sdk.RunbookContext, client *restconf.Client, content string, asJSON bool) (string, error) {
	if !secretref.Has(content) {
		return content, nil
	}
	ns, ok := rc.(sdk.NamedSecrets)
	if !ok {
		return "", sdk.ErrNoNamedSecrets
	}
	var raw []string
	out, values, err := secretref.Resolve(content, func(r secretref.Ref) (string, bool, error) {
		v, found, err := ns.NamedSecret(r.Name, r.Field)
		if err != nil || !found {
			return "", found, err
		}
		raw = append(raw, v)
		return escapeFor(v, asJSON), true, nil
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", paramContent, err)
	}
	client.MaskAlso(append(raw, values...)...)
	sdk.RegisterSecret(rc, values...)
	return out, nil
}

// escapeFor is v as the inside of a JSON string, or as XML text.
func escapeFor(v string, asJSON bool) string {
	if asJSON {
		quoted, _ := json.Marshal(v) // #nosec G104 -- a string always marshals
		return string(quoted[1 : len(quoted)-1])
	}
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(v)) // #nosec G104 -- strings.Builder writes never fail
	return b.String()
}

// stringOr is an optional text parameter, fallback when it is absent or
// empty, and an error when it is not text.
func stringOr(params map[string]any, key, fallback string) (string, error) {
	v, err := sdk.OptionalStringParam(params, key)
	if err != nil || v != "" {
		return v, err
	}
	return fallback, nil
}

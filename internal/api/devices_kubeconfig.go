// POST /inventory/kubeconfig-imports: one kubeconfig context made a
// kubernetes_cluster device and a stored Kubernetes credential, the
// Controller's counterpart to `pleiades import-kubeconfig` (Phase 119c).
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// maxKubeconfigImportBody bounds an import's body: kubeapi.Import's own
// one mebibyte bound on the kubeconfig, which JSON escaping can grow by
// some, and the few other fields.
const maxKubeconfigImportBody = 3 << 20

// KubeconfigCredentials is the credential store an import keeps a
// cluster's token or certificate in: credstore.Store's own methods.
type KubeconfigCredentials interface {
	EnsureManagedType(ctx context.Context, ct credtype.CredentialType) (credstore.CredentialType, error)
	ListCredentials(ctx context.Context, organizationID int) ([]credstore.Credential, error)
	CreateCredential(ctx context.Context, organizationID, typeID int, name, description string, inputs, external map[string]string, opts ...credstore.CredentialOption) (credstore.Credential, error)
	UpdateCredential(ctx context.Context, id int, name, description string, inputs, external map[string]string, opts ...credstore.CredentialOption) (credstore.Credential, error)
	DeleteCredential(ctx context.Context, id int) error
}

// WithKubeconfigImport enables ImportKubeconfig, keeping the credentials
// it imports in store.
func (h *DeviceHandler) WithKubeconfigImport(store KubeconfigCredentials) *DeviceHandler {
	h.kubeconfigCredentials = store
	return h
}

// kubeconfigImportRequest is an import's body.
type kubeconfigImportRequest struct {
	Name         string `json:"name"`
	Kubeconfig   string `json:"kubeconfig"`
	Context      string `json:"context"`
	Organization int    `json:"organization"`
	Replace      bool   `json:"replace"`
}

// kubeconfigImportDTO is what an import reports: the cluster, and the
// credential it stored, with no secret in it.
type kubeconfigImportDTO struct {
	LinkSet

	Device                  deviceDTO `json:"device"`
	Credential              int       `json:"credential"`
	CredentialName          string    `json:"credential_name"`
	CredentialTypeNamespace string    `json:"credential_type_namespace"`
	Context                 string    `json:"context"`
	Server                  string    `json:"server"`
	Namespace               string    `json:"namespace"`
	Identity                string    `json:"identity"`
	PinnedCA                bool      `json:"pinned_ca"`
	Replaced                bool      `json:"replaced"`
}

// ImportKubeconfig makes one kubeconfig context a kubernetes_cluster
// device and a Kubernetes credential in the given organization: the
// server, its authority and server name, and the namespace become the
// device's record; the token or the client certificate and key, with the
// authority, become a kubernetes_bearer_token or
// pleiades_kubernetes_client_certificate credential named after the
// device, encrypted at rest, ready to bind to a template. Nothing that
// would run a program or read a file is taken (kubeapi.Import refuses each
// by name).
//
// The route's own scope is credential:write, since it stores a secret; it
// also requires inventory:write, since it writes a device. A new cluster's
// credential is written first and removed again if the device cannot be
// created; a replacement saves the record first, so a concurrent edit
// refuses it before anything changes.
func (h *DeviceHandler) ImportKubeconfig(w http.ResponseWriter, r *http.Request) {
	if h.factory == nil || h.kubeconfigCredentials == nil {
		RespondError(w, r, http.StatusNotImplemented, "kubeconfig import is not enabled")
		return
	}
	if identity, ok := IdentityFromContext(r.Context()); !ok || !identity.HasScope(auth.ScopeInventoryWrite) {
		RespondError(w, r, http.StatusForbidden, "a kubeconfig import writes a device, which needs inventory:write")
		return
	}
	var req kubeconfigImportRequest
	if !decodeJSONWithin(w, r, &req, maxKubeconfigImportBody) {
		return
	}
	if req.Name == "" || len(req.Name) > maxDeviceNameLength || strings.ContainsAny(req.Name, "\x00\r\n") {
		RespondError(w, r, http.StatusBadRequest, "invalid device name")
		return
	}
	if req.Organization <= 0 {
		RespondError(w, r, http.StatusBadRequest, "organization is required: the credential belongs to one")
		return
	}
	imported, err := kubeapi.Import([]byte(req.Kubeconfig), req.Context)
	if err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ctx := r.Context()
	existing, err := h.repo.GetByName(ctx, req.Name)
	switch {
	case errors.Is(err, inventory.ErrItemNotFound):
		existing = nil
	case err != nil:
		h.writeRepositoryError(w, r, "read", req.Name, err)
		return
	case !req.Replace:
		RespondError(w, r, http.StatusConflict, "device already exists; set replace to replace its connection settings and credential")
		return
	}
	if existing != nil {
		if typed, ok := existing.(deviceTyped); !ok || typed.DeviceType() != kubernetes.TypeCluster {
			RespondError(w, r, http.StatusConflict, "the device is not a kubernetes_cluster, so an import cannot replace it")
			return
		}
		// The import puts the cluster in the request's organization (see
		// assignImportedOrganization), and never moves one out of another:
		// refused before anything is written.
		if reader, ok := h.repo.(inventory.OrganizationReader); ok {
			org, err := reader.DeviceOrganization(ctx, req.Name)
			if err != nil {
				h.writeRepositoryError(w, r, "read", req.Name, err)
				return
			}
			if org != 0 && org != req.Organization {
				RespondError(w, r, http.StatusConflict, "the cluster belongs to another organization, so an import into this one cannot replace it")
				return
			}
		}
	}

	item, err := h.importedCluster(req.Name, existing, clusterProperties(imported))
	if err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, fmt.Sprintf("cluster %q not imported: %v", req.Name, err))
		return
	}

	var stored credstore.Credential
	if existing != nil {
		// A replacement saves the record first: its save is the one the
		// version guard can refuse, and a refusal then changes nothing.
		if err := h.repo.Save(ctx, item); err != nil {
			h.writeRepositoryError(w, r, "import", req.Name, err)
			return
		}
		if stored, _, err = h.storeKubeconfigCredential(ctx, req, imported); err != nil {
			h.logger.ErrorContext(ctx, "storing an imported cluster credential failed", "device", req.Name, "error", err.Error())
			RespondError(w, r, http.StatusConflict, fmt.Sprintf("the record of cluster %q was replaced, but its credential was not: %v; import it again with replace", req.Name, err))
			return
		}
	} else {
		// A new cluster stores its credential first, and removes it again
		// if the record cannot be created (a name taken meanwhile), so a
		// refused import leaves neither behind.
		var undo func()
		if stored, undo, err = h.storeKubeconfigCredential(ctx, req, imported); err != nil {
			h.logger.ErrorContext(ctx, "storing an imported cluster credential failed", "device", req.Name, "error", err.Error())
			RespondError(w, r, http.StatusConflict, err.Error())
			return
		}
		if err := h.repo.Create(ctx, item); err != nil {
			undo()
			h.writeRepositoryError(w, r, "import", req.Name, err)
			return
		}
	}
	if err := h.assignImportedOrganization(ctx, req); err != nil {
		h.logger.ErrorContext(ctx, "putting an imported cluster in its organization failed", "device", req.Name, "error", err.Error())
		RespondError(w, r, http.StatusConflict, fmt.Sprintf("cluster %q and its credential were stored, but the cluster was not put in organization %d: %v; import it again with replace", req.Name, req.Organization, err))
		return
	}
	saved, err := h.repo.GetByName(ctx, req.Name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", req.Name, err)
		return
	}

	namespace := imported.Namespace
	if namespace == "" {
		namespace = "default"
	}
	identity := "token"
	if imported.Token == "" {
		identity = "client certificate"
	}
	dto := kubeconfigImportDTO{Device: toDeviceDTO(saved), Credential: stored.ID, CredentialName: stored.Name,
		CredentialTypeNamespace: stored.TypeNamespace, Context: imported.Context, Server: imported.Server,
		Namespace: namespace, Identity: identity, PinnedCA: len(imported.CAPEM) > 0, Replaced: existing != nil}
	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	Respond(w, r, status, &dto)
}

// assignImportedOrganization puts the imported cluster in the organization
// its credential was stored in. Onboarding lends a stored credential only to
// a cluster of the credential's own organization
// (resolve.BorrowedKubernetesSecrets), so without this an imported cluster,
// which has no credential of its own, could not onboard with the credential
// imported beside it. A repository with no organizations (a file
// inventory) is left as it is.
func (h *DeviceHandler) assignImportedOrganization(ctx context.Context, req kubeconfigImportRequest) error {
	assigner, ok := h.repo.(inventory.OrganizationAssigner)
	if !ok {
		return nil
	}
	return assigner.AssignDeviceOrganization(ctx, req.Name, req.Organization)
}

// importedCluster is the device an import writes: a new kubernetes_cluster
// with props, or existing with its connection properties
// (kubernetes.ConnectionProperties) set to props' as recorded changes,
// every other property, its identity, tags and history kept, through the
// item's own change methods, since a repository saves the revisions they
// record and nothing else (FAILURE_PATTERNS 261 and 432). Either is
// checked by the factory every store builds with before anything is
// written. A changed API server or authority leaves what onboarding proved
// stale, granting nothing until the cluster is onboarded again.
func (h *DeviceHandler) importedCluster(name string, existing pkginventory.InventoryItem, props map[string]pkginventory.PropertyValue) (pkginventory.InventoryItem, error) {
	if existing == nil {
		return h.factory.Build(record.Record{ID: newDeviceID(), Name: name, Type: kubernetes.TypeCluster, Properties: props,
			State: record.InitialState(kubernetes.TypeCluster), Source: pkginventory.SourceAuthority{Plugin: "api"}})
	}
	before := existing.Properties().Raw()
	for _, key := range kubernetes.ConnectionProperties {
		value, set := props[key]
		_, had := before[key]
		var err error
		switch {
		case set:
			err = existing.AddInfo(key, value, true)
		case had:
			err = existing.RemoveInfo(key)
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err := h.factory.Build(record.Record{ID: existing.ID(), Name: name, Type: kubernetes.TypeCluster, Properties: existing.Properties().Raw()}); err != nil {
		return nil, err
	}
	return existing, nil
}

// clusterProperties is the record an import writes: the server, its
// authority and server name, and the namespace.
func clusterProperties(imported kubeapi.Imported) map[string]pkginventory.PropertyValue {
	props := map[string]pkginventory.PropertyValue{kubernetes.APIServerProperty: imported.Server}
	if len(imported.CAPEM) > 0 {
		props[devicetls.CAPEMProperty] = string(imported.CAPEM)
	}
	if imported.ServerName != "" {
		props[devicetls.ServerNameProperty] = imported.ServerName
	}
	if imported.Namespace != "" {
		props[kubernetes.NamespaceProperty] = imported.Namespace
	}
	return props
}

// storeKubeconfigCredential writes the imported identity as the device's
// Kubernetes credential, "<device> (kubeconfig)", creating it or, for a
// replacement, updating the one an earlier import made. It returns an undo
// that removes a credential it created, for a device write that then
// fails; an updated one cannot be put back (the store returns its old
// values redacted), which is why a replacement writes its record first. A
// credential of that name of another type (a token replaced by a
// certificate) is replaced by a new one.
func (h *DeviceHandler) storeKubeconfigCredential(ctx context.Context, req kubeconfigImportRequest, imported kubeapi.Imported) (credstore.Credential, func(), error) {
	namespace := "kubernetes_bearer_token"
	inputs := map[string]string{credtype.KubernetesInputHost: imported.Server}
	if imported.Token != "" {
		inputs[credtype.KubernetesInputBearerToken] = imported.Token
	} else {
		namespace = "pleiades_kubernetes_client_certificate"
		inputs[credtype.KubernetesInputClientCert] = string(imported.ClientCertPEM)
		inputs[credtype.KubernetesInputClientKey] = string(imported.ClientKeyPEM)
	}
	if len(imported.CAPEM) > 0 {
		inputs[credtype.KubernetesInputCACert] = string(imported.CAPEM)
	}
	ct, ok := managed.Type(namespace)
	if !ok {
		return credstore.Credential{}, nil, fmt.Errorf("this build ships no %s credential type", namespace)
	}
	typ, err := h.kubeconfigCredentials.EnsureManagedType(ctx, ct)
	if err != nil {
		return credstore.Credential{}, nil, fmt.Errorf("the %s credential type: %w", namespace, err)
	}

	name := req.Name + " (kubeconfig)"
	description := fmt.Sprintf("Imported from kubeconfig context %q for cluster %s.", imported.Context, req.Name)
	all, err := h.kubeconfigCredentials.ListCredentials(ctx, req.Organization)
	if err != nil {
		return credstore.Credential{}, nil, fmt.Errorf("listing the organization's credentials: %w", err)
	}
	for _, c := range all {
		if c.Name != name {
			continue
		}
		if !req.Replace {
			return credstore.Credential{}, nil, fmt.Errorf("a credential named %q already exists; set replace to replace it", name)
		}
		if c.TypeID == typ.ID {
			updated, err := h.kubeconfigCredentials.UpdateCredential(ctx, c.ID, name, description, inputs, nil)
			if err != nil {
				return credstore.Credential{}, nil, fmt.Errorf("replacing credential %q: %w", name, err)
			}
			return updated, func() {}, nil
		}
		if err := h.kubeconfigCredentials.DeleteCredential(ctx, c.ID); err != nil {
			return credstore.Credential{}, nil, fmt.Errorf("replacing credential %q of another type: %w", name, err)
		}
	}
	created, err := h.kubeconfigCredentials.CreateCredential(ctx, req.Organization, typ.ID, name, description, inputs, nil)
	if err != nil {
		return credstore.Credential{}, nil, fmt.Errorf("storing credential %q: %w", name, err)
	}
	return created, func() { _ = h.kubeconfigCredentials.DeleteCredential(context.WithoutCancel(ctx), created.ID) }, nil
}

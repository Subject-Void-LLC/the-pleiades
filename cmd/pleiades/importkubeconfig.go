// pleiades import-kubeconfig: one context of a kubeconfig, made a
// kubernetes_cluster device and its stored credential.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// maxKubeconfigRead bounds what the command reads before kubeapi.Import
// applies its own bound, so a huge file is refused without being held.
const maxKubeconfigRead = 1<<20 + 1

// importResult is what the command reports, with no secret in it.
type importResult struct {
	Device    string `json:"device"`
	Context   string `json:"context"`
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
	Identity  string `json:"identity"`
	PinnedCA  bool   `json:"pinned_ca"`
}

// runImportKubeconfig imports one context: the server, its authority and
// server name, and the namespace become a kubernetes_cluster device's
// record; the token, or the client certificate and key, becomes its stored
// credential. Nothing in the kubeconfig that runs a program or names a file
// is taken (kubeapi.Import refuses each by name), and the kubeconfig is read
// from the path given, never from KUBECONFIG or a home directory.
func runImportKubeconfig(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"replace": true, "json": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades import-kubeconfig <device> --kubeconfig <path|-> [--context <name>] [--replace] [--json]: %w", err)
	}
	fs := flag.NewFlagSet("import-kubeconfig", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	path := fs.String("kubeconfig", "", "the kubeconfig to read, or - for standard input; never a default location")
	contextName := fs.String("context", "", "the context to import; the kubeconfig's current context when empty")
	replace := fs.Bool("replace", false, "replace the connection settings and credential of a cluster already in the inventory")
	asJSON := fs.Bool("json", false, "print the result as one JSON document")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("--kubeconfig is required: the import reads the file it is given, never KUBECONFIG or ~/.kube/config")
	}

	data, err := readKubeconfig(*path)
	if err != nil {
		return err
	}
	imported, err := kubeapi.Import(data, *contextName)
	if err != nil {
		return err
	}

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

	invPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(invPath)
	if err != nil {
		return fmt.Errorf("failed to read inventory (did you run 'pleiades init'?): %w", err)
	}
	index := -1
	for i, h := range hosts {
		if h.Name == name {
			index = i
		}
	}
	switch {
	case index >= 0 && !*replace:
		return fmt.Errorf("host %q already exists; pass --replace to replace its connection settings and credential", name)
	case index >= 0 && hosts[index].Type != kubernetes.TypeCluster:
		return fmt.Errorf("host %q is a %s, not a %s, so an import cannot replace it", name, hosts[index].Type, kubernetes.TypeCluster)
	}
	spec := inventory.HostSpec{ID: uuid.New().String(), Name: name, Type: kubernetes.TypeCluster, Properties: props}
	if index >= 0 {
		// A replacement keeps the record's identity, tags and every
		// property the import does not own (a route, a node engine host);
		// its connection settings are the import's alone, so one the
		// kubeconfig no longer sets does not linger. A changed server or
		// authority leaves what onboarding proved stale.
		kept := map[string]pkginventory.PropertyValue{}
		for k, v := range hosts[index].Properties {
			if !slices.Contains(kubernetes.ConnectionProperties, k) {
				kept[k] = v
			}
		}
		for k, v := range props {
			kept[k] = v
		}
		spec.ID, spec.Tags, spec.Properties = hosts[index].ID, hosts[index].Tags, kept
	}
	if _, err := inventory.NewItemFactory().Build(record.Record{
		ID: pkginventory.DeviceID(spec.ID), Name: name, Type: spec.Type, Properties: spec.Properties,
	}); err != nil {
		return fmt.Errorf("cluster %q not imported: %w", name, err)
	}
	if index >= 0 {
		hosts[index] = spec
	} else {
		hosts = append(hosts, spec)
	}

	cred := credential.Credential{Token: imported.Token, CertificatePEM: imported.ClientCertPEM, PrivateKeyPEM: imported.ClientKeyPEM}
	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}
	// The credential first: a record written without one would be a
	// cluster nothing can reach, while a credential written without its
	// record is inert until the record exists.
	if err := credential.SaveFileStore(*dir, key, name, cred); err != nil {
		return fmt.Errorf("failed to save the cluster's credential: %w", err)
	}
	if err := inventory.WriteHosts(invPath, hosts); err != nil {
		return err
	}

	identity := "token"
	if imported.Token == "" {
		identity = "client certificate"
	}
	namespace := imported.Namespace
	if namespace == "" {
		namespace = "default"
	}
	res := importResult{Device: name, Context: imported.Context, Server: imported.Server, Namespace: namespace, Identity: identity, PinnedCA: len(imported.CAPEM) > 0}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Printf("imported context %q as cluster %q (%s, namespace %s, %s)\n", res.Context, res.Device, res.Server, res.Namespace, res.Identity)
	if !res.PinnedCA {
		fmt.Println("the kubeconfig embeds no authority, so the cluster's certificate is checked against the system's roots")
	}
	fmt.Printf("next: pleiades onboard %s\n", name)
	return nil
}

// readKubeconfig reads path, or standard input for "-", refusing one past
// kubeapi's bound without holding it all.
func readKubeconfig(path string) ([]byte, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path) // #nosec G304 -- the operator names the file to import
		if err != nil {
			return nil, fmt.Errorf("reading the kubeconfig: %w", err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxKubeconfigRead))
	if err != nil {
		return nil, fmt.Errorf("reading the kubeconfig: %w", err)
	}
	return data, nil
}

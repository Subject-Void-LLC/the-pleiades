// The facts the Controller's records hold about a cluster that has no
// credential of its own, for deciding whether a stored one may be lent to it.
package main

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// borrowingCluster describes cluster for resolve.BorrowedKubernetesSecrets
// from the Controller's own records: its organization, whether a route of
// SSH hops reaches it (its own or one a group it belongs to sets), and the
// authority its record pins. The rule itself lives in resolve, where it is
// tested; this only reads the records it needs.
func borrowingCluster(ctx context.Context, repo inventory.Repository, credentials credential.Store, cluster pkginventory.InventoryItem) (resolve.BorrowingCluster, error) {
	k, ok := cluster.(capability.KubernetesCapable)
	if !ok {
		return resolve.BorrowingCluster{}, fmt.Errorf("device %s is not a Kubernetes cluster", cluster.Name())
	}
	b := resolve.BorrowingCluster{
		Name:        cluster.Name(),
		APIServer:   k.KubeAPIServer(),
		RecordCAPEM: string(devicetls.For(cluster).CAPEM()),
	}
	if reader, ok := repo.(inventory.OrganizationReader); ok {
		org, err := reader.DeviceOrganization(ctx, cluster.Name())
		if err != nil {
			return b, err
		}
		b.OrganizationID = org
	}
	route, err := engine.ResolveRoute(ctx, repo, credentials, cluster)
	if err != nil {
		return b, fmt.Errorf("resolving cluster %s's route: %w", cluster.Name(), err)
	}
	b.Routed = len(route) > 0
	return b, nil
}

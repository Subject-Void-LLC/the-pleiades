// Which organization a stored device belongs to.
package inventory

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
)

// OrganizationReader is satisfied by a repository that records which
// organization a device belongs to: the ent-backed one. A file inventory
// has no organizations, so it does not satisfy it, and a caller that needs
// the answer must refuse rather than assume one.
type OrganizationReader interface {
	// DeviceOrganization returns the id of the organization device name
	// belongs to, or zero when it belongs to none. A device that does not
	// exist is ErrItemNotFound.
	DeviceOrganization(ctx context.Context, name string) (int, error)
}

// OrganizationAssigner is satisfied by a repository that can put a device
// in an organization: the ent-backed one.
type OrganizationAssigner interface {
	// AssignDeviceOrganization puts device name in organization orgID. A
	// device already in that organization is left as it is; one in another
	// organization is refused with ErrCrossTenantMember and never moved,
	// since moving a device between tenants is not something a caller
	// assigning its own may do in passing.
	AssignDeviceOrganization(ctx context.Context, name string, orgID int) error
}

var (
	_ OrganizationReader   = (*entRepository)(nil)
	_ OrganizationAssigner = (*entRepository)(nil)
)

// AssignDeviceOrganization implements OrganizationAssigner.
func (r *entRepository) AssignDeviceOrganization(ctx context.Context, name string, orgID int) error {
	current, err := r.DeviceOrganization(ctx, name)
	switch {
	case err != nil:
		return err
	case current == orgID:
		return nil
	case current != 0:
		return fmt.Errorf("device %s: %w", name, ErrCrossTenantMember)
	}
	n, err := r.entClient(ctx).Device.Update().Where(device.NameEQ(name), device.Not(device.HasOrganization())).SetOrganizationID(orgID).Save(ctx)
	if err != nil {
		return fmt.Errorf("putting device %s in organization %d: %w", name, orgID, err)
	}
	if n != 1 {
		// Another writer gave it an organization between the read and the
		// update; read again so the answer is that writer's, not a guess.
		return r.AssignDeviceOrganization(ctx, name, orgID)
	}
	return nil
}

// DeviceOrganization implements OrganizationReader.
func (r *entRepository) DeviceOrganization(ctx context.Context, name string) (int, error) {
	dev, err := r.entClient(ctx).Device.Query().Where(device.NameEQ(name)).WithOrganization().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, fmt.Errorf("device %s: %w", name, ErrItemNotFound)
		}
		return 0, fmt.Errorf("reading device %s's organization: %w", name, err)
	}
	if dev.Edges.Organization == nil {
		return 0, nil
	}
	return dev.Edges.Organization.ID, nil
}

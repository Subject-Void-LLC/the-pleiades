package catalystcenter

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/catalystcenter"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Property keys this plugin writes onto every discovered device.
//
// They are namespaced with a catalyst_ prefix so a device enriched by a
// second source cannot silently collide with them, and so an operator
// reading a hosts.yaml entry can tell at a glance which fields came from
// the controller rather than from a human.
//
// host and ip are the two exceptions, deliberately unprefixed: they are the
// keys the rest of this codebase already reads (internal/transport/ssh
// resolves a target through the host property, and internal/api's
// dispatcher reads ip), so writing them under a plugin-specific name would
// produce a device nothing else can reach.
const (
	propHost = "host"
	propIP   = "ip"

	propCatalystRole    = "catalyst_role"
	propCatalystID      = "catalyst_id"
	propFamily          = "catalyst_family"
	propSeries          = "catalyst_series"
	propPlatformID      = "catalyst_platform_id"
	propSoftwareType    = "catalyst_software_type"
	propSoftwareVersion = "catalyst_software_version"
	propSerialNumber    = "catalyst_serial_number"
	propMACAddress      = "catalyst_mac_address"
	propTopologyRole    = "catalyst_topology_role"
	propReachability    = "catalyst_reachability_status"
	propCollection      = "catalyst_collection_status"
	propUptime          = "catalyst_uptime"
	propBaseURL         = "catalyst_base_url"
)

// roleController is the propCatalystRole value marking the record that
// describes the Catalyst Center itself rather than a device it manages.
// Classify branches on it, which is why it is a constant rather than a
// string repeated in two files.
const roleController = "controller"

// roleManaged marks a device the controller manages.
const roleManaged = "managed_device"

// controllerRecord builds the Record describing the Catalyst Center itself.
//
// Its ID is derived from the base URL rather than from anything the
// controller reports, because the controller has no entry in its own device
// inventory to take an ID from. The URL is stable for a given deployment,
// which is what a DeviceID has to be.
func controllerRecord(baseURL string) record.Record {
	return record.Record{
		ID:   inventory.DeviceID("catalyst-center:" + baseURL),
		Name: controllerName(baseURL),
		Properties: map[string]inventory.PropertyValue{
			propCatalystRole: roleController,
			propBaseURL:      baseURL,
			propHost:         hostFromURL(baseURL),
			propIP:           hostFromURL(baseURL),
		},
		Tags: []inventory.Tag{"catalyst_center"},
	}
}

// deviceRecord converts one upstream device into a storage-agnostic Record.
//
// Type, Capabilities, and State are deliberately left unset: Discover
// yields unclassified records and Classify is what fills those in. Setting
// them here would put classification in two places.
func deviceRecord(d catalystcenter.Device) record.Record {
	props := map[string]inventory.PropertyValue{
		propCatalystRole: roleManaged,
		propCatalystID:   d.ID,
	}

	// Only non-empty upstream fields are written. An empty string stored as
	// a property is indistinguishable from a real empty value later, and
	// the sandbox returns several fields blank depending on device type.
	for key, value := range map[string]string{
		propHost:            d.ManagementIPAddress,
		propIP:              d.ManagementIPAddress,
		propFamily:          d.Family,
		propSeries:          d.Series,
		propPlatformID:      d.PlatformID,
		propSoftwareType:    d.SoftwareType,
		propSoftwareVersion: d.SoftwareVersion,
		propSerialNumber:    d.SerialNumber,
		propMACAddress:      d.MACAddress,
		propTopologyRole:    d.Role,
		propReachability:    d.ReachabilityStatus,
		propCollection:      d.CollectionStatus,
		propUptime:          d.UpTime,
	} {
		if value != "" {
			props[key] = value
		}
	}

	return record.Record{
		// The controller's own UUID is the stable identifier, not the
		// hostname: a hostname is renameable and DeviceID must not be.
		ID:         inventory.DeviceID(d.ID),
		Name:       deviceName(d),
		Properties: props,
	}
}

// deviceName picks the display name for a device, preferring the hostname
// and falling back to the management address. A device with neither would
// be unaddressable, so the upstream ID is the last resort rather than an
// empty name that GetByName could never match.
func deviceName(d catalystcenter.Device) string {
	switch {
	case d.Hostname != "":
		return d.Hostname
	case d.ManagementIPAddress != "":
		return d.ManagementIPAddress
	default:
		return d.ID
	}
}

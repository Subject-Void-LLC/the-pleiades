package catalystcenter

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// devicesPath is the managed-device inventory endpoint.
const devicesPath = "/dna/intent/api/v1/network-device"

// Device is one managed device as Catalyst Center reports it.
//
// The live API returns roughly fifty fields per device. This models the
// ones classification and fact gathering actually use, and drops the rest
// on purpose: every modeled field is one this codebase can be asked to keep
// working, and a struct that mirrors an upstream schema wholesale acquires
// that obligation for fields nothing reads.
//
// Several numeric-looking fields (InterfaceCount, LineCardCount) come back
// as JSON strings rather than numbers, so they are typed as strings here
// rather than fought with a custom unmarshaler. That is the wire format,
// not a modeling choice.
type Device struct {
	ID                  string `json:"id"`
	Hostname            string `json:"hostname"`
	ManagementIPAddress string `json:"managementIpAddress"`
	MACAddress          string `json:"macAddress"`
	SerialNumber        string `json:"serialNumber"`

	// Family, Series, and PlatformID describe the hardware, for example
	// "Switches and Hubs", "Cisco Catalyst 9000 Series Virtual Switches",
	// and "C9KV-UADP-8P".
	Family     string `json:"family"`
	Series     string `json:"series"`
	PlatformID string `json:"platformId"`
	Type       string `json:"type"`

	// SoftwareType and SoftwareVersion are the OS family and release, for
	// example "IOS-XE" and "17.12.1prd9". SoftwareType is what
	// classification keys on; SoftwareVersion is platform-target data,
	// never a capability.
	SoftwareType    string `json:"softwareType"`
	SoftwareVersion string `json:"softwareVersion"`

	// Role is Catalyst Center's own topology role, for example "ACCESS",
	// "DISTRIBUTION", "CORE", or "BORDER ROUTER".
	Role string `json:"role"`

	// ReachabilityStatus and CollectionStatus are the two liveness signals
	// the controller maintains, for example "Reachable" and "Managed".
	ReachabilityStatus string `json:"reachabilityStatus"`
	CollectionStatus   string `json:"collectionStatus"`

	UpTime      string `json:"upTime"`
	Description string `json:"description"`
}

// deviceListResponse is the envelope every listing endpoint uses.
type deviceListResponse struct {
	Response []Device `json:"response"`
}

// countResponse is the shape of the /count endpoints.
type countResponse struct {
	Response int `json:"response"`
}

// DeviceCount returns how many devices the controller manages. The sync
// plugin uses it to report progress without buffering the whole fleet.
func (c *Client) DeviceCount(ctx context.Context) (int, error) {
	var decoded countResponse
	if err := c.get(ctx, devicesPath+"/count", nil, &decoded); err != nil {
		return 0, err
	}
	return decoded.Response, nil
}

// ListDevices returns one page of managed devices, starting at offset and
// containing at most limit entries.
//
// Catalyst Center's offset is one-based, not zero-based: requesting offset
// 0 and offset 1 both return the first page. Callers should use
// EachDevice rather than paging by hand, which is where that is handled.
func (c *Client) ListDevices(ctx context.Context, offset, limit int) ([]Device, error) {
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		return nil, fmt.Errorf("catalystcenter: device page limit must be positive, got %d", limit)
	}

	query := url.Values{}
	query.Set("offset", strconv.Itoa(offset))
	query.Set("limit", strconv.Itoa(limit))

	var decoded deviceListResponse
	if err := c.get(ctx, devicesPath, query, &decoded); err != nil {
		return nil, err
	}
	return decoded.Response, nil
}

// EachDevice pages through every managed device, calling fn once per
// device. It stops early and returns fn's error if fn returns one.
//
// Paging rather than returning a slice is the point: a large fleet must not
// have to fit in memory at once, which is the same property
// internal/inventory's keyset iterator already proves for the database
// side. A page smaller than pageSize ends the walk, which is how this API
// signals the last page (there is no total or next-cursor field to read).
func (c *Client) EachDevice(ctx context.Context, pageSize int, fn func(Device) error) error {
	if pageSize < 1 {
		return fmt.Errorf("catalystcenter: page size must be positive, got %d", pageSize)
	}

	for offset := 1; ; offset += pageSize {
		page, err := c.ListDevices(ctx, offset, pageSize)
		if err != nil {
			return err
		}
		for _, d := range page {
			if err := fn(d); err != nil {
				return err
			}
		}
		if len(page) < pageSize {
			return nil
		}
	}
}

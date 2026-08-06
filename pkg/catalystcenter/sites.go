package catalystcenter

import "context"

const (
	// sitesPath is the site hierarchy endpoint.
	sitesPath = "/dna/intent/api/v1/site"

	// tagsPath is the tag listing endpoint.
	tagsPath = "/dna/intent/api/v1/tag"
)

// Site is one entry in the controller's site hierarchy.
//
// NameHierarchy is the useful field and Name is not: the hierarchy is a
// full path like "Global/EU/Hungary/HU_B01", while Name is only its last
// component. Two different buildings can share a Name, so anything using a
// site as a grouping key has to use NameHierarchy or it will merge sites
// that are not the same place.
type Site struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	NameHierarchy string `json:"siteNameHierarchy"`
}

// siteListResponse is the site endpoint's envelope.
type siteListResponse struct {
	Response []Site `json:"response"`
}

// ListSites returns the controller's full site hierarchy.
//
// Unlike devices this is not paged. A site hierarchy is an organizational
// tree that is small by construction (the DevNet sandbox has 25 entries,
// and a real deployment has hundreds, not millions), so paging it would add
// a code path with nothing to exercise it.
func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	var decoded siteListResponse
	if err := c.get(ctx, sitesPath, nil, &decoded); err != nil {
		return nil, err
	}
	return decoded.Response, nil
}

// Tag is one tag defined on the controller.
//
// SystemTag distinguishes a tag Catalyst Center created for its own
// purposes (for example AUTO_INV_EVENT_SYNC_DISABLED) from one an operator
// created to mean something. Both are reported; a caller that only wants
// operator intent filters on this field.
type Tag struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SystemTag bool   `json:"systemTag"`
}

// tagListResponse is the tag endpoint's envelope.
type tagListResponse struct {
	Response []Tag `json:"response"`
}

// ListTags returns every tag defined on the controller. Like sites, this is
// small and unpaged.
func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	var decoded tagListResponse
	if err := c.get(ctx, tagsPath, nil, &decoded); err != nil {
		return nil, err
	}
	return decoded.Response, nil
}

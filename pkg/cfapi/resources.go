// Package cfapi: the resources it reads, and the names and ids that reach
// a request, each held to its form first.
package cfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// The forms an id or a name must have before it reaches a path or a query.
var (
	hexID      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	uuidID     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	tunnelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	hostname   = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}\.?$`)
	recordName = regexp.MustCompile(`^(\*\.|_[a-z0-9-]+\.)*([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.)*[a-z0-9-]{2,63}\.?$`)
)

// RecordTypes are the DNS record types edge.dns reads.
var RecordTypes = []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "CAA", "HTTPS", "SVCB", "PTR"}

// ValidAccountID reports whether id is an account id: 32 hex digits.
func ValidAccountID(id string) bool { return hexID.MatchString(id) }

// Account is an account as Cloudflare describes it.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Account reads the account account.
func (c *Client) Account(ctx context.Context, account string) (Account, error) {
	if !ValidAccountID(account) {
		return Account{}, fmt.Errorf("%q is not an account id", account)
	}
	return one[Account](ctx, c, "/accounts/"+account)
}

// Tunnel is a Cloudflare Tunnel.
type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	DeletedAt string `json:"deleted_at"`
}

// Connector is one cloudflared process serving a tunnel, and its
// connections to Cloudflare's edge.
type Connector struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	RunAt   string `json:"run_at"`
	Conns   []struct {
		ColoName           string `json:"colo_name"`
		IsPendingReconnect bool   `json:"is_pending_reconnect"`
	} `json:"conns"`
}

// Tunnel reads a tunnel by id, or by name among the tunnels not deleted,
// refusing a name that resolves to none or to more than one.
func (c *Client) Tunnel(ctx context.Context, account, idOrName string) (Tunnel, error) {
	if !ValidAccountID(account) {
		return Tunnel{}, fmt.Errorf("%q is not an account id", account)
	}
	if uuidID.MatchString(idOrName) {
		return one[Tunnel](ctx, c, "/accounts/"+account+"/cfd_tunnel/"+idOrName)
	}
	if !tunnelName.MatchString(idOrName) {
		return Tunnel{}, fmt.Errorf("%q is not a tunnel id or name", idOrName)
	}
	found, err := list[Tunnel](ctx, c, "/accounts/"+account+"/cfd_tunnel", url.Values{"name": {idOrName}, "is_deleted": {"false"}})
	if err != nil {
		return Tunnel{}, err
	}
	var exact []Tunnel
	for _, t := range found {
		if t.Name == idOrName && t.DeletedAt == "" {
			exact = append(exact, t)
		}
	}
	return single(exact, "tunnel", idOrName, func(t Tunnel) string { return t.ID })
}

// Connectors reads the connectors serving a tunnel.
func (c *Client) Connectors(ctx context.Context, account, tunnelID string) ([]Connector, error) {
	if !ValidAccountID(account) || !uuidID.MatchString(tunnelID) {
		return nil, fmt.Errorf("%q, %q is not an account and tunnel id", account, tunnelID)
	}
	return one[[]Connector](ctx, c, "/accounts/"+account+"/cfd_tunnel/"+tunnelID+"/connections")
}

// Policy is an Access policy: its decision and its rules, kept as JSON.
type Policy struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Decision   string          `json:"decision"`
	Precedence int             `json:"precedence"`
	Include    json.RawMessage `json:"include"`
	Exclude    json.RawMessage `json:"exclude"`
	Require    json.RawMessage `json:"require"`
}

// App is an Access application.
type App struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Domain          string   `json:"domain"`
	Type            string   `json:"type"`
	Audience        string   `json:"aud"`
	SessionDuration string   `json:"session_duration"`
	Policies        []Policy `json:"policies"`
}

// App reads an Access application by id, or by domain, refusing a domain
// that resolves to none or to more than one. Its policies are the
// application's own; when the application lists none they are read from
// its policies.
func (c *Client) App(ctx context.Context, account, idOrDomain string) (App, error) {
	if !ValidAccountID(account) {
		return App{}, fmt.Errorf("%q is not an account id", account)
	}
	var app App
	if uuidID.MatchString(idOrDomain) {
		var err error
		if app, err = one[App](ctx, c, "/accounts/"+account+"/access/apps/"+idOrDomain); err != nil {
			return App{}, err
		}
	} else {
		domain := strings.ToLower(idOrDomain)
		if !hostname.MatchString(strings.Split(domain, "/")[0]) {
			return App{}, fmt.Errorf("%q is not an application id or domain", idOrDomain)
		}
		apps, err := list[App](ctx, c, "/accounts/"+account+"/access/apps", nil)
		if err != nil {
			return App{}, err
		}
		var exact []App
		for _, a := range apps {
			if strings.EqualFold(a.Domain, domain) {
				exact = append(exact, a)
			}
		}
		if app, err = single(exact, "Access application", idOrDomain, func(a App) string { return a.ID }); err != nil {
			return App{}, err
		}
	}
	if len(app.Policies) == 0 {
		policies, err := list[Policy](ctx, c, "/accounts/"+account+"/access/apps/"+app.ID+"/policies", nil)
		if err != nil {
			return App{}, err
		}
		app.Policies = policies
	}
	return app, nil
}

// Organization is an account's Access organization.
type Organization struct {
	AuthDomain string `json:"auth_domain"`
	Name       string `json:"name"`
}

// Issuer is the issuer of the tokens Access signs for an application of
// account: https:// and the organization's team domain.
func (c *Client) Issuer(ctx context.Context, account string) (string, error) {
	if !ValidAccountID(account) {
		return "", fmt.Errorf("%q is not an account id", account)
	}
	org, err := one[Organization](ctx, c, "/accounts/"+account+"/access/organizations")
	if err != nil {
		return "", err
	}
	if org.AuthDomain == "" {
		return "", fmt.Errorf("the account's Access organization names no team domain")
	}
	return "https://" + org.AuthDomain, nil
}

// Zone is a DNS zone.
type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// Zone reads a zone by id, or by name within account, refusing a zone of
// another account: a token reaching two accounts must not read the other's
// zone by name.
func (c *Client) Zone(ctx context.Context, account, idOrName string) (Zone, error) {
	if !ValidAccountID(account) {
		return Zone{}, fmt.Errorf("%q is not an account id", account)
	}
	var z Zone
	if hexID.MatchString(idOrName) {
		var err error
		if z, err = one[Zone](ctx, c, "/zones/"+idOrName); err != nil {
			return Zone{}, err
		}
	} else {
		name := strings.TrimSuffix(strings.ToLower(idOrName), ".")
		if !hostname.MatchString(name) || strings.HasPrefix(name, "*.") {
			return Zone{}, fmt.Errorf("%q is not a zone id or name", idOrName)
		}
		zones, err := list[Zone](ctx, c, "/zones", url.Values{"name": {name}, "account.id": {account}})
		if err != nil {
			return Zone{}, err
		}
		var exact []Zone
		for _, zz := range zones {
			if zz.Name == name {
				exact = append(exact, zz)
			}
		}
		if z, err = single(exact, "zone", idOrName, func(z Zone) string { return z.ID }); err != nil {
			return Zone{}, err
		}
	}
	if z.Account.ID != account {
		return Zone{}, fmt.Errorf("zone %s belongs to account %s, not %s", z.Name, z.Account.ID, account)
	}
	return z, nil
}

// Record is a DNS record.
type Record struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Proxied  bool   `json:"proxied"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority"`
}

// Records reads the records of one name and type in a zone.
func (c *Client) Records(ctx context.Context, zoneID, name, typ string) ([]Record, error) {
	if !hexID.MatchString(zoneID) {
		return nil, fmt.Errorf("%q is not a zone id", zoneID)
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if !recordName.MatchString(name) {
		return nil, fmt.Errorf("%q is not a record name", name)
	}
	if !ValidRecordType(typ) {
		return nil, fmt.Errorf("%q is not a record type this reads (%s)", typ, strings.Join(RecordTypes, ", "))
	}
	records, err := list[Record](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"name": {name}, "type": {typ}})
	if err != nil {
		return nil, err
	}
	var exact []Record
	for _, r := range records {
		if strings.EqualFold(strings.TrimSuffix(r.Name, "."), name) && r.Type == typ {
			exact = append(exact, r)
		}
	}
	return exact, nil
}

// ValidRecordType reports whether typ is a record type this reads.
func ValidRecordType(typ string) bool {
	for _, t := range RecordTypes {
		if t == typ {
			return true
		}
	}
	return false
}

// single is the one item of found, refusing none and more than one.
func single[T any](found []T, what, named string, id func(T) string) (T, error) {
	var zero T
	switch len(found) {
	case 0:
		return zero, fmt.Errorf("no %s is named %s: %w", what, named, ErrNotFound)
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, f := range found {
		ids[i] = id(f)
	}
	return zero, fmt.Errorf("%d of the account's %ss are named %s (%s); name one by its id", len(found), what, named, strings.Join(ids, ", "))
}

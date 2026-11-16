// Package snmp: the client. One Client is one agent and one credential,
// used by one task at a time; every request goes through do, which paces it,
// bounds it and turns gosnmp's answer or error into this package's own.
package snmp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Client is an SNMP session with one agent, as one credential.
type Client struct {
	mu       sync.Mutex
	g        *gosnmp.GoSNMP
	settings Settings
	cred     wire.SNMPCredential
	// secrets are the credential's values every error is masked of.
	secrets []string
	// last is when the previous request was sent, for MinInterval.
	last time.Time
	// sleep waits for d or until ctx ends; a test replaces it.
	sleep func(ctx context.Context, d time.Duration) error
}

// Dial builds a client for the agent at host, with settings s and
// credential cred, after Check refuses any mismatch between them. Nothing is
// sent: UDP has no handshake, and an SNMPv3 engine is discovered by the first
// request.
func Dial(host string, s Settings, cred wire.SNMPCredential) (*Client, error) {
	if err := s.Check(cred); err != nil {
		return nil, err
	}
	if host == "" {
		return nil, errors.New("the device has no host to reach its agent at")
	}
	if s.Port == 0 {
		s.Port = DefaultPort
	}
	g := &gosnmp.GoSNMP{
		Target:    host,
		Port:      uint16(s.Port), // #nosec G115 -- Parse bounds the port to 1..65535
		Transport: "udp",
		Timeout:   s.Timeout,
		Retries:   s.Retries,
		MaxOids:   MaxOIDsPerRequest,
		// gosnmp's default MaxRepetitions is replaced by the device's own.
		MaxRepetitions: uint32(s.MaxRepetitions), // #nosec G115 -- Parse bounds it to 1..100
	}
	if g.Timeout == 0 {
		g.Timeout = DefaultTimeout
	}
	switch s.Version {
	case Version1:
		g.Version, g.Community = gosnmp.Version1, cred.Community
	case Version2c:
		g.Version, g.Community = gosnmp.Version2c, cred.Community
	default:
		usm, flags, err := usmParameters(cred)
		if err != nil {
			return nil, err
		}
		g.Version, g.SecurityModel, g.MsgFlags, g.SecurityParameters = gosnmp.Version3, gosnmp.UserSecurityModel, flags, usm
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("opening a UDP socket to %s: %w", net.JoinHostPort(host, strconv.Itoa(s.Port)), err)
	}
	return &Client{g: g, settings: s, cred: cred, secrets: MaskedSecrets(cred.Secrets()), sleep: sleepCtx}, nil
}

// MaskedSecrets are the values of a flattened credential this package masks
// out of what it reports: wire.MaskedValues, the rule every path masks by,
// and a community too short for that rule, which the engine masks by shape
// alone and this package masks where it stands as its own word (MaskValues),
// since an agent's answer or an error is where an SNMP community is echoed.
func MaskedSecrets(secrets map[string]string) []string {
	out := wire.MaskedValues(secrets)
	if cred := wire.SNMPOf(secrets); cred.ShortCommunity() {
		out = append(out, cred.Community)
	}
	return out
}

// Close releases the client's socket.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.g.Conn == nil {
		return nil
	}
	err := c.g.Conn.Close()
	c.g.Conn = nil
	return err
}

// Version returns the version the client speaks.
func (c *Client) Version() Version { return c.settings.Version }

// Level returns the SNMPv3 security level the client's user authenticates
// at, or "" for a community.
func (c *Client) Level() wire.SNMPSecurityLevel { return c.cred.Level() }

// Authenticated reports whether the agent's answers are authenticated
// (Authenticated of the client's credential).
func (c *Client) Authenticated() bool { return Authenticated(c.cred) }

// EngineID returns the agent's authoritative SNMPv3 engine ID as lowercase
// hex, learned by the client's first request, or "" before one or for a
// community.
func (c *Client) EngineID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	usm, ok := c.g.SecurityParameters.(*gosnmp.UsmSecurityParameters)
	if !ok || usm.AuthoritativeEngineID == "" {
		return ""
	}
	return hex.EncodeToString([]byte(usm.AuthoritativeEngineID))
}

// Get reads oids (numeric, at most MaxOIDsPerRequest) in one GET. An OID the
// agent holds nothing at comes back as a binding of an Absent type in SNMPv2c
// and SNMPv3, and as an *AgentError (noSuchName) in SNMPv1.
func (c *Client) Get(ctx context.Context, oids []string) ([]Varbind, error) {
	if err := checkOIDs(oids); err != nil {
		return nil, err
	}
	return c.do(ctx, "GET", func(g *gosnmp.GoSNMP) (*gosnmp.SnmpPacket, error) { return g.Get(dotted(oids)) })
}

// GetNext reads the binding after each of oids in one GETNEXT.
func (c *Client) GetNext(ctx context.Context, oids []string) ([]Varbind, error) {
	if err := checkOIDs(oids); err != nil {
		return nil, err
	}
	return c.do(ctx, "GETNEXT", func(g *gosnmp.GoSNMP) (*gosnmp.SnmpPacket, error) { return g.GetNext(dotted(oids)) })
}

// GetBulk reads up to maxRepetitions bindings after each of oids in one
// GETBULK, the first nonRepeaters of them once. SNMPv1 has none, and a
// device whose record turns it off (NoBulkProperty) is never sent one.
func (c *Client) GetBulk(ctx context.Context, oids []string, nonRepeaters, maxRepetitions int) ([]Varbind, error) {
	if err := checkOIDs(oids); err != nil {
		return nil, err
	}
	switch {
	case c.settings.Version == Version1:
		return nil, errors.New("SNMPv1 has no GETBULK")
	case c.settings.NoBulk:
		return nil, fmt.Errorf("the device's record turns GETBULK off (%s)", NoBulkProperty)
	case nonRepeaters < 0 || nonRepeaters > len(oids) || maxRepetitions < 1 || maxRepetitions > MaxMaxRepetitions:
		return nil, fmt.Errorf("a GETBULK takes from 0 to %d non-repeaters and from 1 to %d repetitions", len(oids), MaxMaxRepetitions)
	}
	return c.do(ctx, "GETBULK", func(g *gosnmp.GoSNMP) (*gosnmp.SnmpPacket, error) {
		return g.GetBulk(dotted(oids), uint8(nonRepeaters), uint32(maxRepetitions)) // #nosec G115 -- both bounded above
	})
}

// Set writes vars (built by Value) in one SET, which an agent applies whole
// or not at all (RFC 3416 section 4.2.5), and returns the bindings it
// answered with.
func (c *Client) Set(ctx context.Context, vars []Varbind) ([]Varbind, error) {
	if len(vars) == 0 || len(vars) > MaxOIDsPerRequest {
		return nil, fmt.Errorf("a SET writes from 1 to %d values", MaxOIDsPerRequest)
	}
	pdus := make([]gosnmp.SnmpPDU, 0, len(vars))
	for _, v := range vars {
		p, err := v.pdu()
		if err != nil {
			return nil, fmt.Errorf("the value for %s: %w", v.OID, err)
		}
		pdus = append(pdus, p)
	}
	return c.do(ctx, "SET", func(g *gosnmp.GoSNMP) (*gosnmp.SnmpPacket, error) { return g.Set(pdus) })
}

// do sends one request through send, after the device's minimum interval,
// under ctx, and converts its answer: an error status becomes an
// *AgentError, a USM refusal a *RefusedError, and every error is masked of
// the credential.
func (c *Client) do(ctx context.Context, what string, send func(*gosnmp.GoSNMP) (*gosnmp.SnmpPacket, error)) ([]Varbind, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.g.Conn == nil {
		return nil, errors.New("the client is closed")
	}
	if wait := c.settings.MinInterval - time.Since(c.last); c.settings.MinInterval > 0 && wait > 0 {
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.g.Context = ctx
	c.last = time.Now()
	packet, err := send(c.g)
	if err != nil {
		return nil, c.requestError(what, err)
	}
	vars := make([]Varbind, 0, len(packet.Variables))
	for _, pdu := range packet.Variables {
		v, err := fromPDU(pdu)
		if err != nil {
			return nil, fmt.Errorf("%s: the agent answered %s", what, err)
		}
		vars = append(vars, v)
	}
	if packet.Error != gosnmp.NoError {
		e := &AgentError{Status: statusName(packet.Error), Index: int(packet.ErrorIndex)}
		if i := int(packet.ErrorIndex) - 1; i >= 0 && i < len(vars) {
			e.OID = vars[i].OID
		}
		return nil, fmt.Errorf("%s: %w", what, e)
	}
	return vars, nil
}

// sleepCtx waits for d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// checkOIDs refuses an empty list, too many OIDs, and one ParseOID would not
// return as it is.
func checkOIDs(oids []string) error {
	if len(oids) == 0 || len(oids) > MaxOIDsPerRequest {
		return fmt.Errorf("a request names from 1 to %d OIDs", MaxOIDsPerRequest)
	}
	for _, oid := range oids {
		canon, err := ParseOID(oid)
		if err != nil {
			return err
		}
		if canon != oid {
			return fmt.Errorf("OID %q is not numeric without a leading dot; read it through ParseOID first", oid)
		}
	}
	return nil
}

// dotted is oids with gosnmp's leading dot.
func dotted(oids []string) []string {
	out := make([]string, len(oids))
	for i, oid := range oids {
		out[i] = "." + oid
	}
	return out
}

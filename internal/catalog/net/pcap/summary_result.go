// Package pcap: how net.pcap.summary's result holds what pcapsum read, and
// how its management parameter is read.
package pcap

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// flows is the first limit of fs as a result holds them. A credential's
// header is named; its value was never kept.
func flows(fs []pcapsum.Flow, limit int) []any {
	fs = fs[:min(len(fs), limit)]
	out := make([]any, len(fs))
	for i, f := range fs {
		creds := make([]any, len(f.CleartextCredentials))
		for j, c := range f.CleartextCredentials {
			creds[j] = c
		}
		m := map[string]any{
			"client": f.Client, "server": f.Server, "protocol": f.Protocol, "packets": f.Packets,
			"bytes": f.Bytes, "dscp": marks(f.DSCP), "duration_seconds": f.DurationSeconds,
			"encrypted": f.Encrypted, "key_exchange": f.KeyExchange, "cleartext": f.Cleartext,
			"cleartext_management": f.CleartextManagement, "cleartext_credentials": creds,
			"cleartext_in_encrypted": f.CleartextInEncrypted, "headers_unread": f.HeadersUnread,
		}
		if f.Management != "" {
			m["management"] = f.Management
		}
		putTimes(m, f.First, f.Last)
		out[i] = m
	}
	return out
}

// marks is a DSCP table as a result holds it.
func marks(ds []pcapsum.DSCPCount) []any {
	out := make([]any, len(ds))
	for i, d := range ds {
		out[i] = map[string]any{"dscp": d.DSCP, "name": d.Name, "class": d.Class, "packets": d.Packets, "bytes": d.Bytes}
	}
	return out
}

// putTimes sets first and last in m, RFC 3339 in UTC, when the capture held
// a time; a capture of simple packet blocks holds none.
func putTimes(m map[string]any, first, last time.Time) {
	if first.IsZero() {
		return
	}
	m["first"] = first.UTC().Format(time.RFC3339Nano)
	m["last"] = last.UTC().Format(time.RFC3339Nano)
}

// optionsParam reads management: each an address, every port on it a
// management endpoint, or an address and port.
func optionsParam(params map[string]any) (pcapsum.Options, error) {
	list, _, err := sdk.StringSlice(params, paramManagement)
	if err != nil {
		return pcapsum.Options{}, err
	}
	var opts pcapsum.Options
	for _, v := range list {
		if ap, err := netip.ParseAddrPort(v); err == nil {
			opts.Management = append(opts.Management, ap)
			continue
		}
		addr, err := netip.ParseAddr(v)
		if err != nil {
			return pcapsum.Options{}, fmt.Errorf("%s: %q is neither an address nor an address and port", paramManagement, v)
		}
		opts.Management = append(opts.Management, netip.AddrPortFrom(addr, 0))
	}
	return opts, nil
}

// counters is a protocol table as a result holds it.
func counters(m map[string]pcapsum.Counter) map[string]any {
	out := make(map[string]any, len(m))
	for k, c := range m {
		out[k] = map[string]any{"packets": c.Packets, "bytes": c.Bytes}
	}
	return out
}

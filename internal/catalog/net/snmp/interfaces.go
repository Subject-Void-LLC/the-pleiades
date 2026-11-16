// Package snmp: the interface table net.snmp.facts reads, walked column by
// column so a table of many interfaces costs one walk per column, and joined
// by ifIndex.
package snmp

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// ifColumns are the columns read, each with the key its value is recorded
// under. ifIndex comes first: its walk decides which rows there are.
var ifColumns = []struct{ oid, key string }{
	{"1.3.6.1.2.1.2.2.1.1", "index"},
	{"1.3.6.1.2.1.2.2.1.2", "descr"},
	{"1.3.6.1.2.1.2.2.1.3", "type"},
	{"1.3.6.1.2.1.2.2.1.4", "mtu"},
	{"1.3.6.1.2.1.2.2.1.5", "speed"},
	{"1.3.6.1.2.1.2.2.1.6", "phys_address"},
	{"1.3.6.1.2.1.2.2.1.7", "admin_status"},
	{"1.3.6.1.2.1.2.2.1.8", "oper_status"},
	{"1.3.6.1.2.1.31.1.1.1.1", "name"},
	{"1.3.6.1.2.1.31.1.1.1.15", "high_speed"},
	{"1.3.6.1.2.1.31.1.1.1.18", "alias"},
}

// statusNames are RFC 2863's ifAdminStatus and ifOperStatus values.
var statusNames = map[string]string{"1": "up", "2": "down", "3": "testing", "4": "unknown", "5": "dormant", "6": "notPresent", "7": "lowerLayerDown"}

// interfaces walks ifColumns, up to limit rows, and returns each row as a
// fact holds it, in ifIndex order, and whether the table had more. A failed
// walk of ifIndex is an error; a failed walk of another column leaves that
// column out, since an agent need not implement ifXTable.
func (s *session) interfaces(ctx context.Context, limit int) ([]map[string]any, bool, error) {
	index, err := s.client.Walk(ctx, ifColumns[0].oid, limit)
	if err != nil {
		return nil, false, err
	}
	rows := map[string]map[string]any{}
	order := make([]string, 0, len(index.Bindings))
	for _, v := range index.Bindings {
		instance := strings.TrimPrefix(v.OID, ifColumns[0].oid+".")
		if _, seen := rows[instance]; seen || v.Type != snmp.TypeInteger {
			continue
		}
		n, _ := strconv.Atoi(v.Text)
		rows[instance] = map[string]any{"index": n}
		order = append(order, instance)
	}
	for _, col := range ifColumns[1:] {
		res, err := s.client.Walk(ctx, col.oid, limit)
		if err != nil {
			continue
		}
		for _, v := range res.Bindings {
			row, ok := rows[strings.TrimPrefix(v.OID, col.oid+".")]
			if !ok || v.Type.Absent() {
				continue
			}
			row[col.key] = s.column(col.key, v)
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, instance := range order {
		row := rows[instance]
		// ifSpeed stops at its 32-bit ceiling; ifHighSpeed, in millions,
		// says the rest (RFC 2863).
		if hs, ok := row["high_speed"].(int64); ok {
			if speed, _ := row["speed"].(int64); speed == math.MaxUint32 {
				row["speed"] = hs * 1_000_000
			}
			delete(row, "high_speed")
		}
		out = append(out, row)
	}
	return out, index.Truncated, nil
}

// column is one interface value as a fact holds it: a status by its name, a
// number as a number, an address as hex, text masked.
func (s *session) column(key string, v snmp.Varbind) any {
	switch key {
	case "admin_status", "oper_status":
		if name, ok := statusNames[v.Text]; ok {
			return name
		}
		return v.Text
	case "phys_address":
		return fmt.Sprintf("%x", v.Bytes)
	}
	return s.value(v)
}

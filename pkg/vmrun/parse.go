// Readers for what vmrun and vmcli print. Each is a pure function over text,
// held to captures from a real Fusion (testdata/), and refuses output that is
// not the shape it knows rather than guessing at it.
package vmrun

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// parseCounted reads vmrun's counted lists: a header line "Total X: N" and
// then N lines. It refuses a count that does not match the lines that follow,
// since a truncated answer must not read as a shorter list.
func parseCounted(s, header string) ([]string, error) {
	lines := nonEmpty(s)
	// The product can print diagnostics before its answer (a Fusion that has
	// never been opened says it cannot find its product location), so the
	// header is looked for, not assumed to be first.
	for len(lines) > 0 && !strings.HasPrefix(lines[0], header) {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("the output holds no %q line", header)
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[0], header)))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("%q does not end in a count", lines[0])
	}
	rest := lines[1:]
	if len(rest) != n {
		return nil, fmt.Errorf("%q promised %d line(s) and %d followed", lines[0], n, len(rest))
	}
	return rest, nil
}

// parseKeyValues reads vmcli's "key: value" listings. A key or value vmcli
// quoted ('config.version': 8, nvram: 'sample.nvram') is unquoted. A quoted
// key is read to its closing quote first, so a vmx key holding a colon of its
// own ('nvme0:0.fileName': disk.vmdk) is not cut there. A line with no colon
// is ignored; the first occurrence of a key wins.
func parseKeyValues(s string) map[string]string {
	out := map[string]string{}
	for _, line := range nonEmpty(s) {
		k, v, ok := cutKey(line)
		if !ok {
			continue
		}
		k = unquote(strings.TrimSpace(k))
		if _, seen := out[k]; !seen && k != "" {
			out[k] = unquote(strings.TrimSpace(v))
		}
	}
	return out
}

// cutKey splits one "key: value" line at the colon that ends the key: the
// first colon after a quoted key's closing quote, or the first colon at all.
func cutKey(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "'") {
		if end := strings.Index(trimmed[1:], "'"); end >= 0 {
			key, rest := trimmed[:end+2], trimmed[end+2:]
			if v, ok := strings.CutPrefix(strings.TrimLeft(rest, " \t"), ":"); ok {
				return key, v, true
			}
		}
	}
	return strings.Cut(line, ":")
}

// unquote removes one pair of single quotes around s.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// parseNetworks reads listHostNetworks: "Total host networks: N", a header
// row, then one row per network (INDEX NAME TYPE DHCP SUBNET MASK).
func parseNetworks(s string) ([]Network, error) {
	lines := nonEmpty(s)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "Total host networks:") {
		return nil, fmt.Errorf("expected the host network list, got %s", farside.Output(s))
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[0], "Total host networks:")))
	if err != nil || n < 0 || len(lines) != n+2 {
		return nil, fmt.Errorf("%q does not match the %d row(s) that follow its header", lines[0], len(lines)-2)
	}
	var out []Network
	for _, row := range lines[2:] {
		f := strings.Fields(row)
		if len(f) != 6 {
			return nil, fmt.Errorf("network row %q has %d fields, want 6", row, len(f))
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("network row %q has no index", row)
		}
		out = append(out, Network{Index: idx, Name: f[1], Type: f[2], DHCP: f[3] == "true", Subnet: f[4], Mask: f[5]})
	}
	return out, nil
}

// parseCreated reads the path vmcli says it made: a line "VMX file is created,
// filepath: <path>". It returns "" when no such line is there.
func parseCreated(s string) string {
	const marker = "VMX file is created, filepath:"
	for _, line := range nonEmpty(s) {
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
	}
	return ""
}

// nonEmpty is s's lines, trimmed, without the empty ones.
func nonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

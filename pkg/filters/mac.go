package filters

import (
	"fmt"
	"net"
	"strings"
)

// MACToCiscoFormat normalizes a MAC address to Cisco dotted-quad
// notation, lowercase (e.g. "00:00:5e:00:53:01" -> "0000.5e00.5301").
// Accepts colon, dash, or dotted input (net.ParseMAC's own three
// notations); returns "" for a malformed, over-length, or non-EUI-48
// (not exactly 6 bytes) input. EUI-64 and the 20-octet InfiniBand form
// net.ParseMAC also accepts have no Cisco dotted-quad convention this
// function could produce, so they are refused rather than guessed at.
func MACToCiscoFormat(mac string) string {
	hw, ok := parseEUI48(mac)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%02x%02x.%02x%02x.%02x%02x", hw[0], hw[1], hw[2], hw[3], hw[4], hw[5])
}

// MACToColonFormat normalizes a MAC address to colon-separated
// notation, lowercase (e.g. "0000.5e00.5301" -> "00:00:5e:00:53:01").
// Accepts colon, dash, or dotted input. Returns "" for a malformed or
// over-length input; unlike MACToCiscoFormat and MACToWindowsFormat,
// this one is not restricted to EUI-48: net.HardwareAddr.String()
// already renders any length net.ParseMAC accepts in colon-separated
// form, and colon notation has no dotted-quad-style length limitation
// to honor.
func MACToColonFormat(mac string) string {
	if len(mac) > MaxInputBytes {
		return ""
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return ""
	}
	return hw.String()
}

// MACToWindowsFormat normalizes a MAC address to Windows dash-separated
// notation, uppercase (e.g. "00:00:5e:00:53:01" -> "00-00-5E-00-53-01").
// Accepts colon, dash, or dotted input; returns "" for a malformed or
// over-length input.
func MACToWindowsFormat(mac string) string {
	if len(mac) > MaxInputBytes {
		return ""
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(hw.String(), ":", "-"))
}

// MACOUI extracts a MAC address's OUI (organizationally unique
// identifier), its first three octets, colon-separated and uppercase
// (e.g. "00:00:5e:00:53:01" -> "00:00:5E"). Accepts colon, dash, or
// dotted input; returns "" for a malformed, over-length, or
// shorter-than-3-byte input (the 20-octet InfiniBand form still has a
// well-defined first three bytes, so this one is not restricted to
// EUI-48 the way the two full-address normalizers above are).
func MACOUI(mac string) string {
	if len(mac) > MaxInputBytes {
		return ""
	}
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) < 3 {
		return ""
	}
	return strings.ToUpper(fmt.Sprintf("%02x:%02x:%02x", hw[0], hw[1], hw[2]))
}

// parseEUI48 parses s as a MAC address and reports whether it succeeded
// and is exactly 6 bytes (EUI-48), the only length Cisco dotted-quad
// notation (three groups of two bytes each) is defined for. Windows
// dash notation carries no such grouping constraint, so
// MACToWindowsFormat does not use this helper.
func parseEUI48(s string) (net.HardwareAddr, bool) {
	if len(s) > MaxInputBytes {
		return nil, false
	}
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return nil, false
	}
	return hw, true
}

package console_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/console"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// build hydrates a console_device through the registered constructor,
// the same call record.Types hands internal/inventory's ItemFactory.
func build(t *testing.T, props map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	item, err := console.NewDevice(record.Record{
		ID:         "c1",
		Name:       "c1",
		Type:       "console_device",
		Properties: props,
	})
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	return item
}

// TestNewDevice_ConstructsFromRecord proves the constructor hydrates a
// real Record without error and that basic identity fields round-trip.
func TestNewDevice_ConstructsFromRecord(t *testing.T) {
	item := build(t, nil)
	if got := string(item.ID()); got != "c1" {
		t.Errorf("ID() = %q, want %q", got, "c1")
	}
	if got := item.Name(); got != "c1" {
		t.Errorf("Name() = %q, want %q", got, "c1")
	}
}

// TestNewDevice_RegisteredUnderItsTypeKey proves the constructor is
// reachable through the shared registry rather than only by importing
// this package directly, which is what internal/inventory's ItemFactory
// actually goes through.
func TestNewDevice_RegisteredUnderItsTypeKey(t *testing.T) {
	ctor, ok := record.LookupType("console_device")
	if !ok {
		t.Fatal("console_device is not registered in record.Types")
	}
	item, err := ctor(record.Record{ID: "c1", Name: "c1", Type: "console_device"})
	if err != nil {
		t.Fatalf("registered constructor: %v", err)
	}
	if item == nil {
		t.Fatal("registered constructor returned a nil item")
	}
}

// TestNewDevice_DeclaresOnlyTheConfiguredPaths is the central proof of
// this type's design: each of the four console capabilities is declared
// only when the record carries that path's locator, so
// validate.CapabilityRule rejects a serial_exec task aimed at a
// Telnet-only device at plan time instead of letting it dial an empty
// device name at run time.
//
// HasCapability, not Capabilities(), is what every case asserts: it is
// the exact call engine.checkMethodCapabilities and
// validate.CapabilityRule make, and it ANDs the declaration against the
// structural assertion, so a passing case proves both halves at once.
func TestNewDevice_DeclaresOnlyTheConfiguredPaths(t *testing.T) {
	tests := []struct {
		name  string
		props map[string]inventory.PropertyValue
		want  []capability.Name
	}{
		{
			name:  "nothing configured declares nothing",
			props: nil,
			want:  nil,
		},
		{
			name:  "a cabled serial line",
			props: map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0"},
			want:  []capability.Name{capability.NameSerial},
		},
		{
			name:  "a Windows COM port is just as valid",
			props: map[string]inventory.PropertyValue{"serial_device": "COM3"},
			want:  []capability.Name{capability.NameSerial},
		},
		{
			name: "a raw TCP console server line",
			props: map[string]inventory.PropertyValue{
				"raw_passthrough_host": "ts1.example.net",
				"raw_passthrough_port": 2003,
			},
			want: []capability.Name{capability.NameRawPassthrough},
		},
		{
			name: "an RFC 2217 console server line",
			props: map[string]inventory.PropertyValue{
				"rfc2217_host": "ts1.example.net",
				"rfc2217_port": 3003,
			},
			want: []capability.Name{capability.NameRFC2217},
		},
		{
			name:  "bare Telnet",
			props: map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40"},
			want:  []capability.Name{capability.NameTelnet},
		},
		{
			name: "a device on two console servers declares both",
			props: map[string]inventory.PropertyValue{
				"raw_passthrough_host": "ts1.example.net",
				"raw_passthrough_port": 2003,
				"rfc2217_host":         "ts2.example.net",
				"rfc2217_port":         3003,
			},
			want: []capability.Name{capability.NameRawPassthrough, capability.NameRFC2217},
		},
		{
			name:  "line settings alone name nothing to open",
			props: map[string]inventory.PropertyValue{"serial_baud": 115200},
			want:  nil,
		},
		{
			name:  "an empty locator is not a locator",
			props: map[string]inventory.PropertyValue{"serial_device": ""},
			want:  nil,
		},
	}

	all := []capability.Name{
		capability.NameSerial,
		capability.NameRawPassthrough,
		capability.NameRFC2217,
		capability.NameTelnet,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := build(t, tt.props)
			wanted := make(map[capability.Name]bool, len(tt.want))
			for _, name := range tt.want {
				wanted[name] = true
			}
			for _, name := range all {
				if got := item.HasCapability(name); got != wanted[name] {
					t.Errorf("HasCapability(%s) = %v, want %v", name, got, wanted[name])
				}
			}
		})
	}
}

// TestNewDevice_UnionsClassificationCapabilities proves Phase 32's
// granularity decision still holds here: classification-derived
// capabilities are unioned into the declared set on top of whatever the
// record's own properties earned, and HasCapability for one this type
// does not structurally implement correctly stays false, so neither half
// is trusted alone.
func TestNewDevice_UnionsClassificationCapabilities(t *testing.T) {
	item, err := console.NewDevice(record.Record{
		ID:           "c1",
		Name:         "c1",
		Type:         "console_device",
		Properties:   map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40"},
		Capabilities: []capability.Name{capability.NameApt},
	})
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}

	if !item.HasCapability(capability.NameTelnet) {
		t.Error("the record's own telnet_host should still declare TelnetCapable")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("AptCapable is declared but not structurally implemented, so HasCapability must stay false")
	}

	var declared bool
	for _, name := range item.Capabilities() {
		if name == capability.NameApt {
			declared = true
		}
	}
	if !declared {
		t.Error("classification-derived AptCapable should still reach the declared set at the data layer")
	}
}

// TestDevice_SerialAccessors proves the SerialCapable half returns what
// the record configured, and that an unconfigured line takes the 9600
// 8-N-1 console default every vendor ships.
func TestDevice_SerialAccessors(t *testing.T) {
	defaulted := build(t, map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0"})
	dev, ok := defaulted.(capability.SerialCapable)
	if !ok {
		t.Fatal("a console_device must structurally satisfy capability.SerialCapable")
	}
	if got := dev.SerialDevice(); got != serialline.Device("/dev/ttyUSB0") {
		t.Errorf("SerialDevice() = %q, want /dev/ttyUSB0", got)
	}
	want := serialline.Config{BaudRate: 9600, DataBits: 8, Parity: serialline.ParityNone, StopBits: serialline.StopBitsOne}
	if got := dev.SerialLine(); got != want {
		t.Errorf("SerialLine() = %+v, want %+v", got, want)
	}

	overridden := build(t, map[string]inventory.PropertyValue{
		"serial_device":    "/dev/ttyS0",
		"serial_baud":      115200,
		"serial_data_bits": 7,
		"serial_parity":    "even",
		"serial_stop_bits": "2",
	}).(capability.SerialCapable)
	want = serialline.Config{BaudRate: 115200, DataBits: 7, Parity: serialline.ParityEven, StopBits: serialline.StopBitsTwo}
	if got := overridden.SerialLine(); got != want {
		t.Errorf("SerialLine() = %+v, want %+v", got, want)
	}
}

// TestDevice_ConsoleServerAccessors proves the two console-server halves
// read their own independent property sets, so a local adapter's baud
// rate is never silently applied to a remote line.
func TestDevice_ConsoleServerAccessors(t *testing.T) {
	item := build(t, map[string]inventory.PropertyValue{
		"serial_device":        "/dev/ttyUSB0",
		"serial_baud":          9600,
		"raw_passthrough_host": "ts1.example.net",
		"raw_passthrough_port": 2003,
		"rfc2217_host":         "ts2.example.net",
		"rfc2217_port":         3003,
		"rfc2217_baud":         115200,
	})

	raw, ok := item.(capability.RawPassthroughCapable)
	if !ok {
		t.Fatal("a console_device must structurally satisfy capability.RawPassthroughCapable")
	}
	if got := raw.RawPassthroughHost(); got != "ts1.example.net" {
		t.Errorf("RawPassthroughHost() = %q, want ts1.example.net", got)
	}
	if got := raw.RawPassthroughPort(); got != 2003 {
		t.Errorf("RawPassthroughPort() = %d, want 2003", got)
	}

	rfc, ok := item.(capability.RFC2217Capable)
	if !ok {
		t.Fatal("a console_device must structurally satisfy capability.RFC2217Capable")
	}
	if got := rfc.RFC2217Host(); got != "ts2.example.net" {
		t.Errorf("RFC2217Host() = %q, want ts2.example.net", got)
	}
	if got := rfc.RFC2217Port(); got != 3003 {
		t.Errorf("RFC2217Port() = %d, want 3003", got)
	}
	if got := rfc.RFC2217Line().BaudRate; got != 115200 {
		t.Errorf("RFC2217Line().BaudRate = %d, want 115200", got)
	}
	if got := item.(capability.SerialCapable).SerialLine().BaudRate; got != 9600 {
		t.Errorf("SerialLine().BaudRate = %d, want 9600: the two lines are independent", got)
	}
}

// TestDevice_TelnetAccessors proves the well-known port 23 default, the
// one console path that honestly has one.
func TestDevice_TelnetAccessors(t *testing.T) {
	dev, ok := build(t, map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40"}).(capability.TelnetCapable)
	if !ok {
		t.Fatal("a console_device must structurally satisfy capability.TelnetCapable")
	}
	if got := dev.TelnetHost(); got != "10.20.30.40" {
		t.Errorf("TelnetHost() = %q, want 10.20.30.40", got)
	}
	if got := dev.TelnetPort(); got != 23 {
		t.Errorf("TelnetPort() = %d, want the well-known 23", got)
	}

	overridden := build(t, map[string]inventory.PropertyValue{
		"telnet_host": "10.20.30.40",
		"telnet_port": 2323,
	}).(capability.TelnetCapable)
	if got := overridden.TelnetPort(); got != 2323 {
		t.Errorf("TelnetPort() = %d, want 2323", got)
	}
}

// TestNewDevice_RefusesUnusableProperties proves the hydration rule
// .SPECIFICATION/IMPLEMENTATION.md states for this device family: a
// property that is present but unusable is an error at construction, not
// a silent default. A wrong parity or baud rate does not fail a serial
// line, it corrupts every byte crossing it, so defaulting past a typo
// would be indistinguishable from a line that works.
func TestNewDevice_RefusesUnusableProperties(t *testing.T) {
	tests := []struct {
		name      string
		props     map[string]inventory.PropertyValue
		wantInErr string
	}{
		{
			name:      "misspelled parity",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_parity": "evne"},
			wantInErr: "serial_parity",
		},
		{
			name:      "unknown stop bits",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_stop_bits": "3"},
			wantInErr: "serial_stop_bits",
		},
		{
			name:      "baud rate that is not a number",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_baud": "fast"},
			wantInErr: "serial_baud",
		},
		{
			name:      "non-positive baud rate",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_baud": 0},
			wantInErr: "serial_baud",
		},
		{
			name:      "data bits outside what the line library accepts",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_data_bits": 16},
			wantInErr: "serial_data_bits",
		},
		{
			name:      "device name that is not a string",
			props:     map[string]inventory.PropertyValue{"serial_device": 3},
			wantInErr: "serial_device",
		},
		{
			name:      "console server host with no port",
			props:     map[string]inventory.PropertyValue{"raw_passthrough_host": "ts1.example.net"},
			wantInErr: "raw_passthrough_port",
		},
		{
			name:      "console server port with no host",
			props:     map[string]inventory.PropertyValue{"raw_passthrough_port": 2003},
			wantInErr: "raw_passthrough_host",
		},
		{
			name:      "RFC 2217 host with no port",
			props:     map[string]inventory.PropertyValue{"rfc2217_host": "ts1.example.net"},
			wantInErr: "rfc2217_port",
		},
		{
			name:      "port outside the legal range",
			props:     map[string]inventory.PropertyValue{"raw_passthrough_host": "ts1.example.net", "raw_passthrough_port": 70000},
			wantInErr: "raw_passthrough_port",
		},
		{
			name:      "RFC 2217 parity typo",
			props:     map[string]inventory.PropertyValue{"rfc2217_host": "ts1.example.net", "rfc2217_port": 3003, "rfc2217_parity": "nope"},
			wantInErr: "rfc2217_parity",
		},
		{
			name:      "RFC 2217 stop bits typo",
			props:     map[string]inventory.PropertyValue{"rfc2217_host": "ts1.example.net", "rfc2217_port": 3003, "rfc2217_stop_bits": "4"},
			wantInErr: "rfc2217_stop_bits",
		},
		{
			name:      "RFC 2217 data bits out of range",
			props:     map[string]inventory.PropertyValue{"rfc2217_host": "ts1.example.net", "rfc2217_port": 3003, "rfc2217_data_bits": 2},
			wantInErr: "rfc2217_data_bits",
		},
		{
			name:      "RFC 2217 baud rate that is not a number",
			props:     map[string]inventory.PropertyValue{"rfc2217_host": "ts1.example.net", "rfc2217_port": 3003, "rfc2217_baud": true},
			wantInErr: "rfc2217_baud",
		},
		{
			name:      "telnet host that is not a string",
			props:     map[string]inventory.PropertyValue{"telnet_host": 10},
			wantInErr: "telnet_host",
		},
		{
			name:      "telnet port outside the legal range",
			props:     map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40", "telnet_port": -1},
			wantInErr: "telnet_port",
		},
		{
			name:      "console server host that is not a string",
			props:     map[string]inventory.PropertyValue{"raw_passthrough_host": 5, "raw_passthrough_port": 2003},
			wantInErr: "raw_passthrough_host",
		},
		{
			name:      "console server port written as a string",
			props:     map[string]inventory.PropertyValue{"raw_passthrough_host": "ts1.example.net", "raw_passthrough_port": "2003"},
			wantInErr: "raw_passthrough_port",
		},
		{
			name:      "data bits written as a string",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_data_bits": "8"},
			wantInErr: "serial_data_bits",
		},
		{
			name:      "parity that is not a string",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_parity": 1},
			wantInErr: "serial_parity",
		},
		{
			name:      "stop bits that are not a string",
			props:     map[string]inventory.PropertyValue{"serial_device": "/dev/ttyUSB0", "serial_stop_bits": 2},
			wantInErr: "serial_stop_bits",
		},
		{
			name:      "telnet port written as a string",
			props:     map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40", "telnet_port": "23"},
			wantInErr: "telnet_port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := console.NewDevice(record.Record{
				ID:         "c1",
				Name:       "c1",
				Type:       "console_device",
				Properties: tt.props,
			})
			if err == nil {
				t.Fatalf("NewDevice returned a usable device for %v, want a refusal", tt.props)
			}
			if item != nil {
				t.Error("a refused hydration must return a nil item, never a half-built one")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not name the offending property %q", err, tt.wantInErr)
			}
		})
	}
}

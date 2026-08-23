// This file holds console_device's hydration half: the property names an
// operator writes, and the parsing that turns them into a declared
// capability set plus two typed line configurations.
//
// It is separate from device.go so the accessors there stay a flat,
// readable list of what each capability interface asks for, which is
// AGENTS.md's file-size convention applied at the seam that already
// exists rather than at an arbitrary line count.
package console

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// The property keys a console_device record carries. Each one is named
// after the accessor that reads it, the same convention container.Host's
// "docker_endpoint" follows, so an operator reading an error message
// naming a capability can find the property without a lookup table.
const (
	propSerialDevice   = "serial_device"
	propSerialBaud     = "serial_baud"
	propSerialDataBits = "serial_data_bits"
	propSerialParity   = "serial_parity"
	propSerialStopBits = "serial_stop_bits"

	propRawHost = "raw_passthrough_host"
	propRawPort = "raw_passthrough_port"

	propRFC2217Host     = "rfc2217_host"
	propRFC2217Port     = "rfc2217_port"
	propRFC2217Baud     = "rfc2217_baud"
	propRFC2217DataBits = "rfc2217_data_bits"
	propRFC2217Parity   = "rfc2217_parity"
	propRFC2217StopBits = "rfc2217_stop_bits"

	propTelnetHost = "telnet_host"
	propTelnetPort = "telnet_port"
)

// The line defaults, which are 9600 8-N-1: the console setting every
// mainstream vendor ships its gear with and every console cable's
// documentation prints. This is a real convention, in the same sense
// port 22 is for SSH, not a guess picked to avoid a zero value.
const (
	defaultBaudRate   serialline.BaudRate = 9600
	defaultDataBits                       = 8
	defaultTelnetPort                     = 23
)

// The inclusive bounds a data-bit count and a TCP port must fall inside.
// go.bug.st/serial, the library pkg/serialexec drives a real port with,
// accepts 5 through 8 data bits and nothing else; a value outside that
// reaches the library as an argument it will reject far from the record
// that supplied it.
const (
	minDataBits = 5
	maxDataBits = 8
	minPort     = 1
	maxPort     = 65535
)

// consolePaths is hydratePaths' result: which capabilities a record's
// properties entitle it to declare, plus the two parsed line
// configurations NewDevice stores on the Device.
type consolePaths struct {
	declared    []capability.Name
	serialLine  serialline.Config
	rfc2217Line serialline.Config
}

// hydratePaths reads every console property off props, refuses the ones
// that are present but unusable, and reports which of the four console
// capabilities the record has actually configured a path for.
//
// A capability is declared on the strength of its LOCATOR alone (the
// device name, or the host), never its line settings: a record naming a
// serial device with no baud rate is a perfectly ordinary console entry
// taking the 9600 8-N-1 default, while a record with a baud rate and no
// device names nothing to open.
func hydratePaths(props inventory.Properties) (consolePaths, error) {
	paths := consolePaths{
		serialLine:  serialline.Config{BaudRate: defaultBaudRate, DataBits: defaultDataBits},
		rfc2217Line: serialline.Config{BaudRate: defaultBaudRate, DataBits: defaultDataBits},
	}

	device, hasDevice, err := lookupString(props, propSerialDevice)
	if err != nil {
		return consolePaths{}, err
	}
	if hasDevice && device != "" {
		paths.declared = append(paths.declared, capability.NameSerial)
	}
	paths.serialLine, err = parseLine(props, paths.serialLine, propSerialBaud, propSerialDataBits, propSerialParity, propSerialStopBits)
	if err != nil {
		return consolePaths{}, err
	}

	hasRaw, err := hasConsoleServerLine(props, propRawHost, propRawPort)
	if err != nil {
		return consolePaths{}, err
	}
	if hasRaw {
		paths.declared = append(paths.declared, capability.NameRawPassthrough)
	}

	hasRFC2217, err := hasConsoleServerLine(props, propRFC2217Host, propRFC2217Port)
	if err != nil {
		return consolePaths{}, err
	}
	if hasRFC2217 {
		paths.declared = append(paths.declared, capability.NameRFC2217)
	}
	paths.rfc2217Line, err = parseLine(props, paths.rfc2217Line, propRFC2217Baud, propRFC2217DataBits, propRFC2217Parity, propRFC2217StopBits)
	if err != nil {
		return consolePaths{}, err
	}

	telnetHost, hasTelnet, err := lookupString(props, propTelnetHost)
	if err != nil {
		return consolePaths{}, err
	}
	if _, err := lookupPort(props, propTelnetPort); err != nil {
		return consolePaths{}, err
	}
	if hasTelnet && telnetHost != "" {
		paths.declared = append(paths.declared, capability.NameTelnet)
	}

	return paths, nil
}

// hasConsoleServerLine reports whether props names a complete console
// server line under hostKey/portKey, and refuses a half-configured one.
//
// A host with no port is an error rather than a silently undeclared
// capability. The operator plainly meant to reach this device through
// that console server, and the two failure modes available are "tell
// them the port is missing" or "quietly behave as though they never
// configured it at all," where the second ends with them debugging a
// capability refusal whose cause is three files away.
func hasConsoleServerLine(props inventory.Properties, hostKey, portKey string) (bool, error) {
	host, hasHost, err := lookupString(props, hostKey)
	if err != nil {
		return false, err
	}
	port, err := lookupPort(props, portKey)
	if err != nil {
		return false, err
	}

	switch {
	case (!hasHost || host == "") && port == 0:
		return false, nil
	case host == "":
		return false, fmt.Errorf("property %q is set but %q is not, so there is no console server to reach it on", portKey, hostKey)
	case port == 0:
		return false, fmt.Errorf("property %q is set but %q is not, and a console server's per-line port has no well-known default to fall back to", hostKey, portKey)
	default:
		return true, nil
	}
}

// parseLine returns base with every line setting the record overrides
// applied, refusing any value that does not parse.
func parseLine(props inventory.Properties, base serialline.Config, baudKey, dataBitsKey, parityKey, stopBitsKey string) (serialline.Config, error) {
	cfg := base

	baud, hasBaud, err := lookupInt(props, baudKey)
	if err != nil {
		return base, err
	}
	if hasBaud {
		if baud <= 0 {
			return base, fmt.Errorf("property %q must be a positive bit rate, got %d", baudKey, baud)
		}
		cfg.BaudRate = serialline.BaudRate(baud)
	}

	dataBits, hasDataBits, err := lookupInt(props, dataBitsKey)
	if err != nil {
		return base, err
	}
	if hasDataBits {
		if dataBits < minDataBits || dataBits > maxDataBits {
			return base, fmt.Errorf("property %q must be between %d and %d, got %d", dataBitsKey, minDataBits, maxDataBits, dataBits)
		}
		cfg.DataBits = dataBits
	}

	parity, hasParity, err := lookupString(props, parityKey)
	if err != nil {
		return base, err
	}
	if hasParity {
		p, err := serialline.ParseParity(parity)
		if err != nil {
			return base, fmt.Errorf("property %q: %w", parityKey, err)
		}
		cfg.Parity = p
	}

	stopBits, hasStopBits, err := lookupString(props, stopBitsKey)
	if err != nil {
		return base, err
	}
	if hasStopBits {
		s, err := serialline.ParseStopBits(stopBits)
		if err != nil {
			return base, fmt.Errorf("property %q: %w", stopBitsKey, err)
		}
		cfg.StopBits = s
	}

	return cfg, nil
}

// lookupPort returns the TCP port at key, or 0 when the key is absent,
// refusing a value outside the legal range.
func lookupPort(props inventory.Properties, key string) (int, error) {
	port, present, err := lookupInt(props, key)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	if port < minPort || port > maxPort {
		return 0, fmt.Errorf("property %q must be a TCP port between %d and %d, got %d", key, minPort, maxPort, port)
	}
	return port, nil
}

// lookupString returns the string at key and whether the key is present
// at all.
//
// inventory.Properties' own String collapses "absent" and "present but
// holding a number" into one false, and those are different facts here:
// the first means this device is not cabled that way, the second means
// somebody wrote the wrong kind of value and should be told so rather
// than have their configuration ignored. Reading Raw for presence and
// String for the value is what separates them.
func lookupString(props inventory.Properties, key string) (string, bool, error) {
	if _, present := props.Raw()[key]; !present {
		return "", false, nil
	}
	value, ok := props.String(key)
	if !ok {
		return "", true, fmt.Errorf("property %q must be a string", key)
	}
	return value, true, nil
}

// lookupInt returns the int at key and whether the key is present at
// all, separating absence from a wrong type for the same reason
// lookupString does.
func lookupInt(props inventory.Properties, key string) (int, bool, error) {
	if _, present := props.Raw()[key]; !present {
		return 0, false, nil
	}
	value, ok := props.Int(key)
	if !ok {
		return 0, true, fmt.Errorf("property %q must be a number", key)
	}
	return value, true, nil
}

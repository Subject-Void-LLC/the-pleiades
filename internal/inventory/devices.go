package inventory

type baseDevice struct {
	id    string
	props map[string]interface{}
	tags  []string
}

func (b *baseDevice) ID() string {
	return b.id
}

func (b *baseDevice) Properties() map[string]interface{} {
	return b.props
}

func (b *baseDevice) Tags() []string {
	return b.tags
}

// getStringProp safely extracts a string property or returns an empty string.
func (b *baseDevice) getStringProp(key string) string {
	if val, ok := b.props[key].(string); ok {
		return val
	}
	return ""
}

// getBoolProp safely extracts a bool property or returns false.
func (b *baseDevice) getBoolProp(key string) bool {
	if val, ok := b.props[key].(bool); ok {
		return val
	}
	return false
}

// getIntProp safely extracts an int property.
func (b *baseDevice) getIntProp(key string) int {
	if val, ok := b.props[key].(float64); ok { // JSON unmarshals numbers to float64
		return int(val)
	}
	return 0
}

// CiscoRouter implements InventoryItem, SSHTransportCapable, and CiscoIOSCapable
type CiscoRouter struct {
	baseDevice
}

func (c *CiscoRouter) HasCapability(capName string) bool {
	switch capName {
	case "SSHTransportCapable", "CiscoIOSCapable":
		return true
	default:
		return false
	}
}

func (c *CiscoRouter) SSHHost() string {
	return c.getStringProp("host")
}

func (c *CiscoRouter) SSHPort() int {
	if port := c.getIntProp("port"); port != 0 {
		return port
	}
	return 22
}

func (c *CiscoRouter) IOSVersion() string {
	return c.getStringProp("ios_version")
}

func (c *CiscoRouter) SupportsNETCONF() bool {
	return c.getBoolProp("netconf_enabled")
}

// LinuxServer implements InventoryItem, SSHTransportCapable, and LinuxCapable
type LinuxServer struct {
	baseDevice
}

func (l *LinuxServer) HasCapability(capName string) bool {
	switch capName {
	case "SSHTransportCapable", "LinuxCapable":
		return true
	default:
		return false
	}
}

func (l *LinuxServer) SSHHost() string {
	return l.getStringProp("host")
}

func (l *LinuxServer) SSHPort() int {
	if port := l.getIntProp("port"); port != 0 {
		return port
	}
	return 22
}

func (l *LinuxServer) KernelVersion() string {
	return l.getStringProp("kernel_version")
}

func (l *LinuxServer) Distribution() string {
	return l.getStringProp("distribution")
}

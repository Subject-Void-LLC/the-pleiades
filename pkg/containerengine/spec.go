// A container as a task declares it: parsed and refused once, turned into
// the engine's create request, and compared with a live container.
package containerengine

import (
	"fmt"
	"maps"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

// NetworkNone is the network a container joins when its task names none:
// no network at all, so reaching one is always a declared decision.
const NetworkNone = "none"

// Spec is a container as a task declares it. Every field is checked by
// ParseSpec before anything reaches an engine.
type Spec struct {
	// Name is the container's name.
	Name string
	// Image is the reference the task named; ImageID is what the engine
	// runs, filled once the image is resolved and present.
	Image   string
	ImageID string
	// Command and Entrypoint replace the image's when set.
	Command    []string
	Entrypoint []string
	// Env holds environment values by name. Only the names are ever
	// reported: a value may come from a credential.
	Env map[string]string
	// Ports are published ports; they need a network other than none.
	Ports []Port
	// Mounts are bind mounts (read-only unless declared writable) and named
	// volumes.
	Mounts []Mount
	// Network is the one network the container joins.
	Network string
	// Restart is the restart policy: no, on-failure, always or
	// unless-stopped.
	Restart string
	// Labels are set on the container; the engine's own label
	// (LabelManagedBy) is added to them.
	Labels map[string]string
	// Workdir and User set the process's directory and account.
	Workdir string
	User    string
	// ReadOnly gives the container a read-only root filesystem.
	ReadOnly bool
	// Host-escape settings: each widens what the container can do to its
	// host, and is never a default.
	Privileged bool
	CapAdd     []string
	Devices    []string
}

// Port is one published port.
type Port struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Proto         string
}

// String is the port as docker run writes it.
func (p Port) String() string {
	s := strconv.Itoa(int(p.HostPort)) + ":" + strconv.Itoa(int(p.ContainerPort)) + "/" + p.Proto
	if p.HostIP != "" {
		s = p.HostIP + ":" + s
	}
	return s
}

// Mount is one bind mount or named volume.
type Mount struct {
	// Volume is true for a named volume, false for a host path.
	Volume bool
	// Source is the volume's name or the host's absolute path.
	Source string
	// Target is the absolute path inside the container.
	Target string
	// Writable is the declared exception to read-only.
	Writable bool
}

// String is the mount as docker run writes it.
func (m Mount) String() string {
	mode := "ro"
	if m.Writable {
		mode = "rw"
	}
	return m.Source + ":" + m.Target + ":" + mode
}

// LabelManagedBy is the label every container a method creates carries,
// with the image reference the task asked for as its value.
const LabelManagedBy = "org.pleiades.image"

// restartPolicies are the restart policies a task may name.
var restartPolicies = []string{"no", "on-failure", "always", "unless-stopped"}

// envName is what an environment variable's name may be.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// userSpec is what a container's user may be: a name or ID, optionally
// with a group.
var userSpec = regexp.MustCompile(`^[A-Za-z0-9_.-]+(:[A-Za-z0-9_.-]+)?$`)

// capName is what an added capability's name may be.
var capName = regexp.MustCompile(`^(CAP_)?[A-Z][A-Z_]{1,31}$`)

// ParseSpec reads the container a task declares from params, refusing
// anything an engine would read differently from how it reads here: names
// and images the engine would take as something else, ports with no
// network to publish on, host paths that are relative, climb, or name the
// engine's own socket. Image may be empty for a method that addresses an
// existing container by name alone.
func ParseSpec(params map[string]any) (Spec, error) {
	var s Spec
	var err error
	if s.Name, err = textOf(params, "name"); err != nil {
		return s, err
	}
	if s.Name == "" {
		return s, fmt.Errorf("name is required")
	}
	if err := ValidName("container", s.Name); err != nil {
		return s, err
	}
	if s.Image, err = textOf(params, "image"); err != nil {
		return s, err
	}
	if s.Image != "" {
		if err := ValidImage(s.Image); err != nil {
			return s, err
		}
	}
	if s.Command, err = argvOf(params, "command"); err != nil {
		return s, err
	}
	if s.Entrypoint, err = argvOf(params, "entrypoint"); err != nil {
		return s, err
	}
	if s.Env, err = envOf(params); err != nil {
		return s, err
	}
	if s.Network, err = textOf(params, "network"); err != nil {
		return s, err
	}
	if s.Network == "" {
		s.Network = NetworkNone
	}
	if s.Network != NetworkNone && s.Network != "bridge" && s.Network != "host" {
		if err := ValidName("network", s.Network); err != nil {
			return s, err
		}
	}
	if strings.HasPrefix(s.Network, "container:") {
		return s, fmt.Errorf("network: %q joins another container's namespace, which this method does not do", s.Network)
	}
	ports, err := listOf(params, "ports")
	if err != nil {
		return s, err
	}
	for _, raw := range ports {
		p, err := ParsePortSpec(raw)
		if err != nil {
			return s, err
		}
		s.Ports = append(s.Ports, p)
	}
	if len(s.Ports) > 0 && (s.Network == NetworkNone || s.Network == "host") {
		return s, fmt.Errorf("ports: a container on network %q publishes no port; name the network it should join", s.Network)
	}
	volumes, err := listOf(params, "volumes")
	if err != nil {
		return s, err
	}
	for _, raw := range volumes {
		m, err := ParseMount(raw)
		if err != nil {
			return s, err
		}
		s.Mounts = append(s.Mounts, m)
	}
	if s.Restart, err = textOf(params, "restart_policy"); err != nil {
		return s, err
	}
	if s.Restart == "" {
		s.Restart = "no"
	}
	if !slices.Contains(restartPolicies, s.Restart) {
		return s, fmt.Errorf("restart_policy: %q is not one of %s", s.Restart, strings.Join(restartPolicies, ", "))
	}
	if s.Labels, err = stringMap(params, "labels"); err != nil {
		return s, err
	}
	for k := range s.Labels {
		if k == LabelManagedBy {
			return s, fmt.Errorf("labels: %s is this platform's own", LabelManagedBy)
		}
	}
	if s.Workdir, err = textOf(params, "workdir"); err != nil {
		return s, err
	}
	if s.Workdir != "" && (!path.IsAbs(s.Workdir) || path.Clean(s.Workdir) != s.Workdir) {
		return s, fmt.Errorf("workdir: %q is not an absolute, clean path", s.Workdir)
	}
	if s.User, err = textOf(params, "user"); err != nil {
		return s, err
	}
	if s.User != "" && !userSpec.MatchString(s.User) {
		return s, fmt.Errorf("user: %q is not a user or user:group", s.User)
	}
	if s.ReadOnly, err = boolOf(params, "read_only"); err != nil {
		return s, err
	}
	if s.Privileged, err = boolOf(params, "privileged"); err != nil {
		return s, err
	}
	if s.CapAdd, err = listOf(params, "cap_add"); err != nil {
		return s, err
	}
	for i, c := range s.CapAdd {
		if !capName.MatchString(c) {
			return s, fmt.Errorf("cap_add: %q is not a capability name", c)
		}
		s.CapAdd[i] = strings.TrimPrefix(c, "CAP_")
	}
	if s.Devices, err = listOf(params, "devices"); err != nil {
		return s, err
	}
	for _, d := range s.Devices {
		host, _, _ := strings.Cut(d, ":")
		if !strings.HasPrefix(host, "/dev/") || path.Clean(host) != host {
			return s, fmt.Errorf("devices: %q does not name a device under /dev", d)
		}
	}
	return s, nil
}

// ParsePortSpec reads one published port, [host_ip:]host_port:container_port[/proto].
func ParsePortSpec(raw string) (Port, error) {
	var p Port
	spec, proto, _ := strings.Cut(raw, "/")
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" && proto != "sctp" {
		return p, fmt.Errorf("ports: %q names protocol %q, not tcp, udp or sctp", raw, proto)
	}
	p.Proto = proto
	i := strings.LastIndex(spec, ":")
	if i < 0 {
		return p, fmt.Errorf("ports: %q is not host_port:container_port", raw)
	}
	hostPart, containerPart := spec[:i], spec[i+1:]
	if j := strings.LastIndex(hostPart, ":"); j >= 0 {
		ip := strings.Trim(hostPart[:j], "[]")
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return p, fmt.Errorf("ports: %q: %q is not an IP address", raw, ip)
		}
		p.HostIP = addr.String()
		hostPart = hostPart[j+1:]
	}
	var err error
	if p.HostPort, err = portNumber(hostPart); err != nil {
		return p, fmt.Errorf("ports: %q: %w", raw, err)
	}
	if p.ContainerPort, err = portNumber(containerPart); err != nil {
		return p, fmt.Errorf("ports: %q: %w", raw, err)
	}
	return p, nil
}

// portNumber reads a port from 1 to 65535.
func portNumber(s string) (uint16, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s {
		return 0, fmt.Errorf("%q is not a port from 1 to 65535", s)
	}
	return uint16(n), nil
}

// engineSockets are paths that hand whoever mounts them the engine itself,
// which is root on the host.
var engineSockets = []string{"/var/run/docker.sock", "/run/docker.sock", "/run/podman/podman.sock", "/var/run/podman/podman.sock"}

// ParseMount reads one mount, source:target[:ro|rw]. A source beginning
// with "/" is a host path, which must be absolute and clean and never the
// engine's own socket; anything else is a named volume.
func ParseMount(raw string) (Mount, error) {
	var m Mount
	parts := strings.Split(raw, ":")
	switch len(parts) {
	case 2:
	case 3:
		switch parts[2] {
		case "ro":
		case "rw":
			m.Writable = true
		default:
			return m, fmt.Errorf("volumes: %q ends in %q, not ro or rw", raw, parts[2])
		}
	default:
		return m, fmt.Errorf("volumes: %q is not source:target[:ro|rw]", raw)
	}
	m.Source, m.Target = parts[0], parts[1]
	if !path.IsAbs(m.Target) || path.Clean(m.Target) != m.Target || m.Target == "/" {
		return m, fmt.Errorf("volumes: %q mounts at %q, which is not an absolute, clean path below /", raw, m.Target)
	}
	if strings.HasPrefix(m.Source, "/") {
		if path.Clean(m.Source) != m.Source {
			return m, fmt.Errorf("volumes: %q's host path is not clean", raw)
		}
		if slices.Contains(engineSockets, m.Source) {
			return m, fmt.Errorf("volumes: %q mounts the container engine's own socket, which gives the container the host", raw)
		}
		return m, nil
	}
	if strings.HasPrefix(m.Source, ".") || strings.Contains(m.Source, "/") {
		return m, fmt.Errorf("volumes: %q's source is neither an absolute host path nor a volume name", raw)
	}
	if err := ValidName("volume", m.Source); err != nil {
		return m, err
	}
	m.Volume = true
	return m, nil
}

// Create is the engine's create request for s, which must have ImageID.
func (s Spec) Create() (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	if s.ImageID == "" {
		return nil, nil, nil, fmt.Errorf("containerengine: the image is not resolved")
	}
	labels := maps.Clone(s.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelManagedBy] = s.Image
	cfg := &container.Config{
		Image:      s.ImageID,
		Cmd:        s.Command,
		Entrypoint: s.Entrypoint,
		Env:        envList(s.Env),
		Labels:     labels,
		WorkingDir: s.Workdir,
		User:       s.User,
	}
	host := &container.HostConfig{
		NetworkMode:    container.NetworkMode(s.Network),
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyMode(s.Restart)},
		ReadonlyRootfs: s.ReadOnly,
		Privileged:     s.Privileged,
		CapAdd:         s.CapAdd,
		SecurityOpt:    []string{"no-new-privileges"},
	}
	if s.Privileged {
		// A privileged container is given everything; no-new-privileges
		// would only make its declared intent fail confusingly.
		host.SecurityOpt = nil
	}
	for _, d := range s.Devices {
		hostPath, inner, _ := strings.Cut(d, ":")
		if inner == "" {
			inner = hostPath
		}
		host.Devices = append(host.Devices, container.DeviceMapping{PathOnHost: hostPath, PathInContainer: inner, CgroupPermissions: "rwm"})
	}
	for _, m := range s.Mounts {
		t := mount.TypeBind
		if m.Volume {
			t = mount.TypeVolume
		}
		host.Mounts = append(host.Mounts, mount.Mount{Type: t, Source: m.Source, Target: m.Target, ReadOnly: !m.Writable})
	}
	if len(s.Ports) > 0 {
		cfg.ExposedPorts = network.PortSet{}
		host.PortBindings = network.PortMap{}
		for _, p := range s.Ports {
			port, err := network.ParsePort(strconv.Itoa(int(p.ContainerPort)) + "/" + p.Proto)
			if err != nil {
				return nil, nil, nil, err
			}
			cfg.ExposedPorts[port] = struct{}{}
			binding := network.PortBinding{HostPort: strconv.Itoa(int(p.HostPort))}
			if p.HostIP != "" {
				binding.HostIP = netip.MustParseAddr(p.HostIP)
			}
			host.PortBindings[port] = append(host.PortBindings[port], binding)
		}
	}
	return cfg, host, nil, nil
}

// Differences names every way the live container c differs from s, each
// as one line. Image is compared by ID, so s.ImageID must be set; the
// image's own environment and labels are allowed beside the declared ones.
func (s Spec) Differences(c container.InspectResponse) []string {
	var d []string
	if c.Config == nil || c.HostConfig == nil {
		return []string{"the engine answered no configuration for the container"}
	}
	if c.Image != s.ImageID {
		d = append(d, fmt.Sprintf("image: runs %s, the task names %s (%s)", c.Image, s.ImageID, s.Image))
	}
	if len(s.Command) > 0 && !slices.Equal(c.Config.Cmd, s.Command) {
		d = append(d, fmt.Sprintf("command: %q, the task names %q", c.Config.Cmd, s.Command))
	}
	if len(s.Entrypoint) > 0 && !slices.Equal(c.Config.Entrypoint, s.Entrypoint) {
		d = append(d, fmt.Sprintf("entrypoint: %q, the task names %q", c.Config.Entrypoint, s.Entrypoint))
	}
	live := envMap(c.Config.Env)
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		if v, ok := live[k]; !ok || v != s.Env[k] {
			// The value is never named: it may be a credential.
			d = append(d, fmt.Sprintf("env: %s differs from the task's", k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(s.Labels)) {
		if c.Config.Labels[k] != s.Labels[k] {
			d = append(d, fmt.Sprintf("labels: %s is %q, the task names %q", k, c.Config.Labels[k], s.Labels[k]))
		}
	}
	if string(c.HostConfig.NetworkMode) != s.Network && !(s.Network == "bridge" && c.HostConfig.NetworkMode == "default") {
		d = append(d, fmt.Sprintf("network: %s, the task names %s", c.HostConfig.NetworkMode, s.Network))
	}
	if string(c.HostConfig.RestartPolicy.Name) != s.Restart && !(s.Restart == "no" && c.HostConfig.RestartPolicy.Name == "") {
		d = append(d, fmt.Sprintf("restart_policy: %s, the task names %s", c.HostConfig.RestartPolicy.Name, s.Restart))
	}
	if c.HostConfig.Privileged != s.Privileged {
		d = append(d, fmt.Sprintf("privileged: %t, the task names %t", c.HostConfig.Privileged, s.Privileged))
	}
	if c.HostConfig.ReadonlyRootfs != s.ReadOnly {
		d = append(d, fmt.Sprintf("read_only: %t, the task names %t", c.HostConfig.ReadonlyRootfs, s.ReadOnly))
	}
	if want, got := mountStrings(s.Mounts), liveMounts(c); !slices.Equal(want, got) {
		d = append(d, fmt.Sprintf("volumes: %s, the task names %s", strings.Join(got, ", "), strings.Join(want, ", ")))
	}
	if want, got := portStrings(s.Ports), livePorts(c.HostConfig.PortBindings); !slices.Equal(want, got) {
		d = append(d, fmt.Sprintf("ports: %s, the task names %s", strings.Join(got, ", "), strings.Join(want, ", ")))
	}
	if want, got := sortedCopy(s.CapAdd), sortedCopy(c.HostConfig.CapAdd); !slices.Equal(want, got) {
		d = append(d, fmt.Sprintf("cap_add: %s, the task names %s", strings.Join(got, ", "), strings.Join(want, ", ")))
	}
	return d
}

// EnvNames is s's environment names, sorted: what may be reported.
func (s Spec) EnvNames() []string { return slices.Sorted(maps.Keys(s.Env)) }

// envList is env as KEY=value lines, sorted.
func envList(env map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}

// envMap reads KEY=value lines.
func envMap(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		k, v, _ := strings.Cut(l, "=")
		out[k] = v
	}
	return out
}

// mountStrings is mounts as source:target:mode, sorted.
func mountStrings(mounts []Mount) []string {
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, m.String())
	}
	slices.Sort(out)
	return out
}

// liveMounts is c's bind and named-volume mounts as source:target:mode,
// sorted; anonymous volumes an image declares are its own.
func liveMounts(c container.InspectResponse) []string {
	out := []string{}
	for _, m := range c.Mounts {
		var src string
		switch m.Type {
		case mount.TypeBind:
			src = m.Source
		case mount.TypeVolume:
			if m.Name == "" || len(m.Name) == 64 {
				continue
			}
			src = m.Name
		default:
			continue
		}
		mode := "ro"
		if m.RW {
			mode = "rw"
		}
		out = append(out, src+":"+m.Destination+":"+mode)
	}
	slices.Sort(out)
	return out
}

// portStrings is ports as docker run writes them, sorted.
func portStrings(ports []Port) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

// livePorts is a container's port bindings as docker run writes them,
// sorted.
func livePorts(bindings network.PortMap) []string {
	out := []string{}
	for port, bs := range bindings {
		for _, b := range bs {
			p := Port{ContainerPort: port.Num(), Proto: string(port.Proto())}
			n, err := strconv.Atoi(b.HostPort)
			if err != nil {
				continue
			}
			p.HostPort = uint16(n)
			if b.HostIP.IsValid() && !b.HostIP.IsUnspecified() {
				p.HostIP = b.HostIP.String()
			}
			out = append(out, p.String())
		}
	}
	slices.Sort(out)
	return out
}

// sortedCopy is s sorted, never nil.
func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	slices.Sort(out)
	return out
}

// textOf reads an optional text parameter, refusing any other type.
func textOf(params map[string]any, key string) (string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %v is not text; quote it", key, raw)
	}
	return s, nil
}

// boolOf reads an optional boolean parameter.
func boolOf(params map[string]any, key string) (bool, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s: %v is not true or false", key, raw)
	}
	return b, nil
}

// listOf reads an optional list of text.
func listOf(params map[string]any, key string) ([]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if strs, ok := raw.([]string); ok {
			return append([]string{}, strs...), nil
		}
		return nil, fmt.Errorf("%s is a list", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %v is not text; quote it", key, item)
		}
		out = append(out, s)
	}
	return out, nil
}

// argvOf reads a command as a list of arguments; text is refused, since a
// container's command is never run through a shell unless the task names
// one.
func argvOf(params map[string]any, key string) ([]string, error) {
	if s, ok := params[key].(string); ok {
		return nil, fmt.Errorf("%s: %q is text; give a list of arguments (for a shell, [/bin/sh, -c, \"...\"])", key, s)
	}
	return listOf(params, key)
}

// stringMap reads an optional mapping of text to text.
func stringMap(params map[string]any, key string) (map[string]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is a mapping", key)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s's value is not text; quote it", key, k)
		}
		out[k] = s
	}
	return out, nil
}

// envOf reads env, refusing a name the engine would split differently.
func envOf(params map[string]any) (map[string]string, error) {
	env, err := stringMap(params, "env")
	if err != nil {
		return nil, err
	}
	for k, v := range env {
		if !envName.MatchString(k) {
			return nil, fmt.Errorf("env: %q is not a variable name", k)
		}
		if strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("env: %s's value holds a NUL", k)
		}
	}
	return env, nil
}

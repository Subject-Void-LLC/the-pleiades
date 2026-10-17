// A container as a task declares it: parsed and refused once, turned into
// the engine's create request, and compared with a live container.
package containerengine

import (
	"fmt"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The pull policies a task may name.
const (
	PullMissing = "missing"
	PullNever   = "never"
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
	// Pull is how the image is found: PullMissing (resolve a tag through
	// its registry to a digest, and pull that digest when it is absent) or
	// PullNever (the image the engine already holds under the reference as
	// written, for a loaded or air-gapped image; absent is refused).
	Pull string
	// Host-escape settings: each widens what the container can do to its
	// host, and is never a default.
	Privileged bool
	CapAdd     []string
	Devices    []Device
	// Attempt, when set, is a label value that marks the container one
	// create request made (LabelAttempt), so a create whose answer was
	// lost finds its own container rather than another run's of the same
	// name.
	Attempt string
}

// Device is one host device given to the container, as docker run's
// --device reads it: host[:container[:permissions]].
type Device struct {
	// Host is the device's path on the host, under /dev.
	Host string
	// Container is its path inside the container; Host when not given.
	Container string
	// Perms is the cgroup permissions, a non-empty subset of "rwm"; "rwm"
	// when not given.
	Perms string
}

// String is the device as docker run writes it.
func (d Device) String() string { return d.Host + ":" + d.Container + ":" + d.Perms }

// ParseDevice reads one device, host[:container[:permissions]], as docker
// run does: a third part is permissions, never part of a path.
func ParseDevice(raw string) (Device, error) {
	parts := strings.Split(raw, ":")
	if len(parts) > 3 {
		return Device{}, fmt.Errorf("devices: %q is not host[:container[:permissions]]", raw)
	}
	d := Device{Host: parts[0], Container: parts[0], Perms: "rwm"}
	if len(parts) > 1 {
		d.Container = parts[1]
	}
	if len(parts) > 2 {
		d.Perms = parts[2]
	}
	if !strings.HasPrefix(d.Host, "/dev/") || path.Clean(d.Host) != d.Host {
		return Device{}, fmt.Errorf("devices: %q does not name a device under /dev", raw)
	}
	if !path.IsAbs(d.Container) || path.Clean(d.Container) != d.Container || d.Container == "/" {
		return Device{}, fmt.Errorf("devices: %q gives the device at %q, which is not an absolute, clean path below /", raw, d.Container)
	}
	if !validPerms(d.Perms) {
		return Device{}, fmt.Errorf("devices: %q gives permissions %q, which is not some of r, w and m, each once", raw, d.Perms)
	}
	return d, nil
}

// validPerms reports whether p is a non-empty subset of "rwm", each letter
// at most once.
func validPerms(p string) bool {
	if p == "" || len(p) > 3 {
		return false
	}
	seen := map[rune]bool{}
	for _, r := range p {
		if !strings.ContainsRune("rwm", r) || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
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

// LabelAttempt carries Spec.Attempt: which create request made the
// container.
const LabelAttempt = "org.pleiades.attempt"

// restartPolicies are the restart policies a task may name.
var restartPolicies = []string{"no", "on-failure", "always", "unless-stopped"}

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
	if s.Pull, err = textOf(params, "pull"); err != nil {
		return s, err
	}
	if s.Pull == "" {
		s.Pull = PullMissing
	}
	if s.Pull != PullMissing && s.Pull != PullNever {
		return s, fmt.Errorf("pull: %q is not %s or %s", s.Pull, PullMissing, PullNever)
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
		if k == LabelManagedBy || k == LabelAttempt {
			return s, fmt.Errorf("labels: %s is this platform's own", k)
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
	devices, err := listOf(params, "devices")
	if err != nil {
		return s, err
	}
	for _, raw := range devices {
		d, err := ParseDevice(raw)
		if err != nil {
			return s, err
		}
		s.Devices = append(s.Devices, d)
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
		p.HostIP = portHostIP(addr)
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

// engineSockets are paths that hand whoever mounts them an engine, which
// is root on the host (or the user, for a rootless one). A host path that
// is one, or holds one (/, /run, /var/run), is refused.
var engineSockets = []string{
	"/var/run/docker.sock", "/run/docker.sock",
	"/run/podman/podman.sock", "/var/run/podman/podman.sock",
	"/run/containerd/containerd.sock", "/var/run/containerd/containerd.sock",
}

// reachesEngineSocket reports whether mounting the host path p hands the
// container an engine's socket: p is one, holds one, or is in a user's
// runtime directory, where rootless Docker and Podman keep theirs.
func reachesEngineSocket(p string) bool {
	for _, sock := range engineSockets {
		if p == sock || p == "/" || strings.HasPrefix(sock, p+"/") {
			return true
		}
	}
	return p == "/run/user" || strings.HasPrefix(p, "/run/user/") ||
		p == "/var/run/user" || strings.HasPrefix(p, "/var/run/user/")
}

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
		if reachesEngineSocket(m.Source) {
			return m, fmt.Errorf("volumes: %q mounts a container engine's socket, or a directory holding one, which gives the container the host", raw)
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

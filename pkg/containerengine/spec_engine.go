// A container spec as the engine sees it: the create request it becomes,
// and how a live container differs from it.
package containerengine

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

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
	if s.Attempt != "" {
		labels[LabelAttempt] = s.Attempt
	}
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
		Resources:      container.Resources{Memory: s.Memory, MemorySwap: s.MemorySwap},
	}
	if s.Privileged {
		// A privileged container is given everything; no-new-privileges
		// would only make its declared intent fail confusingly.
		host.SecurityOpt = nil
	}
	for _, d := range s.Devices {
		host.Devices = append(host.Devices, container.DeviceMapping{PathOnHost: d.Host, PathInContainer: d.Container, CgroupPermissions: d.Perms})
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
	if want, got := sortedCopy(s.CapAdd), liveCaps(c.HostConfig.CapAdd); !slices.Equal(want, got) {
		d = append(d, fmt.Sprintf("cap_add: %s, the task names %s", strings.Join(got, ", "), strings.Join(want, ", ")))
	}
	if !devicesMatch(s.Devices, c.HostConfig.Devices) {
		d = append(d, fmt.Sprintf("devices: %s, the task names %s", strings.Join(liveDevices(c.HostConfig.Devices), ", "), strings.Join(deviceStrings(s.Devices), ", ")))
	}
	// A limit the task does not name is not compared, as user is not: it
	// says nothing about one the container was given.
	if s.Memory != 0 && c.HostConfig.Memory != s.Memory {
		d = append(d, fmt.Sprintf("memory: %d bytes, the task names %d", c.HostConfig.Memory, s.Memory))
	}
	if s.MemorySwap != 0 && c.HostConfig.MemorySwap != s.MemorySwap {
		d = append(d, fmt.Sprintf("memory_swap: %d bytes, the task names %d", c.HostConfig.MemorySwap, s.MemorySwap))
	}
	// An unset user or workdir is the image's own, which the task does not
	// name, so only a declared one is compared, as command is.
	if s.User != "" && c.Config.User != s.User {
		d = append(d, fmt.Sprintf("user: %q, the task names %q", c.Config.User, s.User))
	}
	if s.Workdir != "" && c.Config.WorkingDir != s.Workdir {
		d = append(d, fmt.Sprintf("workdir: %q, the task names %q", c.Config.WorkingDir, s.Workdir))
	}
	if !s.Privileged && !slices.Contains(c.HostConfig.SecurityOpt, "no-new-privileges") && !slices.Contains(c.HostConfig.SecurityOpt, "no-new-privileges:true") {
		d = append(d, "security: the container may gain privileges (no-new-privileges is not set), which every container this platform makes has")
	}
	return d
}

// liveCaps is a container's added capabilities without the CAP_ prefix
// Podman reports them with, sorted.
func liveCaps(caps []string) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, strings.TrimPrefix(c, "CAP_"))
	}
	slices.Sort(out)
	return out
}

// deviceStrings is devices as docker run writes them, sorted.
func deviceStrings(devices []Device) []string {
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, d.String())
	}
	slices.Sort(out)
	return out
}

// devicesMatch reports whether the live devices are the declared ones, by
// the path each has inside the container. Host path and permissions are
// compared where the engine reports permissions: Docker does; Podman
// reports a device by its numbers, so /dev/null (char 1:3) comes back as
// whichever /dev name it finds first for 1:3, with no permissions.
func devicesMatch(want []Device, live []container.DeviceMapping) bool {
	if len(want) != len(live) {
		return false
	}
	byPath := map[string]container.DeviceMapping{}
	for _, l := range live {
		byPath[l.PathInContainer] = l
	}
	for _, w := range want {
		l, ok := byPath[w.Container]
		if !ok {
			return false
		}
		if l.CgroupPermissions != "" && (l.PathOnHost != w.Host || l.CgroupPermissions != w.Perms) {
			return false
		}
	}
	return true
}

// liveDevices is a container's devices as docker run writes them, sorted.
func liveDevices(devices []container.DeviceMapping) []string {
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, Device{Host: d.PathOnHost, Container: d.PathInContainer, Perms: d.CgroupPermissions}.String())
	}
	slices.Sort(out)
	return out
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
			n, err := strconv.ParseUint(b.HostPort, 10, 16)
			if err != nil {
				continue
			}
			p.HostPort = uint16(n)
			p.HostIP = portHostIP(b.HostIP)
			out = append(out, p.String())
		}
	}
	slices.Sort(out)
	return out
}

// portHostIP is a binding's host address as compared: none and 0.0.0.0
// are one, since Podman reports 0.0.0.0 for a port given no address and
// Docker keeps 0.0.0.0 when one is given; any other address, :: included,
// is itself.
func portHostIP(a netip.Addr) string {
	if !a.IsValid() || a == netip.IPv4Unspecified() {
		return ""
	}
	return a.String()
}

// sortedCopy is s sorted, never nil.
func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	slices.Sort(out)
	return out
}

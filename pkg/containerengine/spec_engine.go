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

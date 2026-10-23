// Tests for the container spec: what a task may declare, the request it
// becomes, and how it is compared with a live container.
package containerengine

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

// TestParseSpec_SafeDefaults: a container a task names with no more than
// its name and image joins no network, restarts never, and mounts nothing.
func TestParseSpec_SafeDefaults(t *testing.T) {
	s, err := ParseSpec(map[string]any{"name": "web", "image": "nginx:1.27"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Network != NetworkNone || s.Restart != "no" || len(s.Mounts) != 0 || s.Privileged {
		t.Errorf("defaults = %+v", s)
	}
	m, err := ParseMount("/srv/conf:/etc/app")
	if err != nil || m.Writable || m.Volume {
		t.Errorf("a bind mount with no mode = %+v, %v; want read-only", m, err)
	}
}

// TestParseSpec_RefusesWhatAnEngineWouldReadOtherwise: each hostile or
// malformed value is refused, naming its parameter.
func TestParseSpec_RefusesWhatAnEngineWouldReadOtherwise(t *testing.T) {
	base := func(k string, v any) map[string]any {
		p := map[string]any{"name": "web", "image": "nginx:1.27", "network": "appnet"}
		p[k] = v
		return p
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"no name":                   {map[string]any{"image": "nginx"}, "name is required"},
		"a name like an option":     {base("name", "--privileged"), "name"},
		"an image like an option":   {base("image", "--privileged"), "image"},
		"command as text":           {base("command", "rm -rf /"), "give a list of arguments"},
		"a port with no network":    {map[string]any{"name": "web", "image": "nginx", "ports": []any{"8080:80"}}, "publishes no port"},
		"a port out of range":       {base("ports", []any{"70000:80"}), "not a port"},
		"a port with a bad ip":      {base("ports", []any{"evil:8080:80"}), "not an IP address"},
		"a port with a bad proto":   {base("ports", []any{"8080:80/icmp"}), "not tcp, udp or sctp"},
		"a relative host path":      {base("volumes", []any{"./data:/data"}), "neither an absolute host path nor a volume name"},
		"a climbing host path":      {base("volumes", []any{"/srv/../etc:/data"}), "not clean"},
		"the engine socket":         {base("volumes", []any{"/var/run/docker.sock:/var/run/docker.sock"}), "engine's socket"},
		"the socket's directory":    {base("volumes", []any{"/var/run:/x"}), "engine's socket"},
		"/run":                      {base("volumes", []any{"/run:/x"}), "engine's socket"},
		"the host's root":           {base("volumes", []any{"/:/host:rw"}), "engine's socket"},
		"a rootless socket":         {base("volumes", []any{"/run/user/1000/docker.sock:/s"}), "engine's socket"},
		"a rootless runtime dir":    {base("volumes", []any{"/run/user/1000:/s"}), "engine's socket"},
		"containerd's socket":       {base("volumes", []any{"/run/containerd/containerd.sock:/s"}), "engine's socket"},
		"podman's directory":        {base("volumes", []any{"/run/podman:/s"}), "engine's socket"},
		"our attempt label":         {base("labels", map[string]any{LabelAttempt: "x"}), "this platform's own"},
		"a device path in a perm":   {base("devices", []any{"/dev/sda:/dev/xvdc:rx"}), "permissions"},
		"a relative device inside":  {base("devices", []any{"/dev/sda:xvdc"}), "absolute, clean path"},
		"a device with four parts":  {base("devices", []any{"/dev/sda:/dev/x:r:w"}), "host[:container"},
		"a doubled permission":      {base("devices", []any{"/dev/sda:/dev/x:rr"}), "permissions"},
		"a mount at /":              {base("volumes", []any{"/srv:/"}), "below /"},
		"a bad mount mode":          {base("volumes", []any{"/srv:/data:z"}), "not ro or rw"},
		"a bad restart policy":      {base("restart_policy", "sometimes"), "restart_policy"},
		"our own label":             {base("labels", map[string]any{LabelManagedBy: "x"}), "this platform's own"},
		"a relative workdir":        {base("workdir", "app"), "workdir"},
		"a user with a shell":       {base("user", "root;id"), "user"},
		"an env name with =":        {base("env", map[string]any{"A=B": "c"}), "variable name"},
		"a capability name like sh": {base("cap_add", []any{"$(id)"}), "capability name"},
		"a device outside /dev":     {base("devices", []any{"/etc/shadow"}), "under /dev"},
		"another container's net":   {base("network", "container:db"), "network"},
		"a number for text":         {base("image", 42), "not text"},
		"memory below the floor":    {base("memory", "1m"), "below"},
		"memory not a size":         {base("memory", "lots"), "not a size"},
		"memory a fraction":         {base("memory", 1.5), "whole number"},
		"memory negative":           {base("memory", "-1"), "not a size"},
		"swap without memory":       {base("memory_swap", "1g"), "needs memory"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSpec(tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseSpec = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestParsePortSpec reads every form a task may write.
func TestParsePortSpec(t *testing.T) {
	for raw, want := range map[string]string{
		"8080:80":           "8080:80/tcp",
		"127.0.0.1:8080:80": "127.0.0.1:8080:80/tcp",
		"[::1]:53:53/udp":   "::1:53:53/udp",
		"9000:9000/sctp":    "9000:9000/sctp",
	} {
		p, err := ParsePortSpec(raw)
		if err != nil || p.String() != want {
			t.Errorf("ParsePortSpec(%q) = %s, %v; want %s", raw, p, err, want)
		}
	}
}

// TestSpec_CreateAndDifferences: the request a spec becomes, read back
// as the engine reports it, differs in nothing; each changed field is then
// named, and an environment value never is.
func TestSpec_CreateAndDifferences(t *testing.T) {
	s, err := ParseSpec(map[string]any{
		"name": "web", "image": "nginx:1.27", "network": "appnet",
		"command":        []any{"nginx", "-g", "daemon off;"},
		"env":            map[string]any{"TOKEN": "secret-sentinel"},
		"ports":          []any{"127.0.0.1:8080:80"},
		"volumes":        []any{"/srv/conf:/etc/nginx/conf.d", "cache:/var/cache/nginx:rw"},
		"labels":         map[string]any{"app": "web"},
		"restart_policy": "unless-stopped",
		"memory":         "512m",
		"memory_swap":    "1g",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Memory != 512<<20 || s.MemorySwap != 1<<30 {
		t.Errorf("memory %d, swap %d", s.Memory, s.MemorySwap)
	}
	if _, _, _, err := s.Create(); err == nil {
		t.Error("a spec with no image ID made a request")
	}
	s.ImageID = "sha256:" + strings.Repeat("a", 64)
	cfg, host, _, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Labels[LabelManagedBy] != "nginx:1.27" || !strings.Contains(strings.Join(host.SecurityOpt, ","), "no-new-privileges") {
		t.Errorf("create = %+v / %+v", cfg, host)
	}
	live := container.InspectResponse{
		Image:      s.ImageID,
		Config:     cfg,
		HostConfig: host,
		Mounts: []container.MountPoint{
			{Type: mount.TypeBind, Source: "/srv/conf", Destination: "/etc/nginx/conf.d", RW: false},
			{Type: mount.TypeVolume, Name: "cache", Destination: "/var/cache/nginx", RW: true},
		},
	}
	if d := s.Differences(live); len(d) != 0 {
		t.Fatalf("the request read back differs: %v", d)
	}
	live.Config.Env = []string{"TOKEN=other"}
	live.HostConfig.RestartPolicy.Name = "always"
	live.HostConfig.Memory = 256 << 20
	live.Image = "sha256:" + strings.Repeat("b", 64)
	d := strings.Join(s.Differences(live), "\n")
	for _, want := range []string{"env: TOKEN", "restart_policy: always", "image: runs sha256:bbb", "memory: 268435456 bytes"} {
		if !strings.Contains(d, want) {
			t.Errorf("differences lack %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "secret-sentinel") || strings.Contains(d, "other") {
		t.Errorf("a difference names an environment value:\n%s", d)
	}
}

// TestParseDevice reads docker run's three forms: a third part is
// permissions, never part of the path inside the container.
func TestParseDevice(t *testing.T) {
	for raw, want := range map[string]Device{
		"/dev/sda":              {Host: "/dev/sda", Container: "/dev/sda", Perms: "rwm"},
		"/dev/sda:/dev/xvdc":    {Host: "/dev/sda", Container: "/dev/xvdc", Perms: "rwm"},
		"/dev/sda:/dev/xvdc:r":  {Host: "/dev/sda", Container: "/dev/xvdc", Perms: "r"},
		"/dev/fuse:/dev/fuse:m": {Host: "/dev/fuse", Container: "/dev/fuse", Perms: "m"},
	} {
		got, err := ParseDevice(raw)
		if err != nil || got != want {
			t.Errorf("ParseDevice(%q) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
}

// TestMount_AllowsAPathBesideTheSockets: a host path that neither is nor
// holds a socket is still a mount.
func TestMount_AllowsAPathBesideTheSockets(t *testing.T) {
	for _, raw := range []string{"/srv/data:/data", "/run/app:/x", "/var/lib/app:/x:rw"} {
		if _, err := ParseMount(raw); err != nil {
			t.Errorf("ParseMount(%q) = %v", raw, err)
		}
	}
}

// TestSizeOf reads every form a size may take, and -1 only where unlimited
// swap is meant.
func TestSizeOf(t *testing.T) {
	for raw, want := range map[any]int64{"6m": 6 << 20, "2G": 2 << 30, "1024k": 1 << 20, "4096b": 4096, "8192": 8192, 8192: 8192, float64(8192): 8192} {
		if got, err := sizeOf(map[string]any{"m": raw}, "m", false); err != nil || got != want {
			t.Errorf("sizeOf(%v) = %d, %v; want %d", raw, got, err, want)
		}
	}
	if got, err := sizeOf(map[string]any{"s": "-1"}, "s", true); err != nil || got != -1 {
		t.Errorf("unlimited swap = %d, %v", got, err)
	}
	for _, raw := range []any{"", "m", "1t", "-2", "9223372036854775807g", true} {
		if _, err := sizeOf(map[string]any{"m": raw}, "m", true); err == nil {
			t.Errorf("sizeOf(%v) was accepted", raw)
		}
	}
}

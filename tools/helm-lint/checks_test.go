// Tests for the invariant checks, run without helm and without a chart.
//
// The checks are exercised against hand-built objects here because the two
// halves fail differently: a rule that never fires and a rule that fires on
// everything both look like a clean `go run ./tools/helm-lint` against a
// correct chart. main.go's own run against the real chart proves the rules
// pass on good input; these prove they fail on bad input.
package main

import (
	"strings"
	"testing"
)

// ptr returns a pointer to any value, which the security-context fields need
// because absent and false have to stay distinguishable.
func ptr[T any](v T) *T { return &v }

// hardened is a container that satisfies every rule, used as the starting
// point each test breaks one field of.
func hardened() container {
	return container{
		Name:  "controller",
		Image: "pleiades/controller:dev",
		SecurityContext: &securityContext{
			RunAsNonRoot:             ptr(true),
			RunAsUser:                ptr(int64(65532)),
			ReadOnlyRootFilesystem:   ptr(true),
			AllowPrivilegeEscalation: ptr(false),
			Capabilities:             &capabilities{Drop: []string{"ALL"}},
			SeccompProfile:           &seccomp{Type: "RuntimeDefault"},
		},
		LivenessProbe:  &probe{HTTPGet: &httpGetAction{Path: "/healthz", Scheme: "HTTPS"}},
		ReadinessProbe: &probe{HTTPGet: &httpGetAction{Path: "/readyz", Scheme: "HTTPS"}},
	}
}

func TestSplitImage(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantRepo string
		wantTag  string
		wantOK   bool
	}{
		{name: "plain", ref: "pleiades/controller:dev", wantRepo: "pleiades/controller", wantTag: "dev", wantOK: true},
		{name: "no tag", ref: "pleiades/controller", wantRepo: "pleiades/controller", wantTag: "", wantOK: false},
		{
			// The case a naive strings.Split(ref, ":") gets wrong: the colon
			// belongs to the registry's port, and the reference has no tag.
			name: "registry port and no tag", ref: "registry.example.com:5000/pleiades/controller",
			wantRepo: "registry.example.com:5000/pleiades/controller", wantTag: "", wantOK: false,
		},
		{
			name: "registry port with tag", ref: "registry.example.com:5000/pleiades/controller:0.1.0",
			wantRepo: "registry.example.com:5000/pleiades/controller", wantTag: "0.1.0", wantOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, tag, ok := splitImage(tc.ref)
			if repo != tc.wantRepo || tag != tc.wantTag || ok != tc.wantOK {
				t.Fatalf("splitImage(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.ref, repo, tag, ok, tc.wantRepo, tc.wantTag, tc.wantOK)
			}
		})
	}
}

func TestCheckImage(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		wantWord string
	}{
		{name: "explicit tag is accepted", image: "pleiades/controller:dev"},
		{name: "registry with port and tag is accepted", image: "registry.example.com:5000/pleiades/controller:0.1.0"},
		{name: "latest is refused", image: "pleiades/controller:latest", wantWord: "latest"},
		{name: "no tag is refused", image: "pleiades/controller", wantWord: "no tag"},
		{name: "digest is refused", image: "pleiades/controller@sha256:0123456789abcdef", wantWord: "digest"},
		{name: "tag and digest together is refused", image: "pleiades/controller:dev@sha256:0123456789abcdef", wantWord: "digest"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := hardened()
			c.Image = tc.image
			findings := checkImage("p", "Deployment/x", c)

			if tc.wantWord == "" {
				if len(findings) != 0 {
					t.Fatalf("checkImage(%q) reported %v, want nothing", tc.image, findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("checkImage(%q) reported %d findings, want 1", tc.image, len(findings))
			}
			if !strings.Contains(findings[0].message, tc.wantWord) {
				t.Fatalf("checkImage(%q) said %q, want it to mention %q", tc.image, findings[0].message, tc.wantWord)
			}
		})
	}
}

func TestCheckProbes(t *testing.T) {
	t.Run("both probes on different paths pass", func(t *testing.T) {
		if findings := checkProbes("p", "Deployment/x", hardened()); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a missing liveness probe is a finding", func(t *testing.T) {
		c := hardened()
		c.LivenessProbe = nil
		findings := checkProbes("p", "Deployment/x", c)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "no livenessProbe") {
			t.Fatalf("reported %v, want one finding about a missing liveness probe", findings)
		}
	})

	t.Run("a missing readiness probe is a finding", func(t *testing.T) {
		c := hardened()
		c.ReadinessProbe = nil
		findings := checkProbes("p", "Deployment/x", c)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "no readinessProbe") {
			t.Fatalf("reported %v, want one finding about a missing readiness probe", findings)
		}
	})

	t.Run("one path answering both questions is a finding", func(t *testing.T) {
		c := hardened()
		c.LivenessProbe = &probe{HTTPGet: &httpGetAction{Path: "/readyz", Scheme: "HTTPS"}}
		findings := checkProbes("p", "Deployment/x", c)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "both target") {
			t.Fatalf("reported %v, want one finding about a shared path", findings)
		}
	})

	t.Run("two probes of different kinds are not a shared path", func(t *testing.T) {
		// The in-chart PostgreSQL: a TCP liveness probe and an exec readiness
		// probe answer different questions and share nothing.
		c := hardened()
		c.Name = "postgres"
		c.LivenessProbe = &probe{TCPSocket: &tcpAction{}}
		c.ReadinessProbe = &probe{Exec: &execAction{Command: []string{"pg_isready"}}}
		if findings := checkProbes("p", "StatefulSet/x", c); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("the runner is no longer waived", func(t *testing.T) {
		// It was, for its whole life, because cmd/runner had nothing a probe
		// could ask. It has a healthcheck subcommand now and both probes in
		// the chart, so a waiver here would let somebody delete them again
		// without a single test going red.
		if _, waived := probeWaivers["runner"]; waived {
			t.Fatal(`probeWaivers still exempts "runner", but the runner container now carries both probes`)
		}
		c := container{Name: "runner", Image: "pleiades/runner:dev"}
		findings := checkProbes("p", "Deployment/x", c)
		if len(findings) != 2 {
			t.Fatalf("reported %v, want a finding for each missing probe", findings)
		}
	})

	t.Run("a waived container may ship without probes", func(t *testing.T) {
		// The mechanism itself, exercised against a table entry this test
		// supplies. probeWaivers is empty today, and a test that only ran
		// against the real table would stop checking the rule the moment the
		// last entry went, which is exactly when it did.
		withWaiver(t, "legacy-sidecar", probeWaiver{
			reason:    "a test-only waiver",
			sourceDir: "cmd/runner",
			endedBy:   "a literal that appears in no source file",
			remedy:    "delete it",
		})
		c := container{Name: "legacy-sidecar", Image: "example/sidecar:dev"}
		if findings := checkProbes("p", "Deployment/x", c); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing for a waived container", findings)
		}
	})

	t.Run("a waived container that gains a probe must lose its waiver", func(t *testing.T) {
		// A waiver says "there is nothing here to ask". The day that stops
		// being true, an entry saying otherwise would sit here forever.
		withWaiver(t, "legacy-sidecar", probeWaiver{
			reason:    "a test-only waiver",
			sourceDir: "cmd/runner",
			endedBy:   "a literal that appears in no source file",
			remedy:    "delete it",
		})
		c := container{
			Name:          "legacy-sidecar",
			Image:         "example/sidecar:dev",
			LivenessProbe: &probe{HTTPGet: &httpGetAction{Path: "/healthz"}},
		}
		findings := checkProbes("p", "Deployment/x", c)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "Delete its waiver entry") {
			t.Fatalf("reported %v, want one finding telling the waiver to go", findings)
		}
	})
}

// withWaiver adds one entry to the package's waiver table for the duration of
// a test and removes it afterwards.
//
// The table is a package variable rather than a parameter, which is fine for a
// tool whose whole job is to hold one fixed list, and this is what keeps a
// test that needs an entry from leaking it into the next one.
func withWaiver(t *testing.T, name string, waiver probeWaiver) {
	t.Helper()
	probeWaivers[name] = waiver
	t.Cleanup(func() { delete(probeWaivers, name) })
}

func TestCheckSecurity(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*container)
		pod      *securityContext
		wantWord string
	}{
		{name: "fully hardened passes", mutate: func(*container) {}},
		{
			name:     "privileged is refused",
			mutate:   func(c *container) { c.SecurityContext.Privileged = ptr(true) },
			wantWord: "privileged",
		},
		{
			name:     "root uid is refused",
			mutate:   func(c *container) { c.SecurityContext.RunAsUser = ptr(int64(0)) },
			wantWord: "runAsUser is 0",
		},
		{
			name:     "an unstated uid is refused",
			mutate:   func(c *container) { c.SecurityContext.RunAsUser = nil },
			wantWord: "NUMERIC",
		},
		{
			name:     "an unstated runAsNonRoot is refused",
			mutate:   func(c *container) { c.SecurityContext.RunAsNonRoot = nil },
			wantWord: "runAsNonRoot is not true",
		},
		{
			name:     "a writable root filesystem is refused",
			mutate:   func(c *container) { c.SecurityContext.ReadOnlyRootFilesystem = ptr(false) },
			wantWord: "readOnlyRootFilesystem",
		},
		{
			name:     "privilege escalation is refused",
			mutate:   func(c *container) { c.SecurityContext.AllowPrivilegeEscalation = ptr(true) },
			wantWord: "allowPrivilegeEscalation",
		},
		{
			name:     "keeping capabilities is refused",
			mutate:   func(c *container) { c.SecurityContext.Capabilities = &capabilities{Drop: []string{"NET_RAW"}} },
			wantWord: "drop ALL",
		},
		{
			name: "adding a capability back is refused",
			mutate: func(c *container) {
				c.SecurityContext.Capabilities = &capabilities{Drop: []string{"ALL"}, Add: []string{"NET_BIND_SERVICE"}}
			},
			wantWord: "adds capabilities back",
		},
		{
			name:     "an unconfined seccomp profile is refused",
			mutate:   func(c *container) { c.SecurityContext.SeccompProfile = &seccomp{Type: "Unconfined"} },
			wantWord: "seccompProfile",
		},
		{
			name: "a pod-level uid and profile satisfy a container that states neither",
			mutate: func(c *container) {
				c.SecurityContext.RunAsUser = nil
				c.SecurityContext.RunAsNonRoot = nil
				c.SecurityContext.SeccompProfile = nil
			},
			pod: &securityContext{
				RunAsNonRoot:   ptr(true),
				RunAsUser:      ptr(int64(65532)),
				SeccompProfile: &seccomp{Type: "RuntimeDefault"},
			},
		},
		{
			name: "a pod-level read-only root does NOT satisfy a container",
			// readOnlyRootFilesystem has no pod-level counterpart in the
			// Kubernetes API, so a pod that appears to set it is setting
			// nothing and the container is writable.
			mutate: func(c *container) { c.SecurityContext.ReadOnlyRootFilesystem = nil },
			pod: &securityContext{
				ReadOnlyRootFilesystem: ptr(true),
			},
			wantWord: "readOnlyRootFilesystem",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := hardened()
			tc.mutate(&c)
			findings := checkSecurity("p", "Deployment/x", c, tc.pod)

			if tc.wantWord == "" {
				if len(findings) != 0 {
					t.Fatalf("reported %v, want nothing", findings)
				}
				return
			}
			for _, f := range findings {
				if strings.Contains(f.message, tc.wantWord) {
					return
				}
			}
			t.Fatalf("reported %v, want a finding mentioning %q", findings, tc.wantWord)
		})
	}
}

func TestCheckManifests(t *testing.T) {
	controllerDeployment := func(pod podSpec) manifest {
		return manifest{
			Kind:     "Deployment",
			Metadata: objectMeta{Name: "release-the-pleiades-controller"},
			Spec:     manifestSpec{Template: podTemplate{Spec: pod}},
		}
	}
	hardenedPod := podSpec{Containers: []container{hardened()}}

	t.Run("a hardened controller passes", func(t *testing.T) {
		findings := checkManifests("p", []manifest{controllerDeployment(hardenedPod)}, "HTTPS")
		if len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a hostPath volume is refused", func(t *testing.T) {
		pod := hardenedPod
		pod.Volumes = []volume{{Name: "docker", HostPath: &hostPathes{Path: "/var/run/docker.sock"}}}
		findings := checkManifests("p", []manifest{controllerDeployment(pod)}, "HTTPS")
		if len(findings) != 1 || !strings.Contains(findings[0].message, "hostPath") {
			t.Fatalf("reported %v, want one finding about the host mount", findings)
		}
	})

	t.Run("the wrong probe scheme is refused", func(t *testing.T) {
		findings := checkManifests("p", []manifest{controllerDeployment(hardenedPod)}, "HTTP")
		if len(findings) != 2 {
			t.Fatalf("reported %v, want one finding per probe", findings)
		}
	})

	t.Run("a volumeMount naming no declared volume is refused", func(t *testing.T) {
		c := hardened()
		c.VolumeMounts = []volumeMount{{Name: "known-hosts", MountPath: "/app/ssh"}}
		pod := podSpec{Containers: []container{c}}

		findings := checkManifests("p", []manifest{controllerDeployment(pod)}, "HTTPS")
		if len(findings) != 1 || !strings.Contains(findings[0].message, "does not declare") {
			t.Fatalf("reported %v, want one finding about the missing volume", findings)
		}
	})

	t.Run("a volumeMount backed by a pod volume is accepted", func(t *testing.T) {
		c := hardened()
		c.VolumeMounts = []volumeMount{{Name: "known-hosts", MountPath: "/app/ssh"}}
		pod := podSpec{Containers: []container{c}, Volumes: []volume{{Name: "known-hosts"}}}

		findings := checkManifests("p", []manifest{controllerDeployment(pod)}, "HTTPS")
		if len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a volumeMount backed by a StatefulSet claim template is accepted", func(t *testing.T) {
		// The false positive this check shipped with for about a minute. A
		// StatefulSet declares its storage in volumeClaimTemplates, which is
		// a different part of the object, so both database workloads in this
		// chart looked broken until the check learned to read it.
		c := hardened()
		c.VolumeMounts = []volumeMount{{Name: "data", MountPath: "/var/lib/postgresql/data"}}
		obj := controllerDeployment(podSpec{Containers: []container{c}})
		obj.Spec.VolumeClaimTemplates = []claimTemplate{{Metadata: objectMeta{Name: "data"}}}

		findings := checkManifests("p", []manifest{obj}, "HTTPS")
		if len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a render with no controller proves nothing and says so", func(t *testing.T) {
		// The failure mode a linter develops silently: parsing changes, the
		// workloads stop being recognized, and a run over zero containers
		// reports success.
		findings := checkManifests("p", []manifest{{Kind: "Service", Metadata: objectMeta{Name: "svc"}}}, "HTTPS")
		if len(findings) != 1 || !strings.Contains(findings[0].message, "proved nothing") {
			t.Fatalf("reported %v, want the empty-render guard to fire", findings)
		}
	})

	t.Run("a workload with no containers is a parse failure, not a pass", func(t *testing.T) {
		findings := checkManifests("p", []manifest{controllerDeployment(podSpec{})}, "HTTPS")
		if len(findings) != 2 {
			t.Fatalf("reported %v, want the no-containers finding and the no-controller finding", findings)
		}
	})
}

func TestDecodeManifests(t *testing.T) {
	// Helm's output starts every document with a "# Source:" comment, and a
	// leading separator produces an empty document that must not become an
	// object with an empty kind.
	rendered := `---
# Source: the-pleiades/templates/controller-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: release-controller
---
# Source: the-pleiades/templates/controller-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: release-controller
spec:
  template:
    spec:
      containers:
        - name: controller
          image: pleiades/controller:dev
`
	objects, err := decodeManifests(rendered)
	if err != nil {
		t.Fatalf("decodeManifests: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("decoded %d objects, want 2", len(objects))
	}
	if got := countContainers(objects); got != 1 {
		t.Fatalf("countContainers = %d, want 1", got)
	}
}

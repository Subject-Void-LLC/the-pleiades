// The rule that every file a mounted configuration names is a file that is
// really mounted.
//
// It exists because of a measured defect rather than a category somebody
// imagined. The broker's ConfigMap emitted
//
//	websocket { tls { cert_file: "/etc/nats/tls/tls.crt" ... } }
//
// whenever nats.websocket.tls was set, while the Secret volume carrying
// that file and the mount that placed it were both gated on
// nats.tls.enabled alone. Setting the first without the second is a real
// arrangement (wss:// on 443 through an ingress, plaintext nats:// inside
// the cluster), and it rendered a manifest that applies cleanly, passes
// every other rule in this tool, and produces a broker that dies at
// startup on a file that was never mounted.
//
// WHY THE EXISTING RULE DID NOT CATCH IT, since it looks like it should
// have. checkVolumeMounts requires every volumeMount to name a declared
// volume. Here there was no dangling mount: the mount and the volume were
// gated on the same condition, so both were simply absent together, and
// the only thing left pointing at the missing path was a string inside a
// ConfigMap. Nothing in Kubernetes and nothing in this tool reads that
// string, which is exactly why the failure waits until the container is
// running.
//
// The rule is deliberately general rather than a check for this one key.
// What it encodes is that a configuration file and the mounts around it
// are one artifact rendered in two templates, and that the only thing
// connecting them is that somebody kept two conditions in step.
package main

import (
	"fmt"
	"regexp"
	"strings"
)

// configuredFilePattern matches a configuration line naming a file.
//
// It keys on the `_file:` suffix that nats-server's own configuration
// grammar uses for every path it reads (cert_file, key_file, ca_file), and
// on a quoted ABSOLUTE path, so a relative value or a bare word is left
// alone. Matching by suffix rather than by listing the three keys is what
// makes this survive the fourth.
var configuredFilePattern = regexp.MustCompile(`(?m)^\s*[a-z_]*_file\s*:\s*"(/[^"]*)"`)

// configuredFilePaths returns every absolute file path conf names.
//
// It is a separate function so the rule can run against text this tool
// controls, which is what TestConfiguredFilePathsFindsPaths does: a render
// that contains no configuration and a matcher that recognizes none
// produce the same clean result.
func configuredFilePaths(conf string) []string {
	var paths []string
	for _, match := range configuredFilePattern.FindAllStringSubmatch(conf, -1) {
		paths = append(paths, match[1])
	}
	return paths
}

// checkConfiguredFilesAreMounted requires every path named by a
// configuration file a container mounts to fall inside one of that
// container's own mounts.
//
// Scoped to the container rather than the pod, because a mount is a
// property of a container: a second container in the same pod declaring
// the mount would not help the one reading the configuration.
func checkConfiguredFilesAreMounted(profile string, objects []manifest) []finding {
	// Every ConfigMap in the render, by name, so a volume can be followed
	// to the text it projects.
	configMaps := make(map[string]map[string]string)
	for _, obj := range objects {
		if obj.Kind == "ConfigMap" {
			configMaps[obj.Metadata.Name] = obj.Data
		}
	}

	var findings []finding
	for _, obj := range objects {
		if !workloadKinds[obj.Kind] {
			continue
		}
		object := fmt.Sprintf("%s/%s", obj.Kind, obj.Metadata.Name)
		pod := obj.Spec.Template.Spec

		// Which volume each name projects, so a mount resolves to text.
		projected := make(map[string]map[string]string)
		for _, vol := range pod.Volumes {
			if vol.ConfigMap == nil {
				continue
			}
			if data, ok := configMaps[vol.ConfigMap.Name]; ok {
				projected[vol.Name] = data
			}
		}

		all := append(append([]container{}, pod.InitContainers...), pod.Containers...)
		for _, c := range all {
			for _, mount := range c.VolumeMounts {
				data, ok := projected[mount.Name]
				if !ok {
					continue
				}
				for key, conf := range data {
					for _, path := range configuredFilePaths(conf) {
						if mountCovers(c.VolumeMounts, path) {
							continue
						}
						findings = append(findings, finding{
							profile: profile, object: object, container: c.Name,
							message: fmt.Sprintf("the mounted configuration %q names the file %q, which no volumeMount on this container places. This renders and applies cleanly, and the container dies at startup on a file that was never there, so the first thing to notice it would be an install.", key, path),
						})
					}
				}
			}
		}
	}
	return findings
}

// mountCovers reports whether any mount places path.
//
// A prefix test on the path SEPARATOR, not on the string: "/etc/nats/tls"
// must cover "/etc/nats/tls/tls.crt" and must not cover
// "/etc/nats/tlsconfig/tls.crt". An exact match counts too, which is the
// subPath shape the broker's own nats.conf mount uses.
func mountCovers(mounts []volumeMount, path string) bool {
	for _, mount := range mounts {
		if mount.MountPath == "" {
			continue
		}
		if path == mount.MountPath || strings.HasPrefix(path, strings.TrimSuffix(mount.MountPath, "/")+"/") {
			return true
		}
	}
	return false
}

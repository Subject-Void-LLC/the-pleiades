// KubernetesKindRule: the plan-time check that a manifest's kinds are ones
// its cluster serves, by what onboarding discovered there.
package validate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// maxManifestObjects bounds how many objects the rule reads from one
// parameter: past it the rest are left to the method, which reads them
// again at run time.
const maxManifestObjects = 10000

// kindServer is a device that can say which Kubernetes kinds it serves,
// from what onboarding discovered (a kubernetes_cluster).
type kindServer interface {
	// ServedKinds returns the kinds as "group/version Kind", the group
	// versions whose discovery failed, and whether the kinds can be read
	// as all there is.
	ServedKinds() (kinds, partialGroups []string, complete bool)
}

// manifestObject is one object a manifest parameter names.
type manifestObject struct {
	apiVersion, kind string
	body             map[string]any
}

// KubernetesKindRule refuses a manifest object whose kind its cluster does
// not serve at that version, before a server-side apply would fail on it
// halfway through a runbook. It reads only a parameter the method marks
// collection.ParamFormatKubernetesManifest, and refuses only what it can
// know:
//
//   - a cluster with no complete discovery (never onboarded, onboarded
//     against another address, or serving more kinds than a discovery
//     keeps) is not checked;
//   - an object in a group whose discovery failed there, or in a group
//     version an APIService anywhere in the runbook registers, is not
//     checked;
//   - a kind a CustomResourceDefinition anywhere in the runbook defines
//     counts as served, since a runbook installs a CRD and then its
//     resources; like TemplateRule's registers this is existence, not
//     order;
//   - a value that holds a template is checked only where its apiVersion
//     and kind are literal, and one that does not parse until it renders
//     is left to run time.
func KubernetesKindRule(world WorldView) []Finding {
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	manifests := map[string][]manifestObject{}
	defined := map[string]bool{}
	registered := map[string]bool{}
	var findings []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		formats := engine.ParamFormats(task.FQCN)
		keys := make([]string, 0, len(formats))
		for key, format := range formats {
			if format == collection.ParamFormatKubernetesManifest {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, ok := task.Params[key]
			if !ok {
				continue
			}
			objects, unnamed := manifestObjects(value)
			if unnamed > 0 {
				findings = append(findings, Finding{
					RuleName: "kubernetes_kind",
					Node:     id,
					Message:  fmt.Sprintf("task %s, parameter %s: %d object(s) name no apiVersion and kind", taskName(id, task), key, unnamed),
				})
			}
			for _, o := range objects {
				defineKinds(o, defined, registered)
			}
			manifests[id] = append(manifests[id], objects...)
		}
	}

	for _, id := range ids {
		objects := manifests[id]
		if len(objects) == 0 {
			continue
		}
		task := world.DAG.Nodes[id]
		_, devices := world.taskDevices(task)
		for _, dev := range devices {
			server, ok := dev.(kindServer)
			if !ok {
				continue
			}
			kinds, partial, complete := server.ServedKinds()
			if !complete {
				continue
			}
			for _, problem := range unservedKinds(objects, kinds, partial, defined, registered) {
				findings = append(findings, Finding{
					RuleName: "kubernetes_kind",
					Node:     id,
					Device:   dev.ID(),
					Message:  fmt.Sprintf("task %s: cluster %s %s; onboard it again if it serves that now", taskName(id, task), dev.Name(), problem),
				})
			}
		}
	}
	return findings
}

// unservedKinds names each object whose kind the cluster does not serve,
// once per kind, with the versions it does serve that kind at.
func unservedKinds(objects []manifestObject, kinds, partial []string, defined, registered map[string]bool) []string {
	served := make(map[string]bool, len(kinds))
	versionsOf := map[string][]string{}
	for _, k := range kinds {
		served[k] = true
		if gv, kind, ok := strings.Cut(k, " "); ok {
			key := groupOf(gv) + " " + kind
			versionsOf[key] = append(versionsOf[key], gv)
		}
	}
	unknownGroups := map[string]bool{}
	for _, gv := range partial {
		unknownGroups[groupOf(gv)] = true
	}

	var problems []string
	reported := map[string]bool{}
	for _, o := range objects {
		key := o.apiVersion + " " + o.kind
		if served[key] || defined[key] || registered[o.apiVersion] || unknownGroups[groupOf(o.apiVersion)] || reported[key] {
			continue
		}
		reported[key] = true
		if others := versionsOf[groupOf(o.apiVersion)+" "+o.kind]; len(others) > 0 {
			problems = append(problems, fmt.Sprintf("does not serve %s at %s, only at %s", o.kind, o.apiVersion, strings.Join(others, ", ")))
		} else {
			problems = append(problems, fmt.Sprintf("does not serve %s %s", o.apiVersion, o.kind))
		}
	}
	return problems
}

// defineKinds records what o adds to a cluster when it is applied: a
// CustomResourceDefinition's kind at each version it serves, and an
// APIService's group version, whose kinds cannot be known in advance.
func defineKinds(o manifestObject, defined, registered map[string]bool) {
	switch {
	case groupOf(o.apiVersion) == "apiextensions.k8s.io" && o.kind == "CustomResourceDefinition":
		spec, _ := o.body["spec"].(map[string]any)
		group, _ := spec["group"].(string)
		names, _ := spec["names"].(map[string]any)
		kind, _ := names["kind"].(string)
		versions, _ := spec["versions"].([]any)
		for _, v := range versions {
			version, _ := v.(map[string]any)
			name, _ := version["name"].(string)
			if servedFlag, set := version["served"].(bool); name == "" || (set && !servedFlag) {
				continue
			}
			defined[group+"/"+name+" "+kind] = true
		}
	case groupOf(o.apiVersion) == "apiregistration.k8s.io" && o.kind == "APIService":
		spec, _ := o.body["spec"].(map[string]any)
		group, _ := spec["group"].(string)
		version, _ := spec["version"].(string)
		if group != "" && version != "" {
			registered[group+"/"+version] = true
		}
	}
}

// groupOf is an apiVersion's group: "apps" for "apps/v1", "" for the core
// group's "v1".
func groupOf(apiVersion string) string {
	group, _, ok := strings.Cut(apiVersion, "/")
	if !ok {
		return ""
	}
	return group
}

// manifestObjects reads the objects a manifest parameter names: a YAML or
// JSON document stream, one object, or a list of objects, with a v1 List's
// items read as objects of their own. It counts the objects that name no
// literal apiVersion and kind, leaving out any whose apiVersion or kind is
// a template, which only rendering can answer. A string that does not parse
// is left to run time: it may be a template that renders into a manifest.
func manifestObjects(value any) ([]manifestObject, int) {
	var docs []any
	switch v := value.(type) {
	case string:
		dec := yaml.NewDecoder(bytes.NewReader([]byte(v)))
		for len(docs) < maxManifestObjects {
			var doc any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, 0
			}
			if doc != nil {
				docs = append(docs, doc)
			}
		}
	case []any:
		docs = v
	default:
		docs = []any{v}
	}

	var objects []manifestObject
	unnamed := 0
	var visit func(doc any)
	visit = func(doc any) {
		if len(objects)+unnamed >= maxManifestObjects {
			return
		}
		body, ok := doc.(map[string]any)
		if !ok {
			unnamed++
			return
		}
		apiVersion, _ := body["apiVersion"].(string)
		kind, _ := body["kind"].(string)
		switch {
		case engine.HasTemplate(apiVersion) || engine.HasTemplate(kind):
			return
		case apiVersion == "" || kind == "":
			unnamed++
			return
		case apiVersion == "v1" && kind == "List":
			items, _ := body["items"].([]any)
			for _, item := range items {
				visit(item)
			}
			return
		}
		objects = append(objects, manifestObject{apiVersion: apiVersion, kind: kind, body: body})
	}
	for _, doc := range docs {
		visit(doc)
	}
	return objects, unnamed
}

func init() {
	Register(KubernetesKindRule)
}

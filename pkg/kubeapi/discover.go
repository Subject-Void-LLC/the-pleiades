// Discovery: what a cluster reports about itself, which onboarding records
// and validation reads.
package kubeapi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// Bounds on what a discovery keeps. A cluster's answers are untrusted (a
// hostile or broken API server could report anything), and onboarding
// stores what it keeps in the device's record.
const (
	maxKinds    = 5000
	maxRules    = 500
	maxNodes    = 1000
	maxNameSize = 253
	// maxRuleEntries bounds the verbs, and the group-qualified resources,
	// one rule keeps: a rule's groups and resources are listed as their
	// product, which a hostile server could make enormous.
	maxRuleEntries = 64
)

// Discovery is what a cluster reported about itself through one credential.
type Discovery struct {
	// Version is the API server's release, as "v1.37.1+k3s1".
	Version string
	// Kinds are the resources the server serves at every version it
	// serves them, as "group/version Kind" ("autoscaling/v1
	// HorizontalPodAutoscaler" beside "autoscaling/v2
	// HorizontalPodAutoscaler", "v1 Pod"), so a manifest at any served
	// version is known to be served.
	Kinds []string
	// KindsTruncated says the server reported more kinds than a discovery
	// keeps, so a kind missing from Kinds may still be served.
	KindsTruncated bool
	// Rules are what the credential may do in Namespace, as the server's
	// own SelfSubjectRulesReview answers: "verbs resources" per rule.
	Rules []string
	// RulesIncomplete is the review's own flag: the server could not list
	// every rule (an authorizer that cannot enumerate, such as a webhook).
	RulesIncomplete bool
	// Namespace is the namespace the rules are for.
	Namespace string
	// Nodes are the cluster's nodes when the credential may list them.
	Nodes []Node
	// Partial names the API groups whose discovery failed, so the kinds
	// are known to be incomplete rather than read as all there is.
	Partial []string
}

// Node is one node as the cluster reports it.
type Node struct {
	Name             string `json:"name"`
	ContainerRuntime string `json:"container_runtime"`
	MachineID        string `json:"machine_id"`
}

// selfSubjectRulesReviews is the resource a credential asks what it may do.
var selfSubjectRulesReviews = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectrulesreviews"}

// nodes is the cluster's node list.
var nodes = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}

// Discover asks the cluster what it is, what it serves, and what this
// session's credential may do in its namespace.
func (s *Session) Discover(ctx context.Context) (Discovery, error) {
	d := Discovery{Namespace: s.Namespace}
	// Every answer below draws on one byte budget (maxDiscoveryBytes).
	disc, dyn, err := boundedClients(s.Config, maxDiscoveryBytes)
	if err != nil {
		return Discovery{}, err
	}
	v, err := disc.ServerVersionWithContext(ctx)
	if err != nil {
		return Discovery{}, fmt.Errorf("kubeapi: asking the server's version: %w", Explain(err))
	}
	d.Version = bounded(v.GitVersion)

	_, lists, err := disc.ServerGroupsAndResourcesWithContext(ctx)
	var partial *discovery.ErrGroupDiscoveryFailed
	switch {
	case errors.As(err, &partial):
		for gv := range partial.Groups {
			d.Partial = append(d.Partial, gv.String())
		}
		sort.Strings(d.Partial)
	case err != nil:
		return Discovery{}, fmt.Errorf("kubeapi: asking what the server serves: %w", Explain(err))
	}
	d.Kinds, d.KindsTruncated = kindsOf(lists)

	if d.Rules, d.RulesIncomplete, err = s.rules(ctx, dyn); err != nil {
		return Discovery{}, err
	}
	if d.Nodes, err = nodesOf(ctx, dyn); err != nil {
		return Discovery{}, err
	}
	return d, nil
}

// kindsOf renders the served resources as "group/version Kind", once each,
// sorted, leaving out subresources ("pods/log"), which are not kinds, and
// reports whether it kept fewer than the server named.
func kindsOf(lists []*metav1.APIResourceList) ([]string, bool) {
	seen := map[string]bool{}
	for _, list := range lists {
		if list == nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") || r.Kind == "" {
				continue
			}
			seen[bounded(list.GroupVersion)+" "+bounded(r.Kind)] = true
		}
	}
	kinds := make([]string, 0, len(seen))
	for k := range seen {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if len(kinds) > maxKinds {
		return kinds[:maxKinds], true
	}
	return kinds, false
}

// rules asks the server, through a SelfSubjectRulesReview, what this
// session's credential may do in its namespace.
func (s *Session) rules(ctx context.Context, dyn dynamic.Interface) ([]string, bool, error) {
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SelfSubjectRulesReview",
		"spec":       map[string]any{"namespace": s.Namespace},
	}}
	got, err := dyn.Resource(selfSubjectRulesReviews).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("kubeapi: asking what this credential may do in namespace %q: %w", s.Namespace, Explain(err))
	}
	incomplete, _, _ := unstructured.NestedBool(got.Object, "status", "incomplete")
	raw, _, _ := unstructured.NestedSlice(got.Object, "status", "resourceRules")
	var rules []string
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		verbs, _, _ := unstructured.NestedStringSlice(m, "verbs")
		groups, _, _ := unstructured.NestedStringSlice(m, "apiGroups")
		resources, _, _ := unstructured.NestedStringSlice(m, "resources")
		text, cut := ruleText(verbs, groups, resources)
		rules = append(rules, text)
		incomplete = incomplete || cut
		if len(rules) == maxRules {
			incomplete = true
			break
		}
	}
	sort.Strings(rules)
	return rules, incomplete, nil
}

// ruleText renders one rule as "verbs on group/resources", keeping at most
// maxRuleEntries of each, and reports whether it left any out.
func ruleText(verbs, groups, resources []string) (string, bool) {
	cut := false
	if len(verbs) > maxRuleEntries {
		verbs, cut = verbs[:maxRuleEntries], true
	}
	qualified := make([]string, 0, min(len(resources)*len(groupsOrCore(groups)), maxRuleEntries))
each:
	for _, r := range resources {
		for _, g := range groupsOrCore(groups) {
			if len(qualified) == maxRuleEntries {
				cut = true
				break each
			}
			if g == "" {
				qualified = append(qualified, bounded(r))
			} else {
				qualified = append(qualified, bounded(g)+"/"+bounded(r))
			}
		}
	}
	return strings.Join(boundedAll(verbs), ",") + " on " + strings.Join(qualified, ","), cut
}

// groupsOrCore is groups, or the core group alone when the rule names none.
func groupsOrCore(groups []string) []string {
	if len(groups) == 0 {
		return []string{""}
	}
	return groups
}

// nodesOf lists the cluster's nodes, or none when the credential may not:
// a namespace's credential usually cannot, and that is not a failure.
func nodesOf(ctx context.Context, dyn dynamic.Interface) ([]Node, error) {
	list, err := dyn.Resource(nodes).List(ctx, metav1.ListOptions{Limit: maxNodes})
	if apierrors.IsForbidden(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kubeapi: listing nodes: %w", Explain(err))
	}
	out := make([]Node, 0, len(list.Items))
	for _, item := range list.Items {
		runtime, _, _ := unstructured.NestedString(item.Object, "status", "nodeInfo", "containerRuntimeVersion")
		machine, _, _ := unstructured.NestedString(item.Object, "status", "nodeInfo", "machineID")
		out = append(out, Node{Name: bounded(item.GetName()), ContainerRuntime: bounded(runtime), MachineID: bounded(machine)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// bounded cuts a name a server reported to the longest any Kubernetes name
// may be, so a hostile answer cannot inflate a device's record.
func bounded(s string) string {
	if len(s) > maxNameSize {
		return s[:maxNameSize]
	}
	return s
}

// boundedAll is bounded for each string.
func boundedAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = bounded(s)
	}
	return out
}

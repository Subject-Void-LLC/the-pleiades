// The lab methods: what is deployed, deploying and destroying a lab, and
// changing its nodes together.
package clab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// maxTopology bounds a topology file read from the project.
const maxTopology = 1 << 20

// fqcnDestroy is the undo of a deploy.
const fqcnDestroy = "virt.clab.lab.destroy"

// labPath is a lab's route, its name escaped as one path segment.
func labPath(lab string, rest ...string) string {
	p := "/api/v1/labs/" + url.PathEscape(lab)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// labList lists the deployed labs and their nodes.
func labList(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	var labs map[string][]nodeInfo
	if err := s.call(ctx, http.MethodGet, "/api/v1/labs", nil, nil, &labs); err != nil {
		return false, err
	}
	names := make([]string, 0, len(labs))
	nodes := map[string]any{}
	for name, ns := range labs {
		names = append(names, name)
		nodes[name] = nodeMaps(ns)
	}
	sort.Strings(names)
	return false, s.stats(map[string]any{"labs": names, "nodes": nodes})
}

// labInfo inspects one lab; one that is not deployed is an answer, not a
// failure.
func labInfo(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab := a.ident("lab")
	nodes, exists, err := s.inspect(ctx, lab)
	if err != nil {
		return false, err
	}
	out := map[string]any{"exists": exists}
	if exists {
		out["nodes"], out["node_count"] = nodeMaps(nodes), len(nodes)
	}
	return false, s.stats(out)
}

// labRead is a lab route that only reads and returns what the server said
// under one result name.
func labRead(path, resultKey, nodeQuery string) handler {
	return func(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
		lab := a.ident("lab")
		q := url.Values{}
		if nodeQuery != "" {
			if n := a.ident("node"); n != "" {
				q.Set(nodeQuery, n)
			}
		}
		var out any
		if path == "topology/yaml" {
			var text string
			if err := s.call(ctx, http.MethodGet, labPath(lab, "topology", "yaml"), nil, nil, &text); err != nil {
				return false, err
			}
			return false, s.stat(resultKey, text)
		}
		if err := s.call(ctx, http.MethodGet, labPath(lab, path), q, nil, &out); err != nil {
			return false, err
		}
		return false, s.stat(resultKey, out)
	}
}

// labValidate asks containerlab to parse the lab's topology. A topology that
// is not valid fails the task, after the answer is recorded.
func labValidate(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab := a.ident("lab")
	var out struct {
		Valid bool   `json:"valid"`
		Nodes int    `json:"nodes"`
		Links int    `json:"links"`
		Error string `json:"error"`
		Path  string `json:"path"`
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "validate"), query("path", a.str("path")), nil, &out); err != nil {
		return false, err
	}
	stats := map[string]any{"valid": out.Valid, "nodes": out.Nodes, "links": out.Links}
	if out.Error != "" {
		stats["error"] = out.Error
	}
	if err := s.stats(stats); err != nil {
		return false, err
	}
	if !out.Valid {
		return false, fmt.Errorf("%s: the topology of lab %q is not valid: %s", s.fqcn, lab, out.Error)
	}
	return false, nil
}

// topologyBody builds the request body that names the topology to deploy:
// the mapping given, a project file read as one, or a repository URL. Exactly
// one is allowed. A topology given as content must name the lab it deploys,
// since the server deploys the name the topology holds.
func (s *session) topologyBody(a *args, lab string) (map[string]any, error) {
	content := a.dict("topology")
	file := a.str("topology_file")
	source := a.str("source_url")
	given := 0
	for _, set := range []bool{content != nil, file != "", source != ""} {
		if set {
			given++
		}
	}
	if a.err != nil {
		return nil, a.err
	}
	if given != 1 {
		return nil, fmt.Errorf("%s: set exactly one of topology, topology_file and source_url", s.fqcn)
	}
	if source != "" {
		return map[string]any{"topologySourceUrl": source}, nil
	}
	if file != "" {
		files, ok := s.rc.(sdk.ProjectFiles)
		if !ok {
			return nil, fmt.Errorf("%s: topology_file: %w", s.fqcn, sdk.ErrNoProjectFiles)
		}
		b, err := files.ReadProjectFile(file, maxTopology)
		if err != nil {
			return nil, fmt.Errorf("%s: topology_file: %w", s.fqcn, err)
		}
		if err := yaml.Unmarshal(b, &content); err != nil || content == nil {
			return nil, fmt.Errorf("%s: topology_file %s is not a YAML mapping", s.fqcn, file)
		}
	}
	if name, _ := content["name"].(string); name != lab {
		return nil, fmt.Errorf("%s: the topology is named %q and lab is %q: the server deploys the name the topology holds, so they must match", s.fqcn, name, lab)
	}
	return map[string]any{"topologyContent": content}, nil
}

// deployQuery is the options a deploy and a redeploy share.
func deployQuery(a *args) url.Values {
	q := query("network", a.str("network"), "ipv4Subnet", a.str("ipv4_subnet"), "ipv6Subnet", a.str("ipv6_subnet"))
	if n, ok := a.integer("max_workers"); ok {
		q.Set("maxWorkers", strconv.Itoa(n))
	}
	if a.flag("skip_post_deploy") {
		q.Set("skipPostDeploy", "true")
	}
	return q
}

// labDeploy deploys a lab unless it is deployed. Its undo, destroying it, is
// recorded before the call when this task is the one making the lab, and kept
// if the call fails, since a deploy that failed partway leaves containers and
// destroying an absent lab is a no-op.
func labDeploy(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	lab := a.ident("lab")
	reconfigure := a.flag("reconfigure")
	body, err := s.topologyBody(a, lab)
	if err != nil {
		return false, err
	}
	q := deployQuery(a)
	if reconfigure {
		q.Set("reconfigure", "true")
	}
	if a.err != nil {
		return false, a.err
	}
	existing, exists, err := s.inspect(ctx, lab)
	if err != nil {
		return false, err
	}
	if exists && !reconfigure {
		return false, s.stats(map[string]any{"exists": true, "created": false, "nodes": nodeMaps(existing)})
	}
	if check {
		return true, s.stats(map[string]any{"exists": true, "created": !exists, "nodes": []any{}})
	}
	if !exists {
		if err := s.undo(sdk.Inverse{FQCN: fqcnDestroy, Params: map[string]any{"lab": lab, "cleanup": true},
			Description: "Destroys the lab " + lab + ", which this run deployed.", KeepOnFailure: true}); err != nil {
			return false, err
		}
	}
	if err := s.call(ctx, http.MethodPost, "/api/v1/labs", q, body, nil); err != nil {
		return false, err
	}
	nodes, now, err := s.inspect(ctx, lab)
	if err != nil {
		return false, err
	}
	if !now {
		return true, fmt.Errorf("%s: the server accepted the deploy and lab %q is not deployed: does the topology's name match it?", s.fqcn, lab)
	}
	return true, s.stats(map[string]any{"exists": true, "created": !exists, "nodes": nodeMaps(nodes)})
}

// labDestroy destroys a lab unless it is not deployed, and says after that it
// is gone.
func labDestroy(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	lab := a.ident("lab")
	q := url.Values{}
	for param, key := range map[string]string{"cleanup": "cleanup", "purge_lab_dir": "purgeLabDir", "graceful": "graceful", "keep_mgmt_net": "keepMgmtNet"} {
		if a.flag(param) {
			q.Set(key, "true")
		}
	}
	if a.err != nil {
		return false, a.err
	}
	_, exists, err := s.inspect(ctx, lab)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, s.stat("exists", false)
	}
	if check {
		return true, s.stat("exists", false)
	}
	if err := s.call(ctx, http.MethodDelete, labPath(lab), q, nil, nil); err != nil {
		return false, err
	}
	if _, still, err := s.inspect(ctx, lab); err != nil {
		return true, err
	} else if still {
		return true, fmt.Errorf("%s: the server accepted the destroy and lab %q is still deployed", s.fqcn, lab)
	}
	return true, s.stat("exists", false)
}

// labRedeploy replaces a lab with a new one from a topology.
func labRedeploy(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab := a.ident("lab")
	body, err := s.topologyBody(a, lab)
	if err != nil {
		return false, err
	}
	q := deployQuery(a)
	for param, key := range map[string]string{"cleanup": "cleanup", "graceful": "graceful"} {
		if a.flag(param) {
			q.Set(key, "true")
		}
	}
	if a.err != nil {
		return false, a.err
	}
	if err := s.call(ctx, http.MethodPut, labPath(lab), q, body, nil); err != nil {
		return false, err
	}
	nodes, now, err := s.inspect(ctx, lab)
	if err != nil {
		return true, err
	}
	if !now {
		return true, fmt.Errorf("%s: the server accepted the redeploy and lab %q is not deployed: does the topology's name match it?", s.fqcn, lab)
	}
	return true, s.stat("nodes", nodeMaps(nodes))
}

// labSave saves each node's running configuration.
func labSave(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab := a.ident("lab")
	filter := a.ident("node_filter")
	if a.err != nil {
		return false, a.err
	}
	if filter != "" {
		n, err := s.node(ctx, lab, filter)
		if err != nil {
			return false, err
		}
		filter = n.Name
	}
	var out struct {
		Message string `json:"message"`
		Output  string `json:"output"`
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "save"), query("nodeFilter", filter), nil, &out); err != nil {
		return false, err
	}
	return true, s.stats(map[string]any{"message": out.Message, "output": out.Output})
}

// labState is lab-wide start, stop and restart: a node is acted on by the
// server whatever its state, so the nodes are read first and the lab is
// reported unchanged when nothing would move.
func labState(verb string) handler {
	return func(ctx context.Context, s *session, a *args, check bool) (bool, error) {
		lab := a.ident("lab")
		if a.err != nil {
			return false, a.err
		}
		nodes, exists, err := s.inspect(ctx, lab)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("%s: lab %q is not deployed", s.fqcn, lab)
		}
		moves := verb == "restart"
		for _, n := range nodes {
			if (verb == "start" && n.State != "running") || (verb == "stop" && n.State == "running") {
				moves = true
			}
		}
		if !moves || check {
			return moves, s.stat("nodes", nodeMaps(nodes))
		}
		if err := s.call(ctx, http.MethodPost, labPath(lab, verb), nil, nil, nil); err != nil {
			return false, err
		}
		after, _, err := s.inspect(ctx, lab)
		if err != nil {
			return true, err
		}
		return true, s.stat("nodes", nodeMaps(after))
	}
}

// labApply applies the lab's on-disk topology by the difference. With dry_run,
// and in a check, it reports the difference and changes nothing, so its check
// is the server's own prediction.
func labApply(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	lab := a.ident("lab")
	dry := a.flag("dry_run") || check
	q := query("path", a.str("path"))
	if dry {
		q.Set("dryRun", "true")
	}
	if a.flag("skip_post_deploy") {
		q.Set("skipPostDeploy", "true")
	}
	if a.err != nil {
		return false, a.err
	}
	var out struct {
		AddedNodes       []string `json:"addedNodes"`
		DeletedNodes     []string `json:"deletedNodes"`
		RecreatedNodes   []string `json:"recreatedNodes"`
		StartedNodes     []string `json:"startedNodes"`
		RestartedNodes   []string `json:"restartedNodes"`
		AddedLinks       []string `json:"addedLinks"`
		DeletedEndpoints []string `json:"deletedEndpoints"`
		DeployedLab      bool     `json:"deployedLab"`
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "apply"), q, nil, &out); err != nil {
		return false, err
	}
	nonNil := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	moves := out.DeployedLab || len(out.AddedNodes)+len(out.DeletedNodes)+len(out.RecreatedNodes)+len(out.StartedNodes)+len(out.RestartedNodes)+len(out.AddedLinks)+len(out.DeletedEndpoints) > 0
	err := s.stats(map[string]any{"added_nodes": nonNil(out.AddedNodes), "deleted_nodes": nonNil(out.DeletedNodes), "recreated_nodes": nonNil(out.RecreatedNodes),
		"started_nodes": nonNil(out.StartedNodes), "restarted_nodes": nonNil(out.RestartedNodes), "added_links": nonNil(out.AddedLinks),
		"deleted_endpoints": nonNil(out.DeletedEndpoints), "dry_run": dry})
	// A dry run changes nothing, however much it reports.
	return moves && !dry || (check && moves), err
}

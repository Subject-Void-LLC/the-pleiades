// container.image.prune: the newest tags of each repository matching a
// pattern kept, ordered as versions, and the rest removed, never an image a
// container uses.
package image

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	fqcnPrune = "container.image.prune"

	paramRepositories = "repositories"
	paramKeepNewest   = "keep_newest"

	statKept      = "kept"
	statKeptInUse = "kept_in_use"

	maxKeepNewest = 1000
)

func init() {
	register(fqcnPrune, containerengine.MethodManifest(collection.Reversibility{
		Reversible: false,
		Notes:      "A pruned tag, and an image whose last tag it was, is gone from the engine; container.image.pull brings an image back from its registry.",
	}, pruneDoc()), prune)
}

// tagged is one tag of a repository on the engine.
type tagged struct {
	ref string // the familiar reference, repo:tag
	tag string
	id  string
}

// prunePlan is what a prune keeps and removes, by familiar reference.
type prunePlan struct {
	keep, keepInUse, remove []string
}

// planPrune decides, for each repository whose familiar name matches
// pattern, which tags are its keep newest (ordered as versions) and which
// go, keeping every tag of an image a container uses.
func planPrune(byRepo map[string][]tagged, pattern string, keep int, used map[string][]string) prunePlan {
	var plan prunePlan
	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		if ok, _ := path.Match(pattern, repo); ok {
			repos = append(repos, repo)
		}
	}
	sort.Strings(repos)
	for _, repo := range repos {
		tags := byRepo[repo]
		// Newest first.
		slices.SortFunc(tags, func(a, b tagged) int { return compareVersions(b.tag, a.tag) })
		// A tag in use is kept apart and takes no keep slot: keep_newest
		// counts the tags a prune could otherwise have removed.
		kept := 0
		for _, t := range tags {
			switch {
			case len(used[t.id]) > 0:
				plan.keepInUse = append(plan.keepInUse, t.ref)
			case kept < keep:
				plan.keep = append(plan.keep, t.ref)
				kept++
			default:
				plan.remove = append(plan.remove, t.ref)
			}
		}
	}
	return plan
}

// prune is container.image.prune's one body.
func prune(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnPrune)
	pattern, err := sdk.RequiredStringParam(params, paramRepositories)
	if err != nil {
		return fail(err)
	}
	if _, err := path.Match(pattern, ""); err != nil || strings.ContainsAny(pattern, " \t\n\r\x00") {
		return fail(fmt.Errorf("%s: %q is not a pattern", paramRepositories, pattern))
	}
	keep, set, err := sdk.IntParam(params, paramKeepNewest)
	if err != nil {
		return fail(err)
	}
	if !set {
		return fail(fmt.Errorf("%s is required", paramKeepNewest))
	}
	if keep < 0 || keep > maxKeepNewest {
		return fail(fmt.Errorf("%s: %d is not from 0 to %d", paramKeepNewest, keep, maxKeepNewest))
	}
	s, err := open(ctx, rc, device, params, fqcnPrune,
		containerengine.OpImageList, containerengine.OpContainerList, containerengine.OpImageRemove)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	list, err := s.API.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return fail(fmt.Errorf("listing images: %w", err))
	}
	byRepo := map[string][]tagged{}
	for _, img := range list.Items {
		for _, rt := range img.RepoTags {
			named, err := reference.ParseNormalizedNamed(rt)
			if err != nil {
				continue // "<none>:<none>" and anything else that is not a tag
			}
			t, ok := named.(reference.NamedTagged)
			if !ok {
				continue
			}
			repo := reference.FamiliarName(named)
			byRepo[repo] = append(byRepo[repo], tagged{ref: reference.FamiliarString(named), tag: t.Tag(), id: img.ID})
		}
	}
	used, err := containersUsing(ctx, s)
	if err != nil {
		return fail(err)
	}
	plan := planPrune(byRepo, pattern, keep, used)
	if err := recordStats(rc, map[string]any{
		statKept: nonNil(plan.keep), statKeptInUse: nonNil(plan.keepInUse), statRemoved: nonNil(plan.remove),
	}); err != nil {
		return collection.Result{}, err
	}
	if len(plan.remove) == 0 {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	var removed []string
	for _, ref := range plan.remove {
		// Never forced: a container that started using the image since the
		// list was read makes the engine refuse, and the prune stops there.
		if _, err := s.API.ImageRemove(ctx, ref, client.ImageRemoveOptions{PruneChildren: true}); err != nil {
			_ = rc.SetStat(statRemoved, nonNil(removed))
			return fail(fmt.Errorf("removing %s, after removing %d other tags: %w", ref, len(removed), err))
		}
		removed = append(removed, ref)
	}
	return collection.Result{Changed: true}, rc.SetStat(statRemoved, removed)
}

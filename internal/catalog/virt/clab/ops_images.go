// The image methods: what the server's runtime holds, pulling an image and
// removing one, each only when it would change something.
package clab

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// image is one image in the server's runtime.
type image struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"shortId"`
	RepoTags []string `json:"repoTags"`
	Size     string   `json:"size"`
	Created  string   `json:"createdAt"`
}

// images lists the server's images.
func (s *session) images(ctx context.Context) (string, []image, error) {
	var out struct {
		Runtime string  `json:"runtime"`
		Images  []image `json:"images"`
	}
	if err := s.call(ctx, http.MethodGet, "/api/v1/images", nil, nil, &out); err != nil {
		return "", nil, err
	}
	return out.Runtime, out.Images, nil
}

// has reports whether an image is held under reference, by tag or by id.
func has(list []image, reference string) bool {
	for _, im := range list {
		if im.ID == reference || im.ShortID == reference {
			return true
		}
		for _, t := range im.RepoTags {
			if t == reference {
				return true
			}
		}
	}
	return false
}

// imageList lists the images.
func imageList(ctx context.Context, s *session, _ *args, _ bool) (bool, error) {
	runtime, list, err := s.images(ctx)
	if err != nil {
		return false, err
	}
	out := make([]any, 0, len(list))
	for _, im := range list {
		tags := im.RepoTags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, map[string]any{"id": im.ID, "short_id": im.ShortID, "repo_tags": tags, "size": im.Size, "created_at": im.Created})
	}
	return false, s.stats(map[string]any{"runtime": runtime, "images": out})
}

// imageAction is an image pull or removal's answer.
type imageAction struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Output  string `json:"output"`
}

// imagePull pulls an image unless it is there.
func imagePull(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	ref := a.image("image")
	if a.err != nil {
		return false, a.err
	}
	_, list, err := s.images(ctx)
	if err != nil {
		return false, err
	}
	if has(list, ref) {
		return false, s.stat("present", true)
	}
	if check {
		return true, s.stat("present", true)
	}
	if err := s.undo(sdk.Inverse{FQCN: "virt.clab.image.remove", Params: map[string]any{"image": ref},
		Description: "Removes the image " + ref + ", which this run pulled.", KeepOnFailure: true}); err != nil {
		return false, err
	}
	var out imageAction
	if err := s.call(ctx, http.MethodPost, "/api/v1/images/pull", nil, map[string]string{"image": ref}, &out); err != nil {
		return false, err
	}
	if !out.Success {
		return true, fmt.Errorf("%s: the server could not pull %s: %s", s.fqcn, ref, firstText(out.Message, out.Output))
	}
	return true, s.stat("present", true)
}

// imageRemove removes an image if it is there.
func imageRemove(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	ref := a.image("image")
	force := a.flag("force")
	if a.err != nil {
		return false, a.err
	}
	_, list, err := s.images(ctx)
	if err != nil {
		return false, err
	}
	if !has(list, ref) {
		return false, s.stat("present", false)
	}
	if check {
		return true, s.stat("present", false)
	}
	if err := s.undo(sdk.Inverse{FQCN: "virt.clab.image.pull", Params: map[string]any{"image": ref}, Partial: true,
		Description: "Pulls " + ref + " again: the tag comes back, though the layers may be a newer build.", KeepOnFailure: true}); err != nil {
		return false, err
	}
	q := query("reference", ref)
	if force {
		q.Set("force", "true")
	}
	var out imageAction
	if err := s.call(ctx, http.MethodDelete, "/api/v1/images", q, nil, &out); err != nil {
		return false, err
	}
	if !out.Success {
		return true, fmt.Errorf("%s: the server could not remove %s: %s", s.fqcn, ref, firstText(out.Message, out.Output))
	}
	return true, s.stat("present", false)
}

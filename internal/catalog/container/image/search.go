// container.image.search: a registry's search, read through the engine;
// its check is its run.
package image

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	register(fqcnSearch, containerengine.MethodManifest(readOnly, searchDoc()), search)
}

// search is container.image.search's body: the registry's search, asked
// through the engine, anonymously.
func search(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, error) {
	fail := failer(fqcnSearch)
	term, err := sdk.RequiredStringParam(params, paramTerm)
	if err != nil {
		return fail(err)
	}
	if len(term) > maxTermLength || strings.IndexFunc(term, unicode.IsControl) >= 0 || term[0] == '-' {
		return fail(fmt.Errorf("%s must be at most %d characters with no control character, and must not begin with \"-\"", paramTerm, maxTermLength))
	}
	limit, set, err := sdk.IntParam(params, paramLimit)
	if err != nil {
		return fail(err)
	}
	if !set {
		limit = defaultSearchLimit
	}
	if limit < 1 || limit > maxSearchLimit {
		return fail(fmt.Errorf("%s: %d is not from 1 to %d", paramLimit, limit, maxSearchLimit))
	}
	official, err := sdk.BoolParamOr(params, paramOfficial, false)
	if err != nil {
		return fail(err)
	}
	stars, _, err := sdk.IntParam(params, paramStars)
	if err != nil {
		return fail(err)
	}
	if stars < 0 {
		return fail(fmt.Errorf("%s: %d is negative", paramStars, stars))
	}
	filters := client.Filters{}
	if official {
		filters = filters.Add("is-official", "true")
	}
	if stars > 0 {
		filters = filters.Add("stars", fmt.Sprint(stars))
	}
	s, err := open(ctx, rc, device, params, fqcnSearch, containerengine.OpImageSearch)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	res, err := s.API.ImageSearch(ctx, term, client.ImageSearchOptions{Limit: limit, Filters: filters})
	if err != nil {
		return fail(fmt.Errorf("searching for %q: %w", term, err))
	}
	results := []map[string]any{}
	for _, r := range res.Items {
		// A registry's text is a third party's: control characters, which a
		// terminal acts on, are dropped, and its length is bounded.
		results = append(results, map[string]any{
			"name": printable(r.Name, maxTermLength), "description": printable(r.Description, maxDescription),
			"stars": r.StarCount, "official": r.IsOfficial,
		})
	}
	return collection.Result{}, rc.SetStat(statResults, results)
}

// printable is s without control characters, at most max bytes.
func printable(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		// Cut, then drop a rune the cut split.
		s = strings.ToValidUTF8(s[:max], "")
	}
	return s
}

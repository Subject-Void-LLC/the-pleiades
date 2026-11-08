// Package playbook: setup's filter, translated by expansion.
//
// ansible.builtin.setup's filter is a pattern or a list of them over
// Ansible's fact names (ansible-core's AnsibleFactCollector._filter): a fact
// is kept when its whole name matches a pattern by fnmatch, case sensitive;
// a pattern not starting with ansible_, facter or ohai also matches with
// ansible_ in front of it; and an empty pattern keeps every fact.
// facts.gather's filter names native keys (os.*, hardware.*), so a pattern
// copied across matches nothing and fails on every host (FAILURE_PATTERNS
// 967). The pattern text is therefore never translated. Each pattern is
// matched here against the Ansible names pkg/ansiblefacts maps, and the task
// gets the native keys of what matched as an explicit sorted list, with the
// keys a Windows host reads its distribution and swap beside. A pattern that
// matches no mapped name is refused at conversion, the whole filter with it
// (facts.gather refuses a filter matching none of its facts at run time, so a
// filter of that pattern alone would fail on every host); and one matching
// every mapped name means no filter, as * and ansible_* do. A finding names a
// pattern by its place in the filter, never by its text: the report is written
// to be shared, and holds names and positions only (FAILURE_PATTERNS 330).
package playbook

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// setupFilterCode is the code a setup filter this converter cannot expand is
// refused with.
const setupFilterCode Code = "args.value"

// setupFilterRewrite is what the module reference says of setup's filter.
const setupFilterRewrite = "its patterns are matched against the Ansible facts the migration guide's fact table maps, and the task gets the native keys they match, or no filter when they match every one; a written list splits the task by device, a WindowsShellCapable device gathering every fact in a task of its own, since Ansible's Windows setup ignores filter"

// setupFilterNote is the review a setup task raises when its filter is
// written as a list of native keys; a filter keeping every fact converts
// exactly and raises none.
const setupFilterNote = "setup's filter is written as the native keys it matches (%s), gathered where the device is not WindowsShellCapable; Ansible's Windows setup ignores filter, so a WindowsShellCapable device gathers every fact in a task of its own, and a Windows host this platform does not know as WindowsShellCapable (a generic_ssh device onboarding did not grant it) gets the filtered gather"

// unprefixedPatterns are the prefixes after which Ansible tries a pattern
// only as written, never with ansible_ in front.
var unprefixedPatterns = []string{"ansible_", "facter", "ohai"}

// hardwareSubsetKeys are the native keys of facts.gather's hardware subset
// (internal/catalog/facts' gatherHostProbes: memory, processors, uptime), the
// same facts Ansible's hardware collector gives; every other key the mapping
// knows is in min. With gather_subset leaving hardware out, a filter keeping
// only these gathers nothing in Ansible and is refused by facts.gather, which
// refuses a filter matching none of the chosen subsets' facts (and only that:
// a filter mixing min and hardware facts gathers the min ones under min, as
// Ansible does). TestHardwareSubsetKeysAreTheCatalogsHardwareSubset holds
// this list to facts.gather's own planning.
var hardwareSubsetKeys = []string{"hardware.memory_mb", "hardware.swap_mb", "hardware.cpu.sockets",
	"hardware.cpu.cores", "hardware.cpu.threads", "host.uptime_seconds"}

// filterExpansion is what a setup filter expands to.
type filterExpansion struct {
	// keys are the native keys the filter keeps, sorted; nil when it keeps
	// every mapped fact and the task needs no filter.
	keys []string
	// wildcards are the places in the filter (from 1) of the patterns
	// holding * or ?, which in Ansible may also keep facts this platform does
	// not gather.
	wildcards []int
}

// setupAdjust is setup's Adjust: gather_subset as setupSubset writes it,
// then the filter expanded into the native keys it means.
func setupAdjust(params map[string]any) Adjustment {
	// Read before setupSubset rewrites gather_subset as facts.gather's.
	hardware, base := true, false
	if raw, ok := params["gather_subset"]; ok {
		added, removed, _ := parseSubset(raw)
		hardware = subsetHasHardware(added, removed)
		base = !hardware && !subsetHasMin(removed)
	}
	adj := noted(setupSubset(params))
	raw, ok := params["filter"]
	if !ok {
		return adj
	}
	ex, why := expandSetupFilter(raw)
	if why != "" {
		// The subset's review still stands beside the refusal.
		adj.Code, adj.Refusal = setupFilterCode, why
		return adj
	}
	if ex.keys == nil {
		// Every mapped fact, which is what an unfiltered gather reads.
		delete(params, "filter")
		return adj
	}
	switch {
	case base:
		// gather_subset leaves out both min and hardware: Ansible's base facts
		// hold none of the mapped ones, so its filter keeps nothing, while
		// facts.gather, which always gathers min, would keep these.
		adj.Code = setupFilterCode
		adj.Refusal = fmt.Sprintf("setup's filter keeps %s and gather_subset leaves out min and hardware, so Ansible gathers none of them, and facts.gather, which always gathers min, would; gather min or hardware, or drop the filter", strings.Join(ex.keys, ", "))
		return adj
	case !hardware && onlyHardware(ex.keys):
		adj.Code = setupFilterCode
		adj.Refusal = fmt.Sprintf("setup's filter keeps only hardware facts (%s) and gather_subset leaves hardware out, so Ansible gathers none of them and facts.gather refuses the filter; gather hardware or filter a min fact", strings.Join(ex.keys, ", "))
		return adj
	}
	keys := make([]any, len(ex.keys))
	for i, key := range ex.keys {
		keys[i] = key
	}
	params["filter"] = keys
	// Ansible's Windows setup ignores filter, so the task is split by device
	// (facts_split.go).
	adj.Windows = unfiltered(params)
	adj.Notes = append(adj.Notes, fmt.Sprintf(setupFilterNote, strings.Join(ex.keys, ", ")))
	if len(ex.wildcards) > 0 {
		adj.Notes = append(adj.Notes, fmt.Sprintf("%s may also match Ansible facts this platform does not gather, which are not gathered; it keeps %s", wildcardNames(ex.wildcards, len(listOf(raw))), strings.Join(ex.keys, ", ")))
	}
	return adj
}

// expandSetupFilter expands v, setup's filter as the task's mapping left it
// (a list of patterns), against the Ansible names the mapping knows, or says
// why it cannot: a pattern computed at run time, one holding a character
// class or an escape (whose meaning differs between Python's fnmatch and
// Go's path.Match), or one matching no mapped name.
func expandSetupFilter(v any) (filterExpansion, string) {
	list, ok := v.([]any)
	switch {
	case !ok && isTemplateText(v):
		// A template only the run resolves (a native loop's item) reaches
		// here whole, as the native template text.
		return filterExpansion{}, filterAtRunTime
	case !ok:
		return filterExpansion{}, "setup's filter is not a pattern or a list of patterns"
	}
	all := ansiblefacts.AnsibleNames()
	var ex filterExpansion
	var matched []string
	for i, item := range list {
		pattern, ok := item.(string)
		name := patternName(i, len(list))
		switch {
		case !ok:
			return filterExpansion{}, fmt.Sprintf("setup's filter holds %s where it takes a pattern", kindOf(item))
		case hasTemplate(pattern):
			return filterExpansion{}, filterAtRunTime
		case strings.ContainsAny(pattern, `[]\`):
			return filterExpansion{}, fmt.Sprintf("%s holds a character class or an escape, which Python's fnmatch and this converter read differently; write the native keys as facts.gather's filter", name)
		}
		hits := matchFactNames(pattern, all)
		if len(hits) == 0 {
			return filterExpansion{}, fmt.Sprintf("%s matches no fact this platform gathers, so a filter of it alone would fail on every host; the facts it gathers are %s", name, strings.Join(all, ", "))
		}
		if strings.ContainsAny(pattern, "*?") {
			ex.wildcards = append(ex.wildcards, i+1)
		}
		matched = append(matched, hits...)
	}
	slices.Sort(matched)
	matched = slices.Compact(matched)
	if len(list) == 0 || len(matched) == len(all) {
		// An empty list keeps everything in Ansible, and so does a filter
		// matching every mapped name, as far as this platform gathers.
		return filterExpansion{}, ""
	}
	for _, name := range matched {
		ex.keys = append(ex.keys, ansiblefacts.FilterKeys(name)...)
	}
	slices.Sort(ex.keys)
	ex.keys = slices.Compact(ex.keys)
	return ex, ""
}

// filterAtRunTime is the refusal of a filter only the run resolves.
const filterAtRunTime = "setup's filter is computed at run time, so it cannot be written as the native keys it matches; write the native keys as facts.gather's filter"

// isTemplateText reports whether v is text holding a template.
func isTemplateText(v any) bool {
	s, ok := v.(string)
	return ok && hasTemplate(s)
}

// matchFactNames is the names in all that pattern keeps as Ansible's filter
// does: an empty pattern keeps every one; otherwise a name matching the
// whole pattern, or, for a pattern Ansible prefixes, matching it with
// ansible_ in front. path.Match's * and ? are fnmatch's over names, which
// hold no slash; the character classes where the two differ are refused
// before this.
func matchFactNames(pattern string, all []string) []string {
	if pattern == "" {
		return slices.Clone(all)
	}
	prefixed := !hasAnyPrefix(pattern, unprefixedPatterns)
	var hits []string
	for _, name := range all {
		ok, _ := path.Match(pattern, name)
		if !ok && prefixed {
			ok, _ = path.Match("ansible_"+pattern, name)
		}
		if ok {
			hits = append(hits, name)
		}
	}
	return hits
}

// hasAnyPrefix reports whether s starts with any of prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// onlyHardware reports whether every key is in facts.gather's hardware
// subset.
func onlyHardware(keys []string) bool {
	for _, key := range keys {
		if !slices.Contains(hardwareSubsetKeys, key) {
			return false
		}
	}
	return true
}

// patternName names the pattern at index i of a filter of n patterns in a
// finding, by its place and never by its text: "setup's filter pattern" for
// the only one, "pattern 2 of setup's filter" otherwise.
func patternName(i, n int) string {
	if n == 1 {
		return "setup's filter pattern"
	}
	return fmt.Sprintf("pattern %d of setup's filter", i+1)
}

// wildcardNames names, by their places in a filter of n patterns, the
// patterns holding a wildcard.
func wildcardNames(places []int, n int) string {
	if n == 1 {
		return "setup's filter pattern"
	}
	words := make([]string, len(places))
	for i, place := range places {
		words[i] = strconv.Itoa(place)
	}
	if len(words) == 1 {
		return "pattern " + words[0] + " of setup's filter"
	}
	return "patterns " + strings.Join(words, ", ") + " of setup's filter"
}

// listOf is v as setup's filter list, or nil when it is not one.
func listOf(v any) []any {
	list, _ := v.([]any)
	return list
}

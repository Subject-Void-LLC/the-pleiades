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
// matches no mapped name is refused at conversion, where facts.gather would
// refuse the filter at run time; and one matching every mapped name means no
// filter, as * and ansible_* do.
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

// setupFilterNote is the review a setup task giving filter raises, and what
// the module reference says of the argument.
const setupFilterNote = "its patterns are matched against the Ansible facts the migration guide's fact table maps, and the task gets the native keys they match (no filter when they match every one); a Windows host's setup ignores filter and gathers every fact, where this task gathers only those keys"

// unprefixedPatterns are the prefixes after which Ansible tries a pattern
// only as written, never with ansible_ in front.
var unprefixedPatterns = []string{"ansible_", "facter", "ohai"}

// hardwareSubsetKeys are the native keys of facts.gather's hardware subset
// (internal/catalog/facts' gatherHostProbes: memory, processors, uptime), the
// same facts Ansible's hardware collector gives; every other key the mapping
// knows is in min. With gather_subset leaving hardware out, a filter keeping
// only these gathers nothing in Ansible and is refused by facts.gather.
var hardwareSubsetKeys = []string{"hardware.memory_mb", "hardware.swap_mb", "hardware.cpu.sockets",
	"hardware.cpu.cores", "hardware.cpu.threads", "host.uptime_seconds"}

// filterExpansion is what a setup filter expands to.
type filterExpansion struct {
	// keys are the native keys the filter keeps, sorted; nil when it keeps
	// every mapped fact and the task needs no filter.
	keys []string
	// wildcards are the patterns holding * or ?, which in Ansible may also
	// keep facts this platform does not gather.
	wildcards []string
}

// setupAdjust is setup's Adjust: gather_subset as setupSubset writes it,
// then the filter expanded into the native keys it means.
func setupAdjust(params map[string]any) Adjustment {
	adj := noted(setupSubset(params))
	raw, ok := params["filter"]
	if !ok {
		return adj
	}
	ex, why := expandSetupFilter(raw)
	if why != "" {
		return Adjustment{Code: setupFilterCode, Refusal: why}
	}
	if ex.keys == nil {
		// Every mapped fact, which is what an unfiltered gather reads.
		delete(params, "filter")
		return adj
	}
	if subsets, ok := params["gather_subset"].([]any); ok && !slices.Contains(subsets, "hardware") && onlyHardware(ex.keys) {
		return Adjustment{Code: setupFilterCode, Refusal: fmt.Sprintf("setup's filter keeps only hardware facts (%s) and gather_subset leaves hardware out, so Ansible gathers none of them and facts.gather refuses the filter; gather hardware or filter a min fact", strings.Join(ex.keys, ", "))}
	}
	keys := make([]any, len(ex.keys))
	for i, key := range ex.keys {
		keys[i] = key
	}
	params["filter"] = keys
	// The filter's own review (setupFilterNote) says the rest; a wildcard
	// is the one thing it cannot say ahead of the patterns.
	if len(ex.wildcards) > 0 {
		adj.Notes = append(adj.Notes, fmt.Sprintf("setup's filter pattern %s may also match Ansible facts this platform does not gather, which are not gathered; it keeps %s", strings.Join(quoteAll(ex.wildcards), ", "), strings.Join(ex.keys, ", ")))
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
	if !ok {
		return filterExpansion{}, "setup's filter is not a pattern or a list of patterns"
	}
	all := ansiblefacts.AnsibleNames()
	var ex filterExpansion
	var matched []string
	for _, item := range list {
		pattern, ok := item.(string)
		switch {
		case !ok:
			return filterExpansion{}, fmt.Sprintf("setup's filter holds %s where it takes a pattern", kindOf(item))
		case hasTemplate(pattern):
			return filterExpansion{}, "setup's filter is computed at run time, so it cannot be written as the native keys it matches; write the native keys as facts.gather's filter"
		case strings.ContainsAny(pattern, `[]\`):
			return filterExpansion{}, fmt.Sprintf("setup's filter pattern %s holds a character class or an escape, which Python's fnmatch and this converter read differently; write the native keys as facts.gather's filter", strconv.Quote(pattern))
		}
		hits := matchFactNames(pattern, all)
		if len(hits) == 0 {
			return filterExpansion{}, fmt.Sprintf("setup's filter pattern %s matches no fact this platform gathers, so facts.gather would refuse it on every host; the facts it gathers are %s", strconv.Quote(pattern), strings.Join(all, ", "))
		}
		if strings.ContainsAny(pattern, "*?") {
			ex.wildcards = append(ex.wildcards, pattern)
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

// quoteAll quotes each string, so a pattern reads as written in a finding.
func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strconv.Quote(s)
	}
	return out
}

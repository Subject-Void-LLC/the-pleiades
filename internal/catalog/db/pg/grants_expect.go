// db.pg.grants' declared set: what a task says a role should hold, read
// and checked before anything is contacted.
package pg

import (
	"fmt"
	"strings"
)

// expectation is a declared set: privileges by kind and object pattern,
// the roles the role is a member of, and what it may own.
type expectation struct {
	privileges map[string]map[string][]privilege // kind, pattern
	memberOf   []string
	owns       []string
}

// parseExpect reads the expect parameter, refusing a kind, a privilege or
// a pattern it does not know.
func parseExpect(params map[string]any) (expectation, error) {
	raw, ok := params[paramExpect].(map[string]any)
	if !ok {
		return expectation{}, fmt.Errorf("%s is a mapping of database, schemas, tables, sequences, functions, columns, member_of and owns", paramExpect)
	}
	e := expectation{privileges: map[string]map[string][]privilege{}}
	for key, value := range raw {
		switch key {
		case "member_of", "owns":
			list, err := stringList(value)
			if err != nil {
				return expectation{}, fmt.Errorf("%s.%s: %w", paramExpect, key, err)
			}
			if key == "member_of" {
				e.memberOf = list
			} else {
				e.owns = list
			}
		case "database":
			list, err := stringList(value)
			if err != nil {
				return expectation{}, fmt.Errorf("%s.database: %w", paramExpect, err)
			}
			privs, err := parsePrivileges(key, "", list)
			if err != nil {
				return expectation{}, err
			}
			e.privileges[key] = map[string][]privilege{"": privs}
		default:
			if _, known := grantKinds[key]; !known {
				return expectation{}, fmt.Errorf("%s.%s is not a kind this compares (database, schemas, tables, sequences, functions, columns, member_of, owns)", paramExpect, key)
			}
			byPattern, ok := value.(map[string]any)
			if !ok {
				return expectation{}, fmt.Errorf("%s.%s is a mapping of object (or schema.*) to privileges", paramExpect, key)
			}
			e.privileges[key] = map[string][]privilege{}
			for pattern, v := range byPattern {
				list, err := stringList(v)
				if err != nil {
					return expectation{}, fmt.Errorf("%s.%s[%s]: %w", paramExpect, key, pattern, err)
				}
				if err := checkPattern(key, pattern); err != nil {
					return expectation{}, err
				}
				privs, err := parsePrivileges(key, pattern, list)
				if err != nil {
					return expectation{}, err
				}
				e.privileges[key][pattern] = privs
			}
		}
	}
	return e, nil
}

// checkPattern refuses an object pattern the kind cannot have.
func checkPattern(kind, pattern string) error {
	parts := strings.Count(strings.SplitN(pattern, "(", 2)[0], ".")
	want := map[string]int{"schemas": 0, "tables": 1, "sequences": 1, "functions": 1, "columns": 2}[kind]
	if pattern == "" || parts != want || (kind == "schemas" && strings.Contains(pattern, "*")) || (kind == "columns" && strings.Contains(pattern, "*")) {
		return fmt.Errorf("%s.%s: %q is not a %s (or schema.*)", paramExpect, kind, pattern, singular[kind])
	}
	if strings.Contains(pattern, "*") && !strings.HasSuffix(pattern, ".*") {
		return fmt.Errorf("%s.%s: %q: only schema.* may hold *", paramExpect, kind, pattern)
	}
	return nil
}

// parsePrivileges reads privilege names, upper-cased, with ALL standing
// for every privilege the kind has and WITH GRANT OPTION kept.
func parsePrivileges(kind, object string, names []string) ([]privilege, error) {
	var out []privilege
	for _, raw := range names {
		name := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
		grantable := strings.HasSuffix(name, " WITH GRANT OPTION")
		name = strings.TrimSuffix(name, " WITH GRANT OPTION")
		if name == "TEMP" {
			name = "TEMPORARY"
		}
		all := grantKinds[kind]
		if name == "ALL" || name == "ALL PRIVILEGES" {
			for _, p := range all {
				out = append(out, privilege{kind: kind, object: object, name: p, grantable: grantable})
			}
			continue
		}
		known := false
		for _, p := range all {
			known = known || p == name
		}
		if !known {
			return nil, fmt.Errorf("%s.%s: %q is not a privilege a %s has (%s)", paramExpect, kind, raw, singular[kind], strings.Join(all, ", "))
		}
		out = append(out, privilege{kind: kind, object: object, name: name, grantable: grantable})
	}
	return out, nil
}

// stringList reads a list of strings.
func stringList(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("it is a list of names")
	}
	out := make([]string, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("entry %d is not a name", i)
		}
		out[i] = s
	}
	return out, nil
}

// Package osvreport reads OSV-Scanner's JSON report (`--format json`, as
// version 2 writes it) into findings: one per package per group of aliased
// advisories, with the group's severity as a rating and the versions that
// fix it. It reads only what a gate needs and refuses a report it cannot
// read whole, since a half-read report is a scan that missed findings.
package osvreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// MaxReportBytes bounds a report.
const MaxReportBytes = 64 << 20

// MaxFindings bounds how many findings one report yields.
const MaxFindings = 100000

// Rating is a severity, ordered.
type Rating int

// The ratings, from a CVSS base score: low is above 0 and below 4.0,
// medium from 4.0, high from 7.0, critical from 9.0. Unknown has no
// score; a gate decides what it weighs.
const (
	Unknown Rating = iota
	Low
	Medium
	High
	Critical
)

// String is the rating's name.
func (r Rating) String() string {
	return [...]string{"unknown", "low", "medium", "high", "critical"}[r]
}

// RatingOf rates a CVSS base score as OSV-Scanner writes it ("9.8");
// anything that is not a score from 0 to 10 is Unknown.
func RatingOf(score string) Rating {
	f, err := strconv.ParseFloat(strings.TrimSpace(score), 64)
	switch {
	case err != nil || f < 0 || f > 10:
		return Unknown
	case f >= 9:
		return Critical
	case f >= 7:
		return High
	case f >= 4:
		return Medium
	case f > 0:
		return Low
	}
	return Unknown
}

// ParseRating reads a rating's name.
func ParseRating(name string) (Rating, error) {
	for r := Unknown; r <= Critical; r++ {
		if r.String() == name {
			return r, nil
		}
	}
	return Unknown, fmt.Errorf("%q is not a severity (low, medium, high, critical)", name)
}

// Finding is one package's exposure to one group of aliased advisories.
type Finding struct {
	// Source is the path the scanner read the package from, as the
	// scanner saw it.
	Source string
	// Ecosystem, Package and Version name what is exposed.
	Ecosystem, Package, Version string
	// ID is the group's first advisory id; IDs is every id in the group,
	// and Aliases every alias, sorted.
	ID      string
	IDs     []string
	Aliases []string
	// Score is the group's highest CVSS base score, as written; Rating
	// rates it.
	Score  string
	Rating Rating
	// Fixed is every version an advisory in the group says fixes it in
	// the package's ecosystem; none when no fix is known.
	Fixed []string
	// Summary is the first advisory's summary.
	Summary string
}

// Names is every id and alias the finding is known by.
func (f Finding) Names() []string {
	return append(append([]string{}, f.IDs...), f.Aliases...)
}

// report is the part of the JSON this reads.
type report struct {
	Results *[]struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Groups []struct {
				IDs         []string `json:"ids"`
				Aliases     []string `json:"aliases"`
				MaxSeverity string   `json:"max_severity"`
			} `json:"groups"`
			Vulnerabilities []struct {
				ID       string `json:"id"`
				Summary  string `json:"summary"`
				Affected []struct {
					Package struct {
						Ecosystem string `json:"ecosystem"`
						Name      string `json:"name"`
					} `json:"package"`
					Ranges []struct {
						Events []map[string]string `json:"events"`
					} `json:"ranges"`
				} `json:"affected"`
			} `json:"vulnerabilities"`
		} `json:"packages"`
	} `json:"results"`
}

// Parse reads a report. One with no results field is not OSV-Scanner's
// JSON and is refused; one with an empty list has no findings.
func Parse(data []byte) ([]Finding, error) {
	if len(data) > MaxReportBytes {
		return nil, fmt.Errorf("the report is over %d bytes", MaxReportBytes)
	}
	var r report
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("the report is not OSV-Scanner's JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the report holds more than one JSON document")
	}
	if r.Results == nil {
		return nil, errors.New("the report has no results; it is not OSV-Scanner's JSON")
	}
	var out []Finding
	for _, result := range *r.Results {
		for _, p := range result.Packages {
			if p.Package.Name == "" {
				return nil, fmt.Errorf("%s: a package with no name", result.Source.Path)
			}
			for _, g := range p.Groups {
				if len(g.IDs) == 0 || contains(g.IDs, "") {
					return nil, fmt.Errorf("%s: %s has a group of advisories with no id", result.Source.Path, p.Package.Name)
				}
				f := Finding{
					Source: result.Source.Path, Ecosystem: p.Package.Ecosystem, Package: p.Package.Name, Version: p.Package.Version,
					ID: g.IDs[0], IDs: sorted(g.IDs), Aliases: sorted(g.Aliases), Score: g.MaxSeverity, Rating: RatingOf(g.MaxSeverity),
				}
				fixed := map[string]bool{}
				for _, v := range p.Vulnerabilities {
					if !contains(g.IDs, v.ID) {
						continue
					}
					if f.Summary == "" {
						f.Summary = v.Summary
					}
					for _, a := range v.Affected {
						if a.Package.Name != p.Package.Name || !strings.EqualFold(a.Package.Ecosystem, p.Package.Ecosystem) {
							continue
						}
						for _, rng := range a.Ranges {
							for _, ev := range rng.Events {
								if fix := ev["fixed"]; fix != "" {
									fixed[fix] = true
								}
							}
						}
					}
				}
				for v := range fixed {
					f.Fixed = append(f.Fixed, v)
				}
				sort.Strings(f.Fixed)
				out = append(out, f)
				if len(out) > MaxFindings {
					return nil, fmt.Errorf("the report holds over %d findings", MaxFindings)
				}
			}
		}
	}
	return out, nil
}

// sorted is a sorted copy without repeats.
func sorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

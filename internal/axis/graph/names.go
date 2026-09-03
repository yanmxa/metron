package graph

import (
	"strings"
	"unicode"
)

// tokens splits an identifier into lowercase words: parseTimeWindow becomes
// {parse, time, window}, HTTPHandler becomes {http, handler}.
//
// Name overlap is the second signal in duplicate detection. Structural
// similarity alone over-fires — measured on a real index it flagged
// complete_text against complete_json and update_scenario against
// create_scenario, which are deliberate sibling variants, not duplication.
func tokens(name string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-':
			flush()
		case unicode.IsUpper(r):
			// A new word starts at a lower→upper boundary, and at the end of
			// an acronym run (HTTPHandler → http, handler).
			if i > 0 && (unicode.IsLower(rs[i-1]) ||
				(i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// nameOverlap is the Jaccard similarity of two identifiers' word sets.
func nameOverlap(a, b string) float64 {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	shared := 0
	seen := map[string]bool{}
	for _, t := range tb {
		if set[t] && !seen[t] {
			shared++
			seen[t] = true
		}
	}
	union := len(set)
	for _, t := range tb {
		if !set[t] {
			set[t] = true
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// jaccard measures how much two symbols call the same things. Two functions
// that reach for nearly the same collaborators are usually doing the same job,
// however differently they are written.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if _, ok := b[k]; ok {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// conventionEntryPoint reports names the language or a framework calls for you,
// so an absent caller says nothing about whether the code is dead.
func conventionEntryPoint(n Node) bool {
	switch n.Name {
	case "main", "init", "String", "Error", "Unwrap", "MarshalJSON", "UnmarshalJSON":
		return true
	}
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(n.Name, p) {
			return true
		}
	}
	return false
}

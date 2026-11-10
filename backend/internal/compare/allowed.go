package compare

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// The rule names an Allowed entry may use.
const (
	RuleDecimalEqual = "decimal-equal"
	RuleInstantEqual = "instant-equal"
	RuleIgnore       = "ignore"
	RuleUnordered    = "unordered"
	RuleNumberEqual  = "number-equal"
	RuleStatus       = "status"
	RuleDetailShape  = "detail-shape"
)

var knownRules = map[string]bool{
	RuleDecimalEqual: true,
	RuleInstantEqual: true,
	RuleIgnore:       true,
	RuleUnordered:    true,
	RuleNumberEqual:  true,
	RuleStatus:       true,
	RuleDetailShape:  true,
}

// AllowedEntry is one allowed difference: a rule for a JSONPath on a route.
// Route is "METHOD /path"; a {name} segment matches one path segment. Path is
// a JSONPath whose array indexes are [*]. Note names the PORT-NOTES number or
// spec line the difference stands for.
type AllowedEntry struct {
	Route string `yaml:"route"`
	Path  string `yaml:"path"`
	Rule  string `yaml:"rule"`
	Note  string `yaml:"note"`
}

// Allowed is the loaded allowed-diffs.yaml.
type Allowed struct {
	Entries []AllowedEntry
	// PythonTZ is the zone a naive Python timestamp is read in; nil is UTC.
	PythonTZ *time.Location
}

// ParseAllowed reads the entries of an allowed-diffs.yaml document. Every
// entry needs a route, a path, a known rule and a note.
func ParseAllowed(data []byte) (Allowed, error) {
	var entries []AllowedEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return Allowed{}, fmt.Errorf("allowed diffs: %w", err)
	}
	return newAllowed(entries)
}

// LoadAllowed reads the entries from r.
func LoadAllowed(r io.Reader) (Allowed, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Allowed{}, err
	}
	return ParseAllowed(data)
}

// LoadAllowedFile reads the entries from path.
func LoadAllowedFile(path string) (Allowed, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller argument (the --allowed flag)
	if err != nil {
		return Allowed{}, err
	}
	return ParseAllowed(data)
}

// newAllowed validates the entries and returns them.
func newAllowed(entries []AllowedEntry) (Allowed, error) {
	for i, e := range entries {
		if strings.TrimSpace(e.Route) == "" {
			return Allowed{}, fmt.Errorf("allowed diffs: entry %d has no route", i+1)
		}
		if strings.TrimSpace(e.Path) == "" {
			return Allowed{}, fmt.Errorf("allowed diffs: entry %d (%s) has no path", i+1, e.Route)
		}
		if !knownRules[e.Rule] {
			return Allowed{}, fmt.Errorf("allowed diffs: entry %d (%s %s) has unknown rule %q", i+1, e.Route, e.Path, e.Rule)
		}
		if strings.TrimSpace(e.Note) == "" {
			return Allowed{}, fmt.Errorf("allowed diffs: entry %d (%s %s) has no note", i+1, e.Route, e.Path)
		}
	}
	return Allowed{Entries: entries}, nil
}

// covers reports whether an entry with rule covers a difference at path.
func (a Allowed) covers(route, path, rule string) bool {
	np := normalizePath(path)
	for _, e := range a.Entries {
		if e.Rule != rule || !routeMatches(e.Route, route) {
			continue
		}
		if pathCovers(e.Path, np) {
			return true
		}
	}
	return false
}

// unorderedAt reports whether the array at path compares as a multiset.
func (a Allowed) unorderedAt(route, path string) bool {
	np := normalizePath(path)
	for _, e := range a.Entries {
		if e.Rule != RuleUnordered || !routeMatches(e.Route, route) {
			continue
		}
		ep := normalizePath(e.Path)
		if ep == np || ep == np+"[*]" {
			return true
		}
	}
	return false
}

// pathCovers reports whether an entry path covers a difference path: equal, or
// an ancestor of it.
func pathCovers(entry, path string) bool {
	if entry == path {
		return true
	}
	return strings.HasPrefix(path, entry+".") || strings.HasPrefix(path, entry+"[")
}

// routeMatches reports whether an entry route covers a route. The method is
// compared when the entry names one; a {name} segment matches one segment.
func routeMatches(pattern, route string) bool {
	pm, pp := splitRoute(pattern)
	rm, rp := splitRoute(route)
	if pm != "" && !strings.EqualFold(pm, rm) {
		return false
	}
	return pathMatches(pp, rp)
}

// splitRoute splits "METHOD /path" into its method (empty when absent) and its
// path, dropping any query string.
func splitRoute(r string) (method, path string) {
	if i := strings.IndexByte(r, '?'); i >= 0 {
		r = r[:i]
	}
	method, path, found := strings.Cut(r, " ")
	if !found {
		return "", method
	}
	return strings.TrimSpace(method), strings.TrimSpace(path)
}

// pathMatches reports whether a pattern path covers a route path.
func pathMatches(pattern, path string) bool {
	if pattern == path {
		return true
	}
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	rs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(rs) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}") {
			continue
		}
		if ps[i] != rs[i] {
			return false
		}
	}
	return true
}

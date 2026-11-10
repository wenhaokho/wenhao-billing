package compare

import (
	"fmt"
	"io"
	"os"
	"regexp"
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

// statusPath is the pseudo-path of the HTTP status. It is not a JSONPath, so
// no body field can collide with it.
const statusPath = "@status"

var knownRules = map[string]bool{
	RuleDecimalEqual: true,
	RuleInstantEqual: true,
	RuleIgnore:       true,
	RuleUnordered:    true,
	RuleNumberEqual:  true,
	RuleStatus:       true,
	RuleDetailShape:  true,
}

// AllowedEntry is one allowed difference: a rule for a path on a route.
//
// Route is "METHOD /path"; a {name} segment matches one path segment, and a
// literal route is preferred over a pattern. Path is a JSONPath whose array
// indexes are [*], or @status for the HTTP status. Note names the PORT-NOTES
// number or spec line the difference stands for.
//
// A status entry names the exact pair (Python, Go). A detail-shape entry
// names the Go status (GoCode) and/or a regexp on the Go detail
// (DetailPattern); at least one is required.
type AllowedEntry struct {
	Route         string `yaml:"route"`
	Path          string `yaml:"path"`
	Rule          string `yaml:"rule"`
	Note          string `yaml:"note"`
	Python        int    `yaml:"python"`
	Go            int    `yaml:"go"`
	GoCode        int    `yaml:"go_code"`
	DetailPattern string `yaml:"detail_pattern"`
}

// Allowed is the loaded allowed-diffs.yaml.
type Allowed struct {
	Entries []AllowedEntry
	// PythonTZ is the zone a naive Python timestamp is read in; nil is UTC.
	PythonTZ *time.Location
}

// ParseAllowed reads the entries of an allowed-diffs.yaml document. Every
// entry needs a route, a path, a known rule and a note; a status entry needs
// both codes and a detail-shape entry needs a code or a pattern.
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
		if err := validateEntry(i, e); err != nil {
			return Allowed{}, err
		}
	}
	return Allowed{Entries: entries}, nil
}

// validateEntry refuses an entry that is missing a field or too broad to name
// one difference.
func validateEntry(i int, e AllowedEntry) error {
	where := fmt.Sprintf("entry %d (%s %s)", i+1, e.Route, e.Path)
	if strings.TrimSpace(e.Route) == "" {
		return fmt.Errorf("allowed diffs: entry %d has no route", i+1)
	}
	if strings.TrimSpace(e.Path) == "" {
		return fmt.Errorf("allowed diffs: %s has no path", where)
	}
	if !knownRules[e.Rule] {
		return fmt.Errorf("allowed diffs: %s has unknown rule %q", where, e.Rule)
	}
	if strings.TrimSpace(e.Note) == "" {
		return fmt.Errorf("allowed diffs: %s has no note", where)
	}
	if !strings.HasPrefix(e.Path, "$") && !strings.HasPrefix(e.Path, "@") {
		return fmt.Errorf("allowed diffs: %s: path must start with $ or @", where)
	}
	switch e.Rule {
	case RuleStatus:
		if e.Path != statusPath {
			return fmt.Errorf("allowed diffs: %s: a status entry's path is %s", where, statusPath)
		}
		if e.Python == 0 || e.Go == 0 {
			return fmt.Errorf("allowed diffs: %s: a status entry needs python and go codes", where)
		}
	case RuleDetailShape:
		if strings.HasPrefix(e.Path, "@") {
			return fmt.Errorf("allowed diffs: %s: a detail-shape path is a body JSONPath", where)
		}
		if e.GoCode == 0 && e.DetailPattern == "" {
			return fmt.Errorf("allowed diffs: %s: a detail-shape entry needs go_code or detail_pattern", where)
		}
		if e.DetailPattern != "" {
			if _, err := regexp.Compile(e.DetailPattern); err != nil {
				return fmt.Errorf("allowed diffs: %s: bad detail_pattern: %w", where, err)
			}
		}
	case RuleIgnore, RuleUnordered:
		if e.Path == "$" {
			return fmt.Errorf("allowed diffs: %s: a %s path must not be $", where, e.Rule)
		}
	}
	return nil
}

// applicable is the entries that apply to route. A literal route is preferred
// over a pattern: when one exists for the request, only the literal entries
// apply, so a {id} entry never reaches a literal sibling such as
// awaiting-finalization.
func (a Allowed) applicable(route string) []AllowedEntry {
	rm, rp := splitRoute(route)
	var literal, pattern []AllowedEntry
	for _, e := range a.Entries {
		em, ep := splitRoute(e.Route)
		if em != "" && !strings.EqualFold(em, rm) {
			continue
		}
		if strings.Contains(ep, "{") {
			if routeMatches(e.Route, route) {
				pattern = append(pattern, e)
			}
			continue
		}
		if ep == rp {
			literal = append(literal, e)
		}
	}
	if len(literal) > 0 {
		return literal
	}
	return pattern
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

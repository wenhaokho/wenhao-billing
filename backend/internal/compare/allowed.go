package compare

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
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
	// PythonHost and GoHost are the two backends' own host names, so a
	// redirect's Location is compared with the scheme and host folded away
	// only between the backends themselves.
	PythonHost string `yaml:"-"`
	GoHost     string `yaml:"-"`
	// usage records which entries one run reached and matched. A runner sets
	// it (TrackUsage); every copy of the Allowed shares it. nil records nothing.
	usage *allowedUsage
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

// LoadAllowedFiles reads the entries of every path, in order, and merges them
// into one Allowed (the --allowed flag may be repeated: the shipped
// allowed-diffs.yaml, then an overlay such as allowed-diffs-prodcopy.yaml used
// for one kind of run only). An error names the file it came from.
func LoadAllowedFiles(paths ...string) (Allowed, error) {
	var merged []AllowedEntry
	for _, path := range paths {
		a, err := LoadAllowedFile(path)
		if err != nil {
			return Allowed{}, fmt.Errorf("%s: %w", path, err)
		}
		merged = append(merged, a.Entries...)
	}
	return newAllowed(merged)
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
		if isWholeBodyPath(e.Path) {
			return fmt.Errorf("allowed diffs: %s: a detail-shape path must not be $ or $[*]", where)
		}
		if e.GoCode == 0 && e.DetailPattern == "" {
			return fmt.Errorf("allowed diffs: %s: a detail-shape entry needs go_code or detail_pattern", where)
		}
		if e.DetailPattern != "" {
			if _, err := regexp.Compile(e.DetailPattern); err != nil {
				return fmt.Errorf("allowed diffs: %s: bad detail_pattern: %w", where, err)
			}
		}
	case RuleIgnore:
		if isWholeBodyPath(e.Path) {
			return fmt.Errorf("allowed diffs: %s: an ignore path must not be $ or $[*]", where)
		}
	case RuleUnordered:
		if e.Path == "$" {
			return fmt.Errorf("allowed diffs: %s: an unordered path must not be $", where)
		}
	}
	return nil
}

// UnusedAllowed is one allowed entry a run never needed, as the report lists
// it.
type UnusedAllowed struct {
	Route string `json:"route"`
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Note  string `json:"note"`
}

// allowedUsage is one run's record of the entries a request's route reached
// (exercised) and the entries that applied to a path or a status pair of an
// answer (matched). An entry is keyed by its value, so a repeated identical
// entry counts once.
type allowedUsage struct {
	mu        sync.Mutex
	exercised map[AllowedEntry]bool
	matched   map[AllowedEntry]bool
}

// TrackUsage returns a copy of a that records, from now on, which entries the
// diffs it is used for reach and match; Unused reads the record. A runner
// calls it once per run, so each run starts with an empty record.
func (a Allowed) TrackUsage() Allowed {
	a.usage = &allowedUsage{exercised: map[AllowedEntry]bool{}, matched: map[AllowedEntry]bool{}}
	return a
}

// exercise records that a request's route reached the entries.
func (u *allowedUsage) exercise(entries []AllowedEntry) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, e := range entries {
		u.exercised[e] = true
	}
}

// match records that the entry applied to an answer.
func (u *allowedUsage) match(e AllowedEntry) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.matched[e] = true
}

// documentaryRules never change the comparison (decimals and instants compare
// by value anyway, see allowed-diffs.yaml), so they can never match and are
// never reported as unused.
var documentaryRules = map[string]bool{RuleDecimalEqual: true, RuleInstantEqual: true}

// Unused is the entries, in file order, whose route the run requested but that
// never applied to anything: a path that no answer had (an ignore, unordered
// or number-equal entry), a status pair that never occurred, or a detail that
// never differed. Such an entry is dead or names the wrong field, as the PN 40
// `$.open_invoices` entry did. An entry whose route was never requested is not
// listed (the run did not exercise it), nor is a documentary rule. It is a
// warning only; without TrackUsage it is always empty.
func (a Allowed) Unused() []UnusedAllowed {
	out := []UnusedAllowed{}
	if a.usage == nil {
		return out
	}
	a.usage.mu.Lock()
	defer a.usage.mu.Unlock()
	seen := map[AllowedEntry]bool{}
	for _, e := range a.Entries {
		if seen[e] || documentaryRules[e.Rule] || !a.usage.exercised[e] || a.usage.matched[e] {
			continue
		}
		seen[e] = true
		out = append(out, UnusedAllowed{Route: e.Route, Path: e.Path, Rule: e.Rule, Note: e.Note})
	}
	return out
}

// isWholeBodyPath reports whether a path names the whole response: $, or $
// followed by nothing but [*] segments. Such a path would hide an entire body.
func isWholeBodyPath(path string) bool {
	if path == "$" {
		return true
	}
	if !strings.HasPrefix(path, "$") {
		return false
	}
	rest := path[1:]
	for rest != "" {
		if !strings.HasPrefix(rest, "[*]") {
			return false
		}
		rest = rest[3:]
	}
	return true
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

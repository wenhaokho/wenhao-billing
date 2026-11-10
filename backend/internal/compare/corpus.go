package compare

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v2"
)

// todayToken is the placeholder an as_of query uses; the runner fills it with
// the run's UTC date, so the balance sheet and the reports always compare the
// same day on both backends (decision 3: as_of = today).
const todayToken = "today"

// placeholder matches a {name} segment in a route or a query. A discovered id
// fills it; today's date fills {today}.
var placeholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// Corpus is the read comparison's ordered list of cases.
type Corpus struct {
	Cases []Case `yaml:"cases"`
}

// Case is one route the runner replays against both backends.
//
// ID names the case in the report: because ids are stable, a known compare
// difference (PN 6, two identical open invoices) is classified by the case id
// in the report rather than hidden by an allowance in allowed-diffs.yaml.
//
// Route is "GET /path"; every {name} in it is filled from an earlier case's
// Discover (or {today}). Queries are the query variants to send ("" is the
// bare route); each may repeat a parameter (status=OPEN&status=SENT) and may
// hold placeholders too. Discover names the id fields this case's list
// response yields, for the detail cases after it. Classify is the PORT-NOTES
// note a difference under this case belongs to; it never hides a difference.
type Case struct {
	ID       string     `yaml:"id"`
	Route    string     `yaml:"route"`
	Queries  []string   `yaml:"queries"`
	Discover []IDSource `yaml:"discover"`
	Classify string     `yaml:"classify"`
}

// IDSource names a JSON field collected from a list response's elements (Name)
// and the placeholder those ids fill (which defaults to Name). The recurring
// templates case names invoice_id, because its list returns invoices.
type IDSource struct {
	Name  string `yaml:"name"`
	Field string `yaml:"field"`
}

// UnmarshalYAML accepts a bare field name ("customer_id") as well as a
// name/field mapping, so the corpus stays readable where the two are equal.
func (s *IDSource) UnmarshalYAML(unmarshal func(any) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		s.Name, s.Field = name, name
		return nil
	}
	type raw IDSource
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	*s = IDSource(r)
	if s.Field == "" {
		s.Field = s.Name
	}
	return nil
}

// ParseCorpus reads a compare-corpus.yaml document and validates it: ids are
// unique, every route is a GET, and every placeholder has a discover source.
func ParseCorpus(data []byte) (Corpus, error) {
	var c Corpus
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Corpus{}, fmt.Errorf("compare corpus: %w", err)
	}
	if err := c.validate(); err != nil {
		return Corpus{}, err
	}
	return c, nil
}

// LoadCorpusFile reads the corpus from path.
func LoadCorpusFile(path string) (Corpus, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller argument (the --corpus flag)
	if err != nil {
		return Corpus{}, err
	}
	return ParseCorpus(data)
}

// validate refuses a corpus that cannot run: no cases, a duplicate id, a route
// that is not a GET, or a placeholder with no discover source.
func (c Corpus) validate() error {
	if len(c.Cases) == 0 {
		return fmt.Errorf("compare corpus: no cases")
	}
	ids := make(map[string]bool, len(c.Cases))
	discovered := map[string]bool{todayToken: true}
	for _, cs := range c.Cases {
		for _, d := range cs.Discover {
			discovered[d.Name] = true
		}
	}
	for i, cs := range c.Cases {
		where := fmt.Sprintf("case %d", i+1)
		if strings.TrimSpace(cs.ID) == "" {
			return fmt.Errorf("compare corpus: %s has no id", where)
		}
		where = fmt.Sprintf("case %s", cs.ID)
		if ids[cs.ID] {
			return fmt.Errorf("compare corpus: case %s is declared twice", cs.ID)
		}
		ids[cs.ID] = true
		method, path := splitRoute(cs.Route)
		if !strings.EqualFold(method, "GET") {
			return fmt.Errorf("compare corpus: %s: the read runner sends GET only, not %q", where, cs.Route)
		}
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("compare corpus: %s: route %q has no path", where, cs.Route)
		}
		for _, name := range placeholders(cs.Route) {
			if !discovered[name] {
				return fmt.Errorf("compare corpus: %s: {%s} has no discover source", where, name)
			}
		}
		for _, q := range cs.Queries {
			for _, name := range placeholders(q) {
				if !discovered[name] {
					return fmt.Errorf("compare corpus: %s: query {%s} has no discover source", where, name)
				}
			}
		}
	}
	return nil
}

// placeholders is the distinct {name} tokens of s, in order.
func placeholders(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// routePath is a case route's path, with the method and any query dropped.
func routePath(route string) string {
	_, path := splitRoute(route)
	return path
}

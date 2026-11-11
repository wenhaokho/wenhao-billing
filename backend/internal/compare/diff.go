// Package compare diffs the Python and Go backends' answers for one route,
// dropping every difference an Allowed entry covers (plan 11, Task 2).
package compare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Response is one backend's answer for a route.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Difference is one difference the Allowed entries do not cover. The JSON keys
// are snake_case so the report stays consistent.
type Difference struct {
	Path   string `json:"path"`
	Python string `json:"python"`
	Go     string `json:"go"`
	// Rule names the rule the difference was seen under: "decimal-equal",
	// "instant-equal", "number-equal", "status", "detail-shape", "self-check"
	// (Go's ledger report disagrees with itself; Python holds what the check
	// expected), or "" for a plain value difference.
	Rule string `json:"rule"`
	// Classify is the PORT-NOTES note a write step names for this JSON path
	// (classify_answers); the difference is still counted and listed.
	Classify string `json:"classify,omitempty"`
}

// missing marks a value present on one side only.
const missing = "<missing>"

// emptyText marks a response with no body, distinct from a JSON null.
const emptyText = "<empty>"

// emptyBody is the value of a response with no body.
type emptyBody struct{}

// decimalLike matches the plain decimal strings Python and Go print for money.
// A leading zero on a whole number is refused, so a digit string such as
// "0812" is not a decimal.
var decimalLike = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// indexPart matches an array index in a JSONPath, so it folds to [*].
var indexPart = regexp.MustCompile(`\[[0-9]+\]`)

// Diff compares the Python and Go answers for route, dropping every difference
// an Allowed entry covers. route is "METHOD /path" (a bare path is accepted)
// and any query string is ignored.
//
// Decimals compare by value (PN 99), timestamps with a time part compare as
// instants, and an error answer compares its detail/error fields and the keys
// both sides carry. PDFs compare status, Content-Type and Content-Disposition
// only. Every other difference is reported.
func Diff(route string, py, gov Response, a Allowed) []Difference {
	d := &differ{
		route:      route,
		entries:    a.applicable(route),
		pythonTZ:   a.PythonTZ,
		pythonHost: a.PythonHost,
		goHost:     a.GoHost,
		govStatus:  gov.Status,
		usage:      a.usage,
	}
	d.usage.exercise(d.entries)
	if isPDF(py) || isPDF(gov) {
		d.comparePDF(py, gov)
		return d.diffs
	}
	d.compareStatus(py.Status, gov.Status)
	if isRedirect(py.Status) || isRedirect(gov.Status) {
		d.compareLocation(py, gov)
	}
	pyv, pyErr := decodeBody(py.Body)
	govv, goErr := decodeBody(gov.Body)
	if isErrorStatus(py.Status) && isErrorStatus(gov.Status) {
		d.compareErrors(py.Body, gov.Body, pyv, govv)
		return d.diffs
	}
	if pyErr != nil || goErr != nil {
		d.compareMediaType(py, gov)
		if !bytes.Equal(py.Body, gov.Body) {
			d.add("$", rawText(py.Body), rawText(gov.Body), "")
		}
		return d.diffs
	}
	d.compare("$", pyv, govv)
	return d.diffs
}

// differ walks the two decoded bodies and records differences.
type differ struct {
	route      string
	entries    []AllowedEntry
	pythonTZ   *time.Location
	pythonHost string
	goHost     string
	govStatus  int
	usage      *allowedUsage
	diffs      []Difference
	// override tags a plain difference as this rule (the error body).
	override string
}

// ignored reports whether an entry ignores the path (or a subtree of it).
func (d *differ) ignored(path string) bool {
	np := normalizePath(path)
	for _, e := range d.entries {
		if e.Rule == RuleIgnore && pathCovers(e.Path, np) {
			d.usage.match(e)
			return true
		}
	}
	return false
}

// unorderedAt is the unordered entry for the array at path, if any: the array
// compares as a multiset, or by its entry's tie keys.
func (d *differ) unorderedAt(path string) (AllowedEntry, bool) {
	np := normalizePath(path)
	for _, e := range d.entries {
		if e.Rule != RuleUnordered {
			continue
		}
		ep := normalizePath(e.Path)
		if ep == np || ep == np+"[*]" {
			d.usage.match(e)
			return e, true
		}
	}
	return AllowedEntry{}, false
}

// visit records the documentary entries (decimal-equal, instant-equal) whose
// path an answer has, so an entry naming a wrong field shows as unused.
func (d *differ) visit(path string) {
	if d.usage == nil {
		return
	}
	np := normalizePath(path)
	for _, e := range d.entries {
		if documentaryRules[e.Rule] && pathCovers(e.Path, np) {
			d.usage.match(e)
		}
	}
}

// numberEqual reports whether the path compares its JSON numbers by value.
func (d *differ) numberEqual(path string) bool {
	np := normalizePath(path)
	for _, e := range d.entries {
		if e.Rule == RuleNumberEqual && pathCovers(e.Path, np) {
			d.usage.match(e)
			return true
		}
	}
	return false
}

// statusAllowed reports whether an entry names this exact status pair.
func (d *differ) statusAllowed(py, gov int) bool {
	for _, e := range d.entries {
		if e.Rule == RuleStatus && e.Python == py && e.Go == gov {
			d.usage.match(e)
			return true
		}
	}
	return false
}

// detailAllowed reports whether an entry allows this error detail: its Go
// status must match the entry's go_code (when set) and its Go detail the
// entry's pattern (when set).
func (d *differ) detailAllowed(path, gov string) bool {
	np := normalizePath(path)
	for _, e := range d.entries {
		if e.Rule != RuleDetailShape || !pathCovers(e.Path, np) {
			continue
		}
		if e.GoCode == 0 && e.DetailPattern == "" {
			continue
		}
		if e.GoCode != 0 && e.GoCode != d.govStatus {
			continue
		}
		if e.DetailPattern != "" {
			re, err := regexp.Compile(e.DetailPattern)
			if err != nil || !re.MatchString(gov) {
				continue
			}
		}
		d.usage.match(e)
		return true
	}
	return false
}

// add records one difference unless an entry covers it.
func (d *differ) add(path, py, gov, rule string) {
	if rule == "" && d.override != "" {
		rule = d.override
	}
	if d.ignored(path) {
		return
	}
	if rule == RuleDetailShape && d.detailAllowed(path, gov) {
		return
	}
	d.diffs = append(d.diffs, Difference{Path: path, Python: py, Go: gov, Rule: rule})
}

// compare walks two decoded values at path.
func (d *differ) compare(path string, py, gov any) {
	d.visit(path)
	if d.ignored(path) || d.emptyMismatch(path, py, gov) {
		return
	}
	switch p := py.(type) {
	case nil:
		if gov != nil {
			d.add(path, "null", text(gov), "")
		}
	case bool:
		g, ok := gov.(bool)
		if !ok || g != p {
			d.add(path, text(py), text(gov), "")
		}
	case string:
		g, ok := gov.(string)
		if !ok {
			d.add(path, text(py), text(gov), "")
			return
		}
		d.compareString(path, p, g)
	case json.Number:
		g, ok := gov.(json.Number)
		if !ok {
			d.add(path, text(py), text(gov), "")
			return
		}
		if !d.numbersEqual(path, p, g) {
			d.add(path, p.String(), g.String(), RuleNumberEqual)
		}
	case []any:
		g, ok := gov.([]any)
		if !ok {
			d.add(path, text(py), text(gov), "")
			return
		}
		d.compareArray(path, p, g)
	case map[string]any:
		g, ok := gov.(map[string]any)
		if !ok {
			d.add(path, text(py), text(gov), "")
			return
		}
		d.compareObject(path, p, g)
	default:
		d.add(path, text(py), text(gov), "")
	}
}

// emptyMismatch handles a body with no bytes: equal to another empty body,
// different from a JSON null or a value.
func (d *differ) emptyMismatch(path string, py, gov any) bool {
	_, pEmpty := py.(emptyBody)
	_, gEmpty := gov.(emptyBody)
	switch {
	case pEmpty && gEmpty:
		return true
	case pEmpty:
		d.add(path, emptyText, text(gov), "")
		return true
	case gEmpty:
		d.add(path, text(py), emptyText, "")
		return true
	}
	return false
}

// compareString compares two strings, as decimals by value and as instants.
func (d *differ) compareString(path, py, gov string) {
	if py == gov {
		return
	}
	if decimalLike.MatchString(py) && decimalLike.MatchString(gov) {
		if decimalsEqual(py, gov) {
			return
		}
		d.add(path, py, gov, RuleDecimalEqual)
		return
	}
	if pt, ok := parseInstant(py, d.pythonZone()); ok {
		if gt, ok := parseInstant(gov, time.UTC); ok {
			if pt.Equal(gt) {
				return
			}
			d.add(path, py, gov, RuleInstantEqual)
			return
		}
	}
	d.add(path, py, gov, "")
}

// compareObject walks the union of the two objects' keys.
func (d *differ) compareObject(path string, py, gov map[string]any) {
	for _, k := range unionKeys(py, gov) {
		cp := path + "." + k
		pv, pok := py[k]
		gv, gok := gov[k]
		switch {
		case pok && gok:
			d.compare(cp, pv, gv)
		case pok:
			d.add(cp, text(pv), missing, "")
		default:
			d.add(cp, missing, text(gv), "")
		}
	}
}

// compareArray walks two arrays in order, or as a multiset where allowed.
func (d *differ) compareArray(path string, py, gov []any) {
	if e, ok := d.unorderedAt(path); ok {
		keys := e.tieKeys()
		if len(keys) == 0 {
			d.compareUnordered(path, py, gov, span(0, len(py)), span(0, len(gov)))
			return
		}
		runs, ok := d.tieRuns(path, py, gov, keys)
		if ok {
			for _, r := range runs {
				d.compareUnordered(path, py, gov, r, r)
			}
			return
		}
		// The keys come in another order: the arrays are not sorted alike, so
		// they are compared in order and every difference is reported.
	}
	d.compareOrdered(path, py, gov)
}

// compareOrdered walks two arrays index by index.
func (d *differ) compareOrdered(path string, py, gov []any) {
	n := min(len(py), len(gov))
	for i := range n {
		d.compare(indexPath(path, i), py[i], gov[i])
	}
	for i := n; i < len(py); i++ {
		d.add(indexPath(path, i), text(py[i]), missing, "")
	}
	for i := n; i < len(gov); i++ {
		d.add(indexPath(path, i), missing, text(gov[i]), "")
	}
}

// compareUnordered pairs each Python element of pi (indexes into py) with an
// equal Go element of gi, then reports the leftovers in order.
func (d *differ) compareUnordered(path string, py, gov []any, pi, gi []int) {
	used := make([]bool, len(gi))
	matched := make([]bool, len(pi))
	for a, i := range pi {
		for b, j := range gi {
			if used[b] {
				continue
			}
			if d.softEqual(indexPath(path, i), py[i], gov[j]) {
				used[b] = true
				matched[a] = true
				break
			}
		}
	}
	leftPy, leftGov := unmatched(matched), unmatched(used)
	n := min(len(leftPy), len(leftGov))
	for k := range n {
		i, j := pi[leftPy[k]], gi[leftGov[k]]
		d.compare(indexPath(path, i), py[i], gov[j])
	}
	for k := n; k < len(leftPy); k++ {
		i := pi[leftPy[k]]
		d.add(indexPath(path, i), text(py[i]), missing, "")
	}
	for k := n; k < len(leftGov); k++ {
		j := gi[leftGov[k]]
		d.add(indexPath(path, j), missing, text(gov[j]), "")
	}
}

// span is the indexes lo..hi-1.
func span(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

// tieRuns checks that both arrays have the same length and the same tie-key
// values index by index (under the value rules: decimals by value, instants
// as instants), and returns the runs of consecutive indexes whose keys are
// equal. false means the arrays are not sorted alike.
func (d *differ) tieRuns(path string, py, gov []any, keys []string) ([][]int, bool) {
	if len(py) != len(gov) {
		return nil, false
	}
	for i := range py {
		if !d.keysEqual(indexPath(path, i), py[i], gov[i], keys) {
			return nil, false
		}
	}
	var runs [][]int
	start := 0
	for i := 1; i <= len(py); i++ {
		if i == len(py) || !d.keysEqual(indexPath(path, i), py[i-1], py[i], keys) {
			runs = append(runs, span(start, i))
			start = i
		}
	}
	return runs, true
}

// keysEqual reports whether two elements have equal values at every tie key.
// A missing key, or an element that is not an object, is a null value.
func (d *differ) keysEqual(path string, a, b any, keys []string) bool {
	for _, k := range keys {
		if !d.softEqual(path+"."+k, keyValue(a, k), keyValue(b, k)) {
			return false
		}
	}
	return true
}

// keyValue is an element's value at key, nil when absent.
func keyValue(el any, key string) any {
	if obj, ok := el.(map[string]any); ok {
		return obj[key]
	}
	return nil
}

// unmatched lists the false entries of a matched slice.
func unmatched(matched []bool) []int {
	var out []int
	for i, m := range matched {
		if !m {
			out = append(out, i)
		}
	}
	return out
}

// softEqual reports whether two values are equal under the same rules, without
// recording a difference. It pairs the elements of an unordered array.
func (d *differ) softEqual(path string, py, gov any) bool {
	d.visit(path)
	if d.ignored(path) {
		return true
	}
	_, pEmpty := py.(emptyBody)
	_, gEmpty := gov.(emptyBody)
	if pEmpty || gEmpty {
		return pEmpty && gEmpty
	}
	switch p := py.(type) {
	case nil:
		return gov == nil
	case bool:
		g, ok := gov.(bool)
		return ok && g == p
	case string:
		g, ok := gov.(string)
		if !ok {
			return false
		}
		if p == g {
			return true
		}
		if decimalLike.MatchString(p) && decimalLike.MatchString(g) {
			return decimalsEqual(p, g)
		}
		if pt, ok := parseInstant(p, d.pythonZone()); ok {
			if gt, ok := parseInstant(g, time.UTC); ok {
				return pt.Equal(gt)
			}
		}
		return false
	case json.Number:
		g, ok := gov.(json.Number)
		return ok && d.numbersEqual(path, p, g)
	case []any:
		g, ok := gov.([]any)
		if !ok {
			return false
		}
		if e, ok := d.unorderedAt(path); ok {
			if keys := e.tieKeys(); len(keys) > 0 {
				runs, ok := d.tieRuns(path, p, g, keys)
				if !ok {
					return false
				}
				for _, r := range runs {
					if !d.multisetEqual(path, p, g, r, r) {
						return false
					}
				}
				return true
			}
			return len(p) == len(g) && d.multisetEqual(path, p, g, span(0, len(p)), span(0, len(g)))
		}
		if len(p) != len(g) {
			return false
		}
		for i := range p {
			if !d.softEqual(indexPath(path, i), p[i], g[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		g, ok := gov.(map[string]any)
		return ok && d.objectSoftEqual(path, p, g)
	default:
		return false
	}
}

// objectSoftEqual is softEqual for two objects: the same keys, each equal.
func (d *differ) objectSoftEqual(path string, py, gov map[string]any) bool {
	for k, pv := range py {
		cp := path + "." + k
		if d.ignored(cp) {
			continue
		}
		gv, ok := gov[k]
		if !ok || !d.softEqual(cp, pv, gv) {
			return false
		}
	}
	for k := range gov {
		cp := path + "." + k
		if d.ignored(cp) {
			continue
		}
		if _, ok := py[k]; !ok {
			return false
		}
	}
	return true
}

// multisetEqual reports whether the elements pi of py and gi of gov are the
// same multiset.
func (d *differ) multisetEqual(path string, py, gov []any, pi, gi []int) bool {
	if len(pi) != len(gi) {
		return false
	}
	used := make([]bool, len(gi))
	for _, i := range pi {
		found := false
		for b, j := range gi {
			if used[b] {
				continue
			}
			if d.softEqual(indexPath(path, i), py[i], gov[j]) {
				used[b] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// numbersEqual compares two JSON numbers, by value where allowed.
func (d *differ) numbersEqual(path string, py, gov json.Number) bool {
	// Asked first, so an entry for a path whose numbers print alike still
	// counts as used (Allowed.Unused).
	byValue := d.numberEqual(path)
	if py.String() == gov.String() {
		return true
	}
	return byValue && decimalsEqual(py.String(), gov.String())
}

// compareStatus records a status difference at @status, a path no body field
// can produce.
func (d *differ) compareStatus(py, gov int) {
	if py == gov {
		return
	}
	if d.statusAllowed(py, gov) {
		return
	}
	d.diffs = append(d.diffs, Difference{
		Path: statusPath, Python: strconv.Itoa(py), Go: strconv.Itoa(gov), Rule: RuleStatus,
	})
}

// compareErrors compares two error answers. It compares the detail, error and
// error_description fields whenever either side has them, and every other key
// both sides carry (such as code); when neither side has any, it falls back to
// the raw text.
func (d *differ) compareErrors(pyBody, govBody []byte, pyv, govv any) {
	pyObj, pyIsObj := pyv.(map[string]any)
	govObj, govIsObj := govv.(map[string]any)
	if !pyIsObj || !govIsObj {
		if rawText(pyBody) != rawText(govBody) {
			d.add("$", rawText(pyBody), rawText(govBody), "")
		}
		return
	}
	prev := d.override
	d.override = RuleDetailShape
	defer func() { d.override = prev }()

	compared := false
	for _, k := range []string{"detail", "error", "error_description"} {
		pv, pok := pyObj[k]
		gv, gok := govObj[k]
		if !pok && !gok {
			continue
		}
		compared = true
		d.compareErrorField("$."+k, pv, pok, gv, gok)
	}
	for _, k := range objectKeys(pyObj) {
		if k == "detail" || k == "error" || k == "error_description" {
			continue
		}
		gv, gok := govObj[k]
		if !gok {
			continue
		}
		compared = true
		d.compare("$."+k, pyObj[k], gv)
	}
	if !compared && rawText(pyBody) != rawText(govBody) {
		d.add("$", rawText(pyBody), rawText(govBody), "")
	}
}

// compareErrorField compares one error field, present on one side or both.
func (d *differ) compareErrorField(path string, pv any, pok bool, gv any, gok bool) {
	switch {
	case pok && gok:
		d.compare(path, pv, gv)
	case pok:
		d.add(path, text(pv), missing, "")
	default:
		d.add(path, missing, text(gv), "")
	}
}

// comparePDF compares a PDF answer's status and two headers only.
func (d *differ) comparePDF(py, gov Response) {
	d.compareStatus(py.Status, gov.Status)
	for _, name := range []string{"Content-Type", "Content-Disposition"} {
		p, g := py.Header.Get(name), gov.Header.Get(name)
		if p != g {
			d.add("$header."+name, p, g, "")
		}
	}
}

// compareLocation compares two redirect answers' Location. The scheme and
// host are folded away only when both name one of the backends' own hosts (or
// are relative); otherwise the whole absolute URL is compared. The order of
// the query parameters may differ.
func (d *differ) compareLocation(py, gov Response) {
	p, g := py.Header.Get("Location"), gov.Header.Get("Location")
	if p != g && !locationEqual(p, g, d.pythonHost, d.goHost) {
		d.add("$header.Location", p, g, "")
	}
}

// locationEqual reports whether two Locations name the same path (escaped
// form), query, fragment, userinfo and opaque part. The scheme and host are
// folded away only between the backends' own hosts (or a relative Location);
// otherwise they are compared exactly. The order of the query parameters may
// differ.
func locationEqual(py, gov, pyHost, goHost string) bool {
	pu, err := url.Parse(py)
	if err != nil {
		return false
	}
	gu, err := url.Parse(gov)
	if err != nil {
		return false
	}
	if !ownLocation(pu, pyHost) || !ownLocation(gu, goHost) {
		if pu.Scheme != gu.Scheme || pu.Host != gu.Host {
			return false
		}
	}
	if pu.EscapedPath() != gu.EscapedPath() {
		return false
	}
	if sortedQuery(pu.RawQuery) != sortedQuery(gu.RawQuery) {
		return false
	}
	return pu.Fragment == gu.Fragment &&
		pu.Opaque == gu.Opaque &&
		pu.User.String() == gu.User.String()
}

// ownLocation reports whether a Location is relative — no scheme and no host —
// or its host is the backend's own host.
func ownLocation(u *url.URL, host string) bool {
	if u.Scheme == "" && u.Host == "" {
		return true
	}
	return host != "" && strings.EqualFold(u.Host, host)
}

// sortedQuery is a query string with its parameters in a fixed order.
func sortedQuery(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	return values.Encode()
}

// compareMediaType compares the media type of two non-JSON answers.
func (d *differ) compareMediaType(py, gov Response) {
	p, g := mediaType(py.Header.Get("Content-Type")), mediaType(gov.Header.Get("Content-Type"))
	if p != g {
		d.add("$header.Content-Type", p, g, "")
	}
}

// mediaType is a Content-Type without its parameters, lower-cased.
func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// isPDF reports whether a response carries a PDF.
func isPDF(r Response) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/pdf")
}

// isErrorStatus reports whether a status is an error answer.
func isErrorStatus(status int) bool { return status >= 400 }

// isRedirect reports whether a status is a redirect.
func isRedirect(status int) bool { return status >= 300 && status < 400 }

// decodeBody decodes a JSON body keeping numbers as written. An empty body is
// an emptyBody value, distinct from a JSON null; anything after the first value
// (a second value or a stray brace) is refused.
func decodeBody(b []byte) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return emptyBody{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// decimalsEqual reports whether two decimal strings are equal by value.
func decimalsEqual(py, gov string) bool {
	p, err := decimal.NewFromString(py)
	if err != nil {
		return false
	}
	g, err := decimal.NewFromString(gov)
	if err != nil {
		return false
	}
	return p.Equal(g)
}

// parseInstant parses an RFC 3339 timestamp or a naive timestamp with a time
// part. A date-only string is not an instant. A naive value is read in loc.
func parseInstant(s string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// pythonZone is the zone a naive Python timestamp is read in.
func (d *differ) pythonZone() *time.Location {
	if d.pythonTZ != nil {
		return d.pythonTZ
	}
	return time.UTC
}

// rawText is a body that did not decode as JSON, for a Difference.
func rawText(b []byte) string { return strings.TrimSpace(string(b)) }

// text renders a decoded JSON value for a Difference.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case emptyBody:
		return emptyText
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// unionKeys is the sorted union of two objects' keys.
func unionKeys(py, gov map[string]any) []string {
	seen := make(map[string]bool, len(py)+len(gov))
	for k := range py {
		seen[k] = true
	}
	for k := range gov {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// objectKeys is an object's sorted keys.
func objectKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizePath folds an array index to [*].
func normalizePath(path string) string { return indexPart.ReplaceAllString(path, "[*]") }

// indexPath is the JSONPath of an array's i-th element.
func indexPath(parent string, i int) string { return fmt.Sprintf("%s[%d]", parent, i) }

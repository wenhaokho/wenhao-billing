// Package compare diffs the Python and Go backends' answers for one route,
// dropping every difference an Allowed entry covers (plan 11, Task 2).
package compare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

// Difference is one difference the Allowed entries do not cover.
type Difference struct {
	Path   string
	Python string
	Go     string
	// Rule names the rule the difference was seen under: "decimal-equal",
	// "instant-equal", "number-equal", "status", "detail-shape", or "" for a
	// plain value difference.
	Rule string
}

// missing marks a value present on one side only.
const missing = "<missing>"

// decimalLike matches the plain decimal strings Python and Go print for money.
var decimalLike = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// indexPart matches an array index in a JSONPath, so it folds to [*].
var indexPart = regexp.MustCompile(`\[[0-9]+\]`)

// Diff compares the Python and Go answers for route, dropping every difference
// an Allowed entry covers. route is "METHOD /path" (a bare path is accepted)
// and any query string is ignored.
//
// Decimals compare by value (PN 99), RFC 3339 and naive timestamps compare as
// instants, and Python's {"detail": d} equals problem+json with the same
// detail and status. PDFs compare status, Content-Type and
// Content-Disposition only. Every other difference is reported.
func Diff(route string, py, gov Response, a Allowed) []Difference {
	d := &differ{route: route, allowed: a}
	if isPDF(py) || isPDF(gov) {
		d.comparePDF(py, gov)
		return d.diffs
	}
	d.compareStatus(py.Status, gov.Status)
	pyv, pyErr := decodeBody(py.Body)
	govv, goErr := decodeBody(gov.Body)
	if isErrorStatus(py.Status) && isErrorStatus(gov.Status) {
		d.compareErrors(py.Body, pyv, pyErr, gov.Body, govv, goErr)
		return d.diffs
	}
	if pyErr != nil || goErr != nil {
		d.add("$", rawText(py.Body), rawText(gov.Body), "")
		return d.diffs
	}
	d.compare("$", pyv, govv)
	return d.diffs
}

// differ walks the two decoded bodies and records differences.
type differ struct {
	route   string
	allowed Allowed
	diffs   []Difference
	// override tags a plain difference as this rule (the error detail).
	override string
}

// add records one difference unless an entry covers it.
func (d *differ) add(path, py, gov, rule string) {
	if rule == "" && d.override != "" {
		rule = d.override
	}
	if d.allowed.covers(d.route, path, RuleIgnore) {
		return
	}
	switch rule {
	case RuleStatus, RuleDetailShape:
		if d.allowed.covers(d.route, path, rule) {
			return
		}
	}
	d.diffs = append(d.diffs, Difference{Path: path, Python: py, Go: gov, Rule: rule})
}

// compare walks two decoded values at path.
func (d *differ) compare(path string, py, gov any) {
	if d.allowed.covers(d.route, path, RuleIgnore) {
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
	if d.allowed.unorderedAt(d.route, path) {
		d.compareUnordered(path, py, gov)
		return
	}
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

// compareUnordered pairs each Python element with an equal Go element, then
// reports the leftovers in order.
func (d *differ) compareUnordered(path string, py, gov []any) {
	used := make([]bool, len(gov))
	matched := make([]bool, len(py))
	for i := range py {
		for j := range gov {
			if used[j] {
				continue
			}
			if d.softEqual(indexPath(path, i), py[i], gov[j]) {
				used[j] = true
				matched[i] = true
				break
			}
		}
	}
	leftPy, leftGov := unmatched(matched), unmatched(used)
	n := min(len(leftPy), len(leftGov))
	for k := range n {
		d.compare(indexPath(path, leftPy[k]), py[leftPy[k]], gov[leftGov[k]])
	}
	for k := n; k < len(leftPy); k++ {
		d.add(indexPath(path, leftPy[k]), text(py[leftPy[k]]), missing, "")
	}
	for k := n; k < len(leftGov); k++ {
		d.add(indexPath(path, leftGov[k]), missing, text(gov[leftGov[k]]), "")
	}
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
	if d.allowed.covers(d.route, path, RuleIgnore) {
		return true
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
		if d.allowed.unorderedAt(d.route, path) {
			return d.multisetEqual(path, p, g)
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
		if d.allowed.covers(d.route, cp, RuleIgnore) {
			continue
		}
		gv, ok := gov[k]
		if !ok || !d.softEqual(cp, pv, gv) {
			return false
		}
	}
	for k := range gov {
		cp := path + "." + k
		if d.allowed.covers(d.route, cp, RuleIgnore) {
			continue
		}
		if _, ok := py[k]; !ok {
			return false
		}
	}
	return true
}

// multisetEqual reports whether two arrays hold the same elements.
func (d *differ) multisetEqual(path string, py, gov []any) bool {
	if len(py) != len(gov) {
		return false
	}
	used := make([]bool, len(gov))
	for i := range py {
		found := false
		for j := range gov {
			if used[j] {
				continue
			}
			if d.softEqual(indexPath(path, i), py[i], gov[j]) {
				used[j] = true
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
	if py.String() == gov.String() {
		return true
	}
	return d.allowed.covers(d.route, path, RuleNumberEqual) && decimalsEqual(py.String(), gov.String())
}

// compareStatus records a status difference.
func (d *differ) compareStatus(py, gov int) {
	if py == gov {
		return
	}
	d.add("$.status", strconv.Itoa(py), strconv.Itoa(gov), RuleStatus)
}

// compareErrors compares two error answers: their detail only, whatever else
// the problem+json shape carries.
func (d *differ) compareErrors(pyBody []byte, pyv any, pyErr error, govBody []byte, govv any, govErr error) {
	pd, pok := detailOf(pyv, pyErr, pyBody)
	gd, gok := detailOf(govv, govErr, govBody)
	prev := d.override
	d.override = RuleDetailShape
	defer func() { d.override = prev }()
	switch {
	case !pok && !gok:
	case !pok:
		d.add("$.detail", missing, text(gd), RuleDetailShape)
	case !gok:
		d.add("$.detail", text(pd), missing, RuleDetailShape)
	default:
		d.compare("$.detail", pd, gd)
	}
}

// detailOf is the detail of an error body: the "detail" field of an object,
// the decoded value, or the raw text of a body that did not decode.
func detailOf(v any, err error, body []byte) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		d, ok := m["detail"]
		return d, ok
	}
	if err == nil && v != nil {
		return v, true
	}
	if s := strings.TrimSpace(string(body)); s != "" {
		return s, true
	}
	return nil, false
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

// isPDF reports whether a response carries a PDF.
func isPDF(r Response) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/pdf")
}

// isErrorStatus reports whether a status is an error answer.
func isErrorStatus(status int) bool { return status >= 400 }

// decodeBody decodes a JSON body keeping numbers as written. An empty body is
// a nil value.
func decodeBody(b []byte) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
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

// parseInstant parses an RFC 3339 timestamp, a naive timestamp or a date. A
// naive value is read in loc.
func parseInstant(s string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// pythonZone is the zone a naive Python timestamp is read in.
func (d *differ) pythonZone() *time.Location {
	if d.allowed.PythonTZ != nil {
		return d.allowed.PythonTZ
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
	keys := make([]string, 0, len(py)+len(gov))
	seen := make(map[string]bool, len(py)+len(gov))
	for k := range py {
		seen[k] = true
	}
	for k := range gov {
		seen[k] = true
	}
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizePath folds an array index to [*].
func normalizePath(path string) string { return indexPart.ReplaceAllString(path, "[*]") }

// indexPath is the JSONPath of an array's i-th element.
func indexPath(parent string, i int) string { return fmt.Sprintf("%s[%d]", parent, i) }

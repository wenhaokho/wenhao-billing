// Package i18ntest checks a project's sentences against its Indonesian
// catalog. Call Scan from a test in the project:
//
//	func TestCatalog(t *testing.T) { i18ntest.Scan(t, "../..", catalog.ID) }
package i18ntest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/LOQATO/gokit/i18n"
)

// Reporter is the part of testing.TB Scan uses.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// Scan reads every non-test .go file under root (skipping testdata and
// vendor) and fails on: an i18n.T template that is not a string literal; a
// parameter name that is not a string literal or not in its template; a
// template with no Indonesian entry; an entry whose placeholders differ from
// its template's; an entry no code uses.
//
// extra are sentences shown by code outside root (a library's, such as
// apierr.Sentences()): they count as used and each needs an entry.
func Scan(r Reporter, root string, c *i18n.Catalog, extra ...string) {
	r.Helper()
	used := map[string]bool{}
	for _, template := range extra {
		used[template] = true
		if _, ok := c.Sentences[template]; !ok {
			r.Errorf("no Indonesian for %q", template)
		}
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == "testdata" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isT(call) {
				return true
			}
			checkCall(r, fset, call, c, used)
			return true
		})
		return nil
	})
	if err != nil {
		r.Errorf("scanning %s: %v", root, err)
	}
	var unused []string
	for template := range c.Sentences {
		if !used[template] {
			unused = append(unused, template)
		}
	}
	sort.Strings(unused)
	for _, template := range unused {
		r.Errorf("unused entry %q", template)
	}
}

func isT(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "i18n"
}

func checkCall(r Reporter, fset *token.FileSet, call *ast.CallExpr, c *i18n.Catalog, used map[string]bool) {
	r.Helper()
	at := fset.Position(call.Pos())
	if len(call.Args) == 0 {
		r.Errorf("%s: i18n.T with no template", at)
		return
	}
	template, ok := literal(call.Args[0])
	if !ok {
		r.Errorf("%s: i18n.T template is not a string literal", at)
		return
	}
	used[template] = true
	fields, ok := placeholders(template)
	if !ok {
		r.Errorf("%s: malformed template %q", at, template)
		return
	}
	given := map[string]bool{}
	for i := 1; i < len(call.Args); i += 2 {
		name, ok := literal(call.Args[i])
		if !ok {
			r.Errorf("%s: parameter name in %q is not a string literal", at, template)
			continue
		}
		if !fields[name] {
			r.Errorf("%s: parameter %q is not in %q", at, name, template)
		}
		given[name] = true
	}
	for name := range fields {
		if !given[name] {
			r.Errorf("%s: %q has no parameter %q", at, template, name)
		}
	}
	entry, ok := c.Sentences[template]
	if !ok {
		r.Errorf("%s: no Indonesian for %q", at, template)
		return
	}
	entryFields, ok := placeholders(entry)
	if !ok || !sameKeys(fields, entryFields) {
		r.Errorf("%s: placeholders of %q differ from its Indonesian %q", at, template, entry)
	}
}

func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// placeholders reads {name} fields; {{ and }} are literal braces.
func placeholders(template string) (map[string]bool, bool) {
	out := map[string]bool{}
	for i := 0; i < len(template); i++ {
		switch template[i] {
		case '{':
			if i+1 < len(template) && template[i+1] == '{' {
				i++
				continue
			}
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return nil, false
			}
			out[template[i+1:i+1+end]] = true
			i += end + 1
		case '}':
			if i+1 < len(template) && template[i+1] == '}' {
				i++
				continue
			}
			return nil, false
		}
	}
	return out, true
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

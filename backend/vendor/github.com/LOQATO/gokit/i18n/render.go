package i18n

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/LOQATO/gokit/money"
)

var (
	monthsEN      = [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	monthsShortEN = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	monthsID      = [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	monthsShortID = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
)

// Render writes t in lang. Indonesian uses c's entry for the template; with
// no entry, or an entry whose placeholders do not fit, the reader gets the
// English. It never panics; a template that cannot be formatted is returned
// as it stands.
func (t Text) Render(lang Lang, c *Catalog) string {
	if lang == Indonesian && c != nil {
		if template, ok := c.Sentences[t.Template]; ok {
			if out, ok := format(template, t.params(Indonesian, c)); ok {
				return out
			}
		}
	}
	if out, ok := format(t.Template, t.params(English, c)); ok {
		return out
	}
	return t.Template
}

func (t Text) params(lang Lang, c *Catalog) map[string]string {
	out := make(map[string]string, len(t.Params))
	for name, value := range t.Params {
		out[name] = param(value, lang, c)
	}
	return out
}

func param(value any, lang Lang, c *Catalog) string {
	switch v := value.(type) {
	case string:
		return v
	case Text:
		return v.Render(lang, c)
	case Word:
		return words(string(v), lang, c)
	case Noun:
		if lang == Indonesian && c != nil {
			if noun, ok := c.Nouns[string(v)]; ok {
				return noun
			}
		}
		return string(v)
	case Joined:
		parts := make([]string, len(v.Items))
		for i, item := range v.Items {
			if text, ok := item.(Text); ok {
				parts[i] = text.Render(lang, c)
			} else {
				parts[i] = fmt.Sprint(item)
			}
		}
		return strings.Join(parts, v.Sep)
	case Date:
		return DayLabel(v.Time, lang)
	case Month:
		return MonthLabel(v.Time, lang)
	case ShortMonth:
		return ShortMonthLabel(v.Time, lang)
	case MonthName:
		if lang == Indonesian {
			return monthsShortID[v.Month()-1]
		}
		return monthsShortEN[v.Month()-1]
	case Money:
		return money.Format(v.Amount, v.Currency)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprint(v)
	}
}

// words reads a stored value: "IN_PROGRESS" -> "In progress"; Indonesian
// from c.Words, else the English.
func words(value string, lang Lang, c *Catalog) string {
	if lang == Indonesian && c != nil {
		if word, ok := c.Words[value]; ok {
			return word
		}
	}
	if value == "" {
		return value
	}
	return value[:1] + strings.ReplaceAll(strings.ToLower(value[1:]), "_", " ")
}

// DayLabel writes a day: "10 Oct 2026" / "10 Okt 2026".
func DayLabel(d time.Time, lang Lang) string {
	months := monthsShortEN
	if lang == Indonesian {
		months = monthsShortID
	}
	return fmt.Sprintf("%d %s %d", d.Day(), months[d.Month()-1], d.Year())
}

// MonthLabel writes a month: "October 2026" / "Oktober 2026".
func MonthLabel(d time.Time, lang Lang) string {
	months := monthsEN
	if lang == Indonesian {
		months = monthsID
	}
	return fmt.Sprintf("%s %d", months[d.Month()-1], d.Year())
}

// ShortMonthLabel writes a month short: "Oct 2026" / "Okt 2026".
func ShortMonthLabel(d time.Time, lang Lang) string {
	months := monthsShortEN
	if lang == Indonesian {
		months = monthsShortID
	}
	return fmt.Sprintf("%s %d", months[d.Month()-1], d.Year())
}

// piece is a literal run or a {field} of a template.
type piece struct {
	text    string
	isField bool
}

var errTemplate = errors.New("malformed template")

// parse splits a template the way Python's str.format reads it: {name} is a
// field, {{ and }} are literal braces, anything else unbalanced is an error.
// A field is returned as written (it may hold a conversion or a spec, which
// fieldNames refuses).
func parse(template string) ([]piece, error) {
	var pieces []piece
	var lit strings.Builder
	for i := 0; i < len(template); i++ {
		ch := template[i]
		switch {
		case ch == '{' && i+1 < len(template) && template[i+1] == '{':
			lit.WriteByte('{')
			i++
		case ch == '}' && i+1 < len(template) && template[i+1] == '}':
			lit.WriteByte('}')
			i++
		case ch == '{':
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return nil, errTemplate
			}
			field := template[i+1 : i+1+end]
			if strings.ContainsRune(field, '{') {
				return nil, errTemplate
			}
			if lit.Len() > 0 {
				pieces = append(pieces, piece{text: lit.String()})
				lit.Reset()
			}
			pieces = append(pieces, piece{text: field, isField: true})
			i += end + 1
		case ch == '}':
			return nil, errTemplate
		default:
			lit.WriteByte(ch)
		}
	}
	if lit.Len() > 0 {
		pieces = append(pieces, piece{text: lit.String()})
	}
	return pieces, nil
}

// fieldNames is the set of names a template uses, or an error when a field
// is anything but a plain identifier (a conversion, a spec, a position, an
// attribute): nothing this package writes.
func fieldNames(template string) (map[string]bool, error) {
	pieces, err := parse(template)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range pieces {
		if !p.isField {
			continue
		}
		if !isIdentifier(p.text) {
			return nil, errTemplate
		}
		names[p.text] = true
	}
	return names, nil
}

func format(template string, values map[string]string) (string, bool) {
	pieces, err := parse(template)
	if err != nil {
		return "", false
	}
	var out strings.Builder
	for _, p := range pieces {
		if !p.isField {
			out.WriteString(p.text)
			continue
		}
		value, ok := values[p.text]
		if !ok {
			return "", false
		}
		out.WriteString(value)
	}
	return out.String(), true
}

// isIdentifier is Python's str.isidentifier for the names this package uses.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

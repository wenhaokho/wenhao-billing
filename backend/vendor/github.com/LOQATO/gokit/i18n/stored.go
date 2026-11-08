package i18n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// A Text kept in a database column, in the JSON TAGARA's Python dump_text
// writes: {"t": template, "p": {name: parameter}}. A parameter is a string,
// an integer, or one of {"text": ...}, {"word": ...},
// {"joined": [...], "sep": ...}, {"month": "YYYY-MM-DD"},
// {"short_month": ...}, {"month_name": ...}, {"money": ["1.50", "IDR"]},
// {"date": "YYYY-MM-DD"}.

const isoDate = "2006-01-02"

// Dump writes t as stored JSON. It refuses a parameter that cannot be read
// back (a Noun, a float, a bool, a decimal on its own): store a stored value
// as a Word and money as Money.
func Dump(t Text) ([]byte, error) {
	v, err := dumpText(t)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func dumpText(t Text) (map[string]any, error) {
	params := make(map[string]any, len(t.Params))
	for name, value := range t.Params {
		v, err := dumpParam(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		params[name] = v
	}
	return map[string]any{"t": t.Template, "p": params}, nil
}

func dumpParam(value any) (any, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int:
		return v, nil
	case int64:
		return v, nil
	case Text:
		inner, err := dumpText(v)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": inner}, nil
	case Word:
		return map[string]any{"word": string(v)}, nil
	case Joined:
		items := make([]any, len(v.Items))
		for i, item := range v.Items {
			switch item.(type) {
			case string, Text:
			default:
				return nil, fmt.Errorf("a Joined part is %T, not a string or a Text", item)
			}
			d, err := dumpParam(item)
			if err != nil {
				return nil, err
			}
			items[i] = d
		}
		return map[string]any{"joined": items, "sep": v.Sep}, nil
	case Month:
		return map[string]any{"month": v.Format(isoDate)}, nil
	case ShortMonth:
		return map[string]any{"short_month": v.Format(isoDate)}, nil
	case MonthName:
		return map[string]any{"month_name": v.Format(isoDate)}, nil
	case Money:
		return map[string]any{"money": []string{decimalString(v.Amount), v.Currency}}, nil
	case Date:
		return map[string]any{"date": v.Format(isoDate)}, nil
	default:
		return nil, fmt.Errorf("%T cannot be stored in a Text; name a stored value with Word", value)
	}
}

// decimalString keeps the amount's own scale ("933336.50", as Python writes
// it), where decimal.String would drop the trailing zero.
func decimalString(d decimal.Decimal) string {
	if exp := d.Exponent(); exp < 0 {
		return d.StringFixed(-exp)
	}
	return d.String()
}

var errUnreadable = errors.New("unreadable stored text")

// Load reads what Dump (or TAGARA's Python dump_text) wrote. It answers false
// for anything else, never panics: a column may hold anything.
func Load(data []byte) (text Text, ok bool) {
	defer func() {
		if recover() != nil {
			text, ok = Text{}, false
		}
	}()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return Text{}, false
	}
	t, err := loadText(v)
	if err != nil {
		return Text{}, false
	}
	return t, true
}

func loadText(v any) (Text, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return Text{}, errUnreadable
	}
	template, ok := obj["t"].(string)
	if !ok {
		return Text{}, errUnreadable
	}
	rawParams := map[string]any{}
	if p, present := obj["p"]; present {
		if rawParams, ok = p.(map[string]any); !ok {
			return Text{}, errUnreadable
		}
	}
	names, err := fieldNames(template)
	if err != nil {
		return Text{}, err
	}
	params := make(map[string]any, len(rawParams))
	for name, raw := range rawParams {
		// Python's Text(template, **params) reserves these two names.
		if !isIdentifier(name) || name == "template" || name == "self" {
			return Text{}, errUnreadable
		}
		value, err := loadParam(raw)
		if err != nil {
			return Text{}, err
		}
		params[name] = value
	}
	for name := range names {
		if _, ok := params[name]; !ok {
			return Text{}, errUnreadable
		}
	}
	return Text{Template: template, Params: params}, nil
}

func loadParam(raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil, errUnreadable
		}
		return int(n), nil
	case map[string]any:
		return loadTagged(v)
	default:
		return nil, errUnreadable
	}
}

func loadTagged(v map[string]any) (any, error) {
	if inner, ok := v["text"]; ok {
		return loadText(inner)
	}
	if word, ok := v["word"]; ok {
		s, ok := word.(string)
		if !ok {
			return nil, errUnreadable
		}
		return Word(s), nil
	}
	if items, ok := v["joined"]; ok {
		list, ok := items.([]any)
		sep, sepOK := v["sep"].(string)
		if !ok || !sepOK {
			return nil, errUnreadable
		}
		parts := make([]any, len(list))
		for i, item := range list {
			part, err := loadParam(item)
			if err != nil {
				return nil, err
			}
			switch part.(type) {
			case string, Text:
			default:
				return nil, errUnreadable
			}
			parts[i] = part
		}
		return Joined{Items: parts, Sep: sep}, nil
	}
	if s, ok := v["month"]; ok {
		d, err := loadDate(s)
		return Month{d}, err
	}
	if s, ok := v["short_month"]; ok {
		d, err := loadDate(s)
		return ShortMonth{d}, err
	}
	if s, ok := v["month_name"]; ok {
		d, err := loadDate(s)
		return MonthName{d}, err
	}
	if s, ok := v["date"]; ok {
		d, err := loadDate(s)
		return Date{d}, err
	}
	if pair, ok := v["money"]; ok {
		list, ok := pair.([]any)
		if !ok || len(list) != 2 {
			return nil, errUnreadable
		}
		amount, aOK := list[0].(string)
		currency, cOK := list[1].(string)
		if !aOK || !cOK {
			return nil, errUnreadable
		}
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return nil, errUnreadable
		}
		return Money{Amount: d, Currency: currency}, nil
	}
	return nil, errUnreadable
}

func loadDate(raw any) (time.Time, error) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, errUnreadable
	}
	d, err := time.Parse(isoDate, s)
	if err != nil {
		return time.Time{}, errUnreadable
	}
	return d, nil
}

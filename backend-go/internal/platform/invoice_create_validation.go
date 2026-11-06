package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func validateInvoiceCreate(body []byte) []validationError {
	if len(body) == 0 {
		return []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if e := dec.Decode(&value); e != nil {
		return itemUpdateJSONError(body, e, false)
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return itemUpdateJSONError(body, e, true)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}}
	}
	var errs []validationError
	add := func(typ string, loc []any, msg string, input any, ctx any) {
		errs = append(errs, validationError{typ, loc, msg, input, ctx})
	}
	stringField := func(name string, required, nullable bool, min, max int) {
		v, exists := obj[name]
		loc := []any{"body", name}
		if !exists {
			if required {
				add("missing", loc, "Field required", obj, nil)
			}
			return
		}
		if v == nil && nullable {
			return
		}
		x, ok := v.(string)
		if !ok {
			add("string_type", loc, "Input should be a valid string", v, nil)
			return
		}
		n := utf8.RuneCountInString(x)
		if n < min {
			add("string_too_short", loc, fmt.Sprintf("String should have at least %d characters", min), v, map[string]int{"min_length": min})
		} else if max > 0 && n > max {
			add("string_too_long", loc, fmt.Sprintf("String should have at most %d characters", max), v, map[string]int{"max_length": max})
		}
	}
	uuidField := func(name string) {
		v, exists := obj[name]
		if !exists || v == nil {
			return
		}
		loc := []any{"body", name}
		x, ok := v.(string)
		if !ok {
			add("uuid_type", loc, "UUID input should be a string, bytes or UUID object", v, nil)
			return
		}
		if _, ok := parsePathUUID(x); !ok {
			d := uuidErrorDetail(x)
			add("uuid_parsing", loc, "Input should be a valid UUID, "+d, v, map[string]string{"error": d})
		}
	}
	money := func(v any, loc []any, required bool) {
		if v == nil {
			if required {
				add("decimal_type", loc, "Decimal input should be an integer, float, string or Decimal object", v, nil)
			}
			return
		}
		raw, _ := json.Marshal(v)
		if _, ok := itemMoney(raw); !ok {
			e := itemMoneyError("", v)
			e.Loc = loc
			if e.Type == "greater_than_equal" {
				e.Ctx = map[string]int{"ge": 0}
			}
			errs = append(errs, e)
		}
	}
	for _, name := range []string{"customer_id", "project_id"} {
		uuidField(name)
	}
	stringField("invoice_type", false, false, 0, 0)
	stringField("currency", true, false, 3, 3)
	for _, name := range []string{"invoice_number", "po_so_number"} {
		stringField(name, false, true, 0, 0)
	}
	for _, name := range []string{"issue_date", "due_date"} {
		v, exists := obj[name]
		if !exists || v == nil {
			continue
		}
		loc := []any{"body", name}
		switch v.(type) {
		case string, json.Number:
		default:
			add("date_type", loc, "Input should be a valid date", v, nil)
			continue
		}

		_, problem := invoiceDate(v)
		if problem != nil {
			problem.Loc = loc
			problem.Input = v
			errs = append(errs, *problem)
		}
	}
	for _, name := range []string{"payment_terms", "notes", "footer", "internal_note"} {
		stringField(name, false, true, 0, 0)
	}
	for _, f := range []struct {
		name, expected string
		values         []string
	}{{"discount_type", "'PERCENT' or 'AMOUNT'", []string{"PERCENT", "AMOUNT"}}, {"wht_type", "'NONE', 'PPH21' or 'PPH23'", []string{"NONE", "PPH21", "PPH23"}}} {
		v, exists := obj[f.name]
		if exists && v != nil {
			valid := false
			for _, x := range f.values {
				if v == x {
					valid = true
				}
			}
			if !valid {
				add("literal_error", []any{"body", f.name}, "Input should be "+f.expected, v, map[string]string{"expected": f.expected})
			}
		}
		if f.name == "discount_type" {
			if v, exists := obj["discount_value"]; exists {
				money(v, []any{"body", "discount_value"}, false)
			}
		}
	}
	if v, exists := obj["wht_rate"]; exists && v != nil {
		text := strings.TrimPrefix(strings.TrimSpace(fmt.Sprint(v)), "-")
		loc := []any{"body", "wht_rate"}
		expanded, ok := expandMoney(text)
		if !ok {
			e := itemMoneyError("", v)
			e.Loc = loc
			errs = append(errs, e)
		} else {
			parts := strings.SplitN(expanded, ".", 2)
			whole := strings.TrimLeft(parts[0], "0")
			fraction := strings.TrimRight(parts[1], "0")
			if len(whole)+len(fraction) > 7 {
				add("decimal_max_digits", loc, "Decimal input should have no more than 7 digits in total", v, map[string]int{"max_digits": 7})
			} else if len(fraction) > 4 {
				add("decimal_max_places", loc, "Decimal input should have no more than 4 decimal places", v, map[string]int{"decimal_places": 4})
			} else if len(whole) > 3 {
				add("decimal_whole_digits", loc, "Decimal input should have no more than 3 digits before the decimal point", v, map[string]int{"whole_digits": 3})
			}
		}
	}

	if v, exists := obj["wht_gross_up"]; exists && v != nil {
		if e := itemBooleanError(v, []any{"body", "wht_gross_up"}); e != nil {
			errs = append(errs, *e)
		}
	}
	if v, exists := obj["line_items"]; exists {
		lines, ok := v.([]any)
		if !ok {
			add("list_type", []any{"body", "line_items"}, "Input should be a valid list", v, nil)
		} else {
			for i, lv := range lines {
				loc := []any{"body", "line_items", i}
				line, ok := lv.(map[string]any)
				if !ok {
					add("model_attributes_type", loc, "Input should be a valid dictionary or object to extract fields from", lv, nil)
					continue
				}
				for _, name := range []string{"item_id", "description", "quantity", "unit_price", "position"} {
					v, exists := line[name]
					p := append(append([]any{}, loc...), name)
					if !exists {
						if name == "description" || name == "quantity" || name == "unit_price" {
							add("missing", p, "Field required", line, nil)
						}
						continue
					}
					switch name {
					case "item_id":
						if v != nil {
							x, ok := v.(string)
							if !ok {
								add("uuid_type", p, "UUID input should be a string, bytes or UUID object", v, nil)
							} else if _, ok := parsePathUUID(x); !ok {
								d := uuidErrorDetail(x)
								add("uuid_parsing", p, "Input should be a valid UUID, "+d, v, map[string]string{"error": d})
							}
						}
					case "description":
						if _, ok := v.(string); !ok {
							add("string_type", p, "Input should be a valid string", v, nil)
						}
					case "quantity", "unit_price":
						money(v, p, true)
					case "position":
						if v == nil {
							add("int_type", p, "Input should be a valid integer", v, nil)
						}
					}
				}
			}
		}
	}
	return errs
}

func invoiceDate(v any) (time.Time, *validationError) {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return fxDate(strconv.FormatFloat(f, 'f', -1, 64))
		}
	}
	return fxDate(fmt.Sprint(v))
}

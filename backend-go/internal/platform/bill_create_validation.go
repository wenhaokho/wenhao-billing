package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

func validateBillCreate(body []byte) []validationError { return validateBill(body, false) }

func validateBill(body []byte, update bool) []validationError {
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
		if !exists {
			if name == "vendor_id" && !update {
				add("missing", []any{"body", name}, "Field required", obj, nil)
			}
			return
		}
		if v == nil && (name != "vendor_id" || update) {
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
	for _, name := range []string{"vendor_id", "project_id"} {
		uuidField(name)
	}
	stringField("currency", !update, update, 3, 3)
	for _, name := range []string{"bill_number", "po_number"} {
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
	for _, name := range []string{"payment_terms", "notes"} {
		stringField(name, false, true, 0, 0)
	}
	for _, f := range []struct {
		name, expected string
		values         []string
	}{{"discount_type", "'PERCENT' or 'AMOUNT'", []string{"PERCENT", "AMOUNT"}}} {
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
	if update {
		if v, exists := obj["status"]; exists && v != nil && v != "DRAFT" && v != "OPEN" && v != "PARTIAL" && v != "PAID" && v != "VOID" {
			add("literal_error", []any{"body", "status"}, "Input should be 'DRAFT', 'OPEN', 'PARTIAL', 'PAID' or 'VOID'", v, map[string]string{"expected": "'DRAFT', 'OPEN', 'PARTIAL', 'PAID' or 'VOID'"})
		}
	}
	if v, exists := obj["line_items"]; exists && !(update && v == nil) {
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
				for _, name := range []string{"item_id", "expense_account_id", "description", "quantity", "unit_price", "position"} {
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
					case "expense_account_id", "position":
						if v == nil && name == "expense_account_id" {
							continue
						}
						text := strings.TrimSpace(fmt.Sprint(v))
						if text == "true" {
							text = "1"
						}
						if text == "false" {
							text = "0"
						}
						rat, valid := boundedItemRat(text)
						if str, ok := v.(string); ok {
							valid = valid && billIntegerString.MatchString(strings.TrimSpace(str))
						}
						if !valid || !rat.IsInt() || !rat.Num().IsInt64() {
							typ, msg := "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
							if v == nil {
								typ, msg = "int_type", "Input should be a valid integer"
							} else if _, ok := v.(json.Number); ok && valid && !rat.IsInt() {
								typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
							}
							add(typ, p, msg, v, nil)
						}
					case "description":
						if _, ok := v.(string); !ok {
							add("string_type", p, "Input should be a valid string", v, nil)
						}
					case "quantity", "unit_price":
						money(v, p, true)

					}
				}
			}
		}
	}
	return errs
}

var billIntegerString = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.0+)?$`)

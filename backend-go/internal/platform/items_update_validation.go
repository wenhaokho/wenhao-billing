package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// updateField preserves omission separately from an explicit null.
type updateField[T any] struct {
	Present, Null bool
	Value         T
}
type itemUpdateTarget struct {
	ZoneID, RecordID, RecordName, RecordType, LiveContent, MaintenanceContent string
	Proxied                                                                   updateField[bool]
}
type itemUpdate struct {
	Sku, Name, ItemType, Description, DefaultCurrency, HostingDomain, HostingStatus updateField[string]
	DefaultUnitPrice, DefaultPurchasePrice                                          updateField[string]
	RevenueAccountID, ExpenseAccountID, HostingGraceDays                            updateField[*big.Int]
	IsSold, IsPurchased, Active, IsHosting, HostingSuspensionEnabled                updateField[bool]
	CloudflareTarget                                                                updateField[itemUpdateTarget]
}

func (u itemUpdate) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	add := func(k string, p, n bool, v any) {
		if p {
			if n {
				m[k] = nil
			} else {
				m[k] = v
			}
		}
	}
	for k, v := range map[string]updateField[string]{"sku": u.Sku, "name": u.Name, "item_type": u.ItemType, "description": u.Description, "default_currency": u.DefaultCurrency, "hosting_domain": u.HostingDomain, "hosting_status": u.HostingStatus, "default_unit_price": u.DefaultUnitPrice, "default_purchase_price": u.DefaultPurchasePrice} {
		add(k, v.Present, v.Null, v.Value)
	}
	for k, v := range map[string]updateField[bool]{"is_sold": u.IsSold, "is_purchased": u.IsPurchased, "active": u.Active, "is_hosting": u.IsHosting, "hosting_suspension_enabled": u.HostingSuspensionEnabled} {
		add(k, v.Present, v.Null, v.Value)
	}
	for k, v := range map[string]updateField[*big.Int]{"revenue_account_id": u.RevenueAccountID, "expense_account_id": u.ExpenseAccountID, "hosting_grace_days": u.HostingGraceDays} {
		add(k, v.Present, v.Null, v.Value)
	}
	t := u.CloudflareTarget
	if t.Present {
		if t.Null {
			m["cloudflare_target"] = nil
		} else {
			v := t.Value
			target := map[string]any{"zone_id": v.ZoneID, "record_id": v.RecordID, "record_name": v.RecordName, "record_type": v.RecordType, "live_content": v.LiveContent, "maintenance_content": v.MaintenanceContent}
			if v.Proxied.Present {
				target["proxied"] = v.Proxied.Value
			}
			m["cloudflare_target"] = target
		}
	}
	return json.Marshal(m)
}
func (u *itemUpdate) set(name string, present, null bool, value any) {
	sf := updateField[string]{Present: present, Null: null}
	bf := updateField[bool]{Present: present, Null: null}
	inf := updateField[*big.Int]{Present: present, Null: null}
	tf := updateField[itemUpdateTarget]{Present: present, Null: null}
	if v, ok := value.(string); ok {
		sf.Value = v
	}
	if v, ok := value.(bool); ok {
		bf.Value = v
	}
	if v, ok := value.(*big.Int); ok {
		inf.Value = v
	}
	if v, ok := value.(itemUpdateTarget); ok {
		tf.Value = v
	}
	switch name {
	case "sku":
		u.Sku = sf
	case "name":
		u.Name = sf
	case "item_type":
		u.ItemType = sf
	case "description":
		u.Description = sf
	case "default_currency":
		u.DefaultCurrency = sf
	case "hosting_domain":
		u.HostingDomain = sf
	case "hosting_status":
		u.HostingStatus = sf
	case "default_unit_price":
		u.DefaultUnitPrice = sf
	case "default_purchase_price":
		u.DefaultPurchasePrice = sf
	case "is_sold":
		u.IsSold = bf
	case "is_purchased":
		u.IsPurchased = bf
	case "active":
		u.Active = bf
	case "is_hosting":
		u.IsHosting = bf
	case "hosting_suspension_enabled":
		u.HostingSuspensionEnabled = bf
	case "revenue_account_id":
		u.RevenueAccountID = inf
	case "expense_account_id":
		u.ExpenseAccountID = inf
	case "hosting_grace_days":
		u.HostingGraceDays = inf
	case "cloudflare_target":
		u.CloudflareTarget = tf
	}
}

type itemUpdateValue struct {
	Present, Null bool
	Value         any
}

func parseItemUpdate(body []byte) (itemUpdate, []validationError) {
	u := itemUpdate{}
	if len(body) == 0 {
		return u, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var input any
	if err := d.Decode(&input); err != nil {
		return u, itemUpdateJSONError(body, err, false)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return u, itemUpdateJSONError(body, err, true)
	}
	obj, ok := input.(map[string]any)
	if !ok {
		return u, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", input, nil}}
	}
	var errs []validationError
	for _, f := range []struct {
		name, kind string
		min, max   int
	}{{"sku", "string", 0, 0}, {"name", "string", 0, 0}, {"item_type", "type", 0, 0}, {"description", "string", 0, 0}, {"default_currency", "string", 3, 3}, {"default_unit_price", "money", 0, 0}, {"default_purchase_price", "money", 0, 0}, {"revenue_account_id", "int", 0, 0}, {"expense_account_id", "int", 0, 0}, {"is_sold", "bool", 0, 0}, {"is_purchased", "bool", 0, 0}, {"active", "bool", 0, 0}, {"is_hosting", "bool", 0, 0}, {"hosting_domain", "string", 0, 255}, {"hosting_grace_days", "int", 0, 365}, {"hosting_suspension_enabled", "bool", 0, 0}, {"hosting_status", "status", 0, 0}, {"cloudflare_target", "target", 0, 0}} {
		v, exists := obj[f.name]
		if !exists {
			continue
		}
		field := itemUpdateValue{Present: true, Null: v == nil, Value: v}
		u.set(f.name, field.Present, field.Null, field.Value)
		if v == nil {
			continue
		}
		loc := []any{"body", f.name}
		switch f.kind {
		case "string":
			s, ok := v.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
				continue
			}
			n := utf8.RuneCountInString(s)
			if n < f.min {
				errs = append(errs, validationError{"string_too_short", loc, fmt.Sprintf("String should have at least %d characters", f.min), v, map[string]int{"min_length": f.min}})
			} else if f.max > 0 && n > f.max {
				errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
			}
		case "type", "status":
			options := []string{"SERVICE", "USAGE", "FIXED_FEE"}
			if f.kind == "status" {
				options = []string{"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"}
			}
			valid := false
			quoted := []string{}
			for _, s := range options {
				if v == s {
					valid = true
				}
				quoted = append(quoted, "'"+s+"'")
			}
			if !valid {
				expected := strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
				errs = append(errs, validationError{"literal_error", loc, "Input should be " + expected, v, map[string]string{"expected": expected}})
			}
		case "bool":
			if e := updateBooleanError(v, loc); e != nil {
				errs = append(errs, *e)
			} else {
				switch x := v.(type) {
				case bool:
					field.Value = x
				case string:
					switch strings.ToLower(x) {
					case "true", "1", "on", "yes", "y", "t":
						field.Value = true
					default:
						field.Value = false
					}
				case json.Number:
					r, _ := boundedItemRat(string(x))
					field.Value = r != nil && r.Sign() != 0
				}
				u.set(f.name, field.Present, field.Null, field.Value)
			}
		case "money":
			raw, _ := json.Marshal(v)
			n, valid := itemMoney(raw)
			if !valid {
				errs = append(errs, itemMoneyError(f.name, v))
			} else {
				value, _ := n.Value()
				field.Value = value
				u.set(f.name, field.Present, field.Null, field.Value)
			}
		case "int":
			s := fmt.Sprint(v)
			if b, ok := v.(bool); ok {
				if b {
					s = "1"
				} else {
					s = "0"
				}
			}
			s = strings.TrimSpace(s)
			r, valid := boundedItemRat(s)
			if _, yes := v.(string); yes && !itemIntegerString.MatchString(s) {
				valid = false
			}
			if !valid || !r.IsInt() {
				typ, msg := "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
				if _, yes := v.(json.Number); yes && r != nil && !r.IsInt() {
					typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
				} else if _, yes := v.(string); !yes {
					typ, msg = "int_type", "Input should be a valid integer"
				}
				errs = append(errs, validationError{typ, loc, msg, v, nil})
			} else if f.max > 0 && r.Sign() < 0 {
				errs = append(errs, validationError{"greater_than_equal", loc, "Input should be greater than or equal to 0", v, map[string]int{"ge": 0}})
			} else if f.max > 0 && r.Cmp(big.NewRat(int64(f.max), 1)) > 0 {
				errs = append(errs, validationError{"less_than_equal", loc, "Input should be less than or equal to 365", v, map[string]int{"le": 365}})
			} else {
				field.Value = new(big.Int).Set(r.Num())
				u.set(f.name, field.Present, field.Null, field.Value)
			}
		case "target":
			target, ok := v.(map[string]any)
			if !ok {
				errs = append(errs, validationError{"model_attributes_type", loc, "Input should be a valid dictionary or object to extract fields from", v, nil})
				continue
			}
			t := itemUpdateTarget{}
			for _, sub := range []struct {
				name string
				max  int
				dest *string
			}{{"zone_id", 100, &t.ZoneID}, {"record_id", 100, &t.RecordID}, {"record_name", 255, &t.RecordName}, {"record_type", 20, &t.RecordType}, {"live_content", 0, &t.LiveContent}, {"maintenance_content", 0, &t.MaintenanceContent}} {
				x, exists := target[sub.name]
				sl := []any{"body", f.name, sub.name}
				if !exists {
					errs = append(errs, validationError{"missing", sl, "Field required", target, nil})
					continue
				}
				s, ok := x.(string)
				if !ok {
					errs = append(errs, validationError{"string_type", sl, "Input should be a valid string", x, nil})
				} else if len(s) == 0 {
					errs = append(errs, validationError{"string_too_short", sl, "String should have at least 1 character", x, map[string]int{"min_length": 1}})
				} else if sub.max > 0 && utf8.RuneCountInString(s) > sub.max {
					errs = append(errs, validationError{"string_too_long", sl, fmt.Sprintf("String should have at most %d characters", sub.max), x, map[string]int{"max_length": sub.max}})
				} else {
					*sub.dest = s
				}
			}
			if x, exists := target["proxied"]; exists {
				t.Proxied.Present = true
				if e := updateBooleanError(x, []any{"body", f.name, "proxied"}); e != nil {
					errs = append(errs, *e)
				} else {
					switch x := x.(type) {
					case bool:
						t.Proxied.Value = x
					case string:
						switch strings.ToLower(x) {
						case "true", "1", "on", "yes", "y", "t":
							t.Proxied.Value = true
						}
					case json.Number:
						r, _ := boundedItemRat(string(x))
						t.Proxied.Value = r != nil && r.Sign() != 0
					}
				}
			} else {
				t.Proxied.Value = true
			}
			u.set(f.name, true, false, t)
		}
	}
	return u, errs
}

// Compare numeric booleans exactly; float64 would turn nearby decimals into 1.
func updateBooleanError(v any, loc []any) *validationError {
	if n, ok := v.(json.Number); ok {
		r, valid := boundedItemRat(string(n))
		if valid {
			if r.Sign() == 0 || r.Cmp(big.NewRat(1, 1)) == 0 {
				return nil
			}
			if r.IsInt() {
				return &validationError{"bool_parsing", loc, "Input should be a valid boolean, unable to interpret input", v, nil}
			}
		}
		return &validationError{"bool_type", loc, "Input should be a valid boolean", v, nil}
	}
	return itemBooleanError(v, loc)
}

// boundedItemRat rejects untrusted lexemes before big.Rat can allocate for an exponent.
func boundedItemRat(s string) (*big.Rat, bool) {
	if len(s) > 4096 {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp := s[i+1:]
		if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
			exp = exp[1:]
		}
		if len(exp) > 4 {
			return nil, false
		}
		n, err := strconv.Atoi(exp)
		if err != nil || n > 1000 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}
func itemUpdateJSONError(body []byte, err error, extra bool) []validationError {
	pos := len(body)
	reason := "Expecting value"
	if extra {
		dec := json.NewDecoder(bytes.NewReader(body))
		var v any
		_ = dec.Decode(&v)
		pos = int(dec.InputOffset())
		for pos < len(body) && (body[pos] == ' ' || body[pos] == '\n' || body[pos] == '\r' || body[pos] == '\t') {
			pos++
		}
		reason = "Extra data"
	} else if e, ok := err.(*json.SyntaxError); ok {
		if e.Offset > 0 && e.Offset <= int64(len(body)) {
			pos = int(e.Offset) - 1
		}
	}
	if !extra {
		prefix := strings.TrimSpace(string(body[:pos]))
		if strings.HasSuffix(prefix, "{") || (strings.HasSuffix(prefix, ",") && itemJSONContainer(body[:pos]) == '{') {
			reason = "Expecting property name enclosed in double quotes"
		}
	}
	// Python's JSON decoder reports positions in Unicode characters, not bytes.
	pos = utf8.RuneCount(body[:pos])
	return []validationError{{"json_invalid", []any{"body", pos}, "JSON decode error", map[string]any{}, map[string]string{"error": reason}}}
}

// itemJSONContainer identifies the innermost open container while ignoring strings.
func itemJSONContainer(prefix []byte) byte {
	var stack []byte
	quoted, escaped := false, false
	for _, c := range prefix {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) > 0 {
		return stack[len(stack)-1]
	}
	return 0
}

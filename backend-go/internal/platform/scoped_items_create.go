package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

func (h scopedProfileRouter) itemCreate(w http.ResponseWriter, r *http.Request) {

	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	var rawBody json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&rawBody); err != nil {
		validationResponse(w, itemUpdateJSONError(body, err, false))
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		validationResponse(w, []validationError{{"json_invalid", []any{"body"}, "JSON decode error", nil, nil}})
		return
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &input); err != nil || input == nil {
		var value any
		valueDecoder := json.NewDecoder(bytes.NewReader(rawBody))
		valueDecoder.UseNumber()
		if err := valueDecoder.Decode(&value); err != nil {
			authError(w, 500, "internal server error")
			return
		}
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}})
		return
	}

	originalJSON, err := json.Marshal(input)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	var values map[string]any
	originalDecoder := json.NewDecoder(bytes.NewReader(originalJSON))
	originalDecoder.UseNumber()
	if err = originalDecoder.Decode(&values); err != nil || values == nil {
		authError(w, 500, "internal server error")
		return
	}
	var original any = values
	var preliminary []validationError
	for _, field := range []string{"name", "item_type"} {
		value, exists := values[field]
		if !exists {
			preliminary = append(preliminary, validationError{"missing", []any{"body", field}, "Field required", original, nil})
			continue
		}
		if field == "name" {
			str, ok := value.(string)
			switch {
			case !ok:
				preliminary = append(preliminary, validationError{"string_type", []any{"body", field}, "Input should be a valid string", value, nil})
			case utf8.RuneCountInString(str) < 1:
				preliminary = append(preliminary, validationError{"string_too_short", []any{"body", field}, "String should have at least 1 character", value, map[string]int{"min_length": 1}})
			case utf8.RuneCountInString(str) > 255:
				preliminary = append(preliminary, validationError{"string_too_long", []any{"body", field}, "String should have at most 255 characters", value, map[string]int{"max_length": 255}})
			}
		} else if value != "SERVICE" && value != "USAGE" && value != "FIXED_FEE" {
			preliminary = append(preliminary, validationError{"literal_error", []any{"body", field}, "Input should be 'SERVICE', 'USAGE' or 'FIXED_FEE'", value, map[string]string{"expected": "'SERVICE', 'USAGE' or 'FIXED_FEE'"}})
		}
	}

	if value, exists := values["default_currency"]; exists {
		loc := []any{"body", "default_currency"}
		str, ok := value.(string)
		switch {
		case !ok:
			preliminary = append(preliminary, validationError{"string_type", loc, "Input should be a valid string", value, nil})
		case utf8.RuneCountInString(str) < 3:
			preliminary = append(preliminary, validationError{"string_too_short", loc, "String should have at least 3 characters", value, map[string]int{"min_length": 3}})
		case utf8.RuneCountInString(str) > 3:
			preliminary = append(preliminary, validationError{"string_too_long", loc, "String should have at most 3 characters", value, map[string]int{"max_length": 3}})
		}
	}
	for _, field := range []string{"default_unit_price", "default_purchase_price"} {
		if raw, ok := input[field]; ok {
			if _, valid := itemMoney(raw); !valid {
				preliminary = append(preliminary, itemMoneyError(field, values[field]))
			}
		}
	}
	if raw, exists := values["cloudflare_target"]; exists && raw != nil {
		target, ok := raw.(map[string]any)
		if !ok {
			preliminary = append(preliminary, validationError{"model_attributes_type", []any{"body", "cloudflare_target"}, "Input should be a valid dictionary or object to extract fields from", raw, nil})
		} else {
			for _, field := range []struct {
				name string
				max  int
			}{{"zone_id", 100}, {"record_id", 100}, {"record_name", 255}, {"record_type", 20}, {"live_content", 0}, {"maintenance_content", 0}} {
				val, ok := target[field.name]
				loc := []any{"body", "cloudflare_target", field.name}
				if !ok {
					preliminary = append(preliminary, validationError{"missing", loc, "Field required", target, nil})
					continue
				}
				str, ok := val.(string)
				switch {
				case !ok:
					preliminary = append(preliminary, validationError{"string_type", loc, "Input should be a valid string", val, nil})
				case utf8.RuneCountInString(str) == 0:
					preliminary = append(preliminary, validationError{"string_too_short", loc, "String should have at least 1 character", val, map[string]int{"min_length": 1}})
				case field.max > 0 && utf8.RuneCountInString(str) > field.max:
					preliminary = append(preliminary, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", field.max), val, map[string]int{"max_length": field.max}})
				}
			}
			if value, exists := target["proxied"]; exists {
				if e := itemBooleanError(value, []any{"body", "cloudflare_target", "proxied"}); e != nil {
					preliminary = append(preliminary, *e)
				}
			}

		}
	}

	for _, field := range []struct {
		name string
		max  int
	}{{"sku", 50}, {"description", 0}, {"hosting_domain", 255}} {
		value, exists := values[field.name]
		if !exists || value == nil {
			continue
		}
		loc := []any{"body", field.name}
		str, ok := value.(string)
		if !ok {
			preliminary = append(preliminary, validationError{"string_type", loc, "Input should be a valid string", value, nil})
		} else if field.max > 0 && utf8.RuneCountInString(str) > field.max {
			preliminary = append(preliminary, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", field.max), value, map[string]int{"max_length": field.max}})
		}
	}
	for _, field := range []string{"is_sold", "is_purchased", "active", "is_hosting", "hosting_suspension_enabled"} {
		value, exists := values[field]
		if !exists || (value == nil && field == "hosting_suspension_enabled") {
			continue
		}
		if e := itemBooleanError(value, []any{"body", field}); e != nil {
			preliminary = append(preliminary, *e)
		}
	}
	for _, field := range []string{"revenue_account_id", "expense_account_id", "hosting_grace_days"} {
		value, exists := values[field]
		if !exists || value == nil {
			continue
		}
		str := fmt.Sprint(value)
		if b, yes := value.(bool); yes {
			if b {
				str = "1"
			} else {
				str = "0"
			}
		}
		str = strings.TrimSpace(str)
		rat, ok := boundedItemRat(str)
		if _, isString := value.(string); isString && !itemIntegerString.MatchString(str) {
			ok = false
		}
		loc := []any{"body", field}
		if !ok || !rat.IsInt() {
			typ := "int_parsing"
			msg := "Input should be a valid integer, unable to parse string as an integer"
			if number, isNumber := value.(json.Number); isNumber && strings.ContainsAny(string(number), ".eE") && rat != nil && !rat.IsInt() {
				typ = "int_from_float"
				msg = "Input should be a valid integer, got a number with a fractional part"
			} else if _, ok := value.(string); !ok {
				typ = "int_type"
				msg = "Input should be a valid integer"
			}
			preliminary = append(preliminary, validationError{typ, loc, msg, value, nil})
			continue
		}
		if field == "hosting_grace_days" && rat.Sign() < 0 {
			preliminary = append(preliminary, validationError{"greater_than_equal", loc, "Input should be greater than or equal to 0", value, map[string]int{"ge": 0}})
		} else if field == "hosting_grace_days" && rat.Cmp(big.NewRat(365, 1)) > 0 {
			preliminary = append(preliminary, validationError{"less_than_equal", loc, "Input should be less than or equal to 365", value, map[string]int{"le": 365}})
		}
	}
	order := map[string]int{"sku": 0, "name": 1, "item_type": 2, "description": 3, "default_currency": 4, "default_unit_price": 5, "default_purchase_price": 6, "revenue_account_id": 7, "expense_account_id": 8, "is_sold": 9, "is_purchased": 10, "active": 11, "is_hosting": 12, "hosting_domain": 13, "hosting_grace_days": 14, "hosting_suspension_enabled": 15, "cloudflare_target": 16}
	sort.SliceStable(preliminary, func(i, j int) bool {
		return order[preliminary[i].Loc[1].(string)] < order[preliminary[j].Loc[1].(string)]
	})
	if len(preliminary) > 0 {
		validationResponse(w, preliminary)
		return
	}

	invalidInput := func(field string) {
		value := original.(map[string]any)[field]
		typ := "value_error"
		msg := "Invalid input"
		var ctx any
		switch field {
		case "is_sold", "is_purchased", "active", "is_hosting", "hosting_suspension_enabled":
			typ = "bool_parsing"
			msg = "Input should be a valid boolean, unable to interpret input"
			if value == nil {
				typ = "bool_type"
				msg = "Input should be a valid boolean"
			}
		case "name":
			if str, ok := value.(string); ok {
				if utf8.RuneCountInString(str) == 0 {
					typ = "string_too_short"
					msg = "String should have at least 1 character"
					ctx = map[string]int{"min_length": 1}
				} else {
					typ = "string_too_long"
					msg = "String should have at most 255 characters"
					ctx = map[string]int{"max_length": 255}
				}
			} else {
				typ = "string_type"
				msg = "Input should be a valid string"
			}
		case "item_type":
			typ = "literal_error"
			msg = "Input should be 'SERVICE', 'USAGE' or 'FIXED_FEE'"
			ctx = map[string]string{"expected": "'SERVICE', 'USAGE' or 'FIXED_FEE'"}

		case "default_unit_price", "default_purchase_price":
			e := itemMoneyError(field, value)
			typ = e.Type
			msg = e.Msg
			ctx = e.Ctx

		case "hosting_grace_days":
			str := fmt.Sprint(value)
			rat, ok := boundedItemRat(str)
			if ok && rat.IsInt() {
				if rat.Sign() < 0 {
					typ = "greater_than_equal"
					msg = "Input should be greater than or equal to 0"
					ctx = map[string]int{"ge": 0}
				} else {
					typ = "less_than_equal"
					msg = "Input should be less than or equal to 365"
					ctx = map[string]int{"le": 365}
				}
			} else {
				typ = "int_parsing"
				msg = "Input should be a valid integer, unable to parse string as an integer"
			}
		case "default_currency":
			if str, ok := value.(string); ok {
				if utf8.RuneCountInString(str) < 3 {
					typ = "string_too_short"
					msg = "String should have at least 3 characters"
					ctx = map[string]int{"min_length": 3}
				} else {
					typ = "string_too_long"
					msg = "String should have at most 3 characters"
					ctx = map[string]int{"max_length": 3}
				}
			} else {
				typ = "string_type"
				msg = "Input should be a valid string"
			}
		}
		validationResponse(w, []validationError{{typ, []any{"body", field}, msg, value, ctx}})
	}

	for _, field := range []string{"default_currency", "is_sold", "is_purchased", "active", "is_hosting"} {
		if raw, ok := input[field]; ok && string(raw) == "null" {
			invalidInput(field)
			return
		}
	}
	normalizeBool := func(obj map[string]json.RawMessage, field string, nullable bool) bool {
		raw, ok := obj[field]
		if !ok {
			return true
		}
		if string(raw) == "null" {
			return nullable
		}
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			return true
		}
		var val string
		if json.Unmarshal(raw, &val) != nil {
			var number float64
			if json.Unmarshal(raw, &number) == nil && (number == 0 || number == 1) {
				obj[field], _ = json.Marshal(number == 1)
				return true
			}
			val = string(raw)
		}
		switch strings.ToLower(val) {
		case "true", "1", "on", "yes", "y", "t":
			b = true
		case "false", "0", "off", "no", "n", "f":
			b = false
		default:
			return false
		}
		obj[field], _ = json.Marshal(b)
		return true
	}
	for _, field := range []string{"is_sold", "is_purchased", "active", "is_hosting", "hosting_suspension_enabled"} {
		if !normalizeBool(input, field, field == "hosting_suspension_enabled") {
			invalidInput(field)
			return
		}
	}
	for _, field := range []string{"revenue_account_id", "expense_account_id", "hosting_grace_days"} {
		raw, ok := input[field]
		if !ok || string(raw) == "null" {
			continue
		}
		var str string
		if json.Unmarshal(raw, &str) != nil {
			str = string(raw)
		}
		if string(raw) == "true" {
			str = "1"
		} else if string(raw) == "false" {
			str = "0"
		}
		rat, ok := boundedItemRat(strings.TrimSpace(str))
		if !ok || !rat.IsInt() {
			invalidInput(field)
			return
		}
		if !rat.Num().IsInt64() || rat.Num().Int64() < -2147483648 || rat.Num().Int64() > 2147483647 {
			authError(w, 500, "internal server error")
			return
		}
		input[field] = json.RawMessage(rat.Num().String())
	}
	if raw, ok := input["cloudflare_target"]; ok && string(raw) != "null" {
		var target map[string]json.RawMessage
		if json.Unmarshal(raw, &target) != nil || target == nil || !normalizeBool(target, "proxied", false) {
			invalidInput("cloudflare_target")
			return
		}

		var missingTarget []validationError
		for _, field := range []string{"zone_id", "record_id", "record_name", "record_type", "live_content", "maintenance_content"} {
			if _, ok := target[field]; !ok {
				missingTarget = append(missingTarget, validationError{"missing", []any{"body", "cloudflare_target", field}, "Field required", original.(map[string]any)["cloudflare_target"], nil})
			}
		}
		if len(missingTarget) > 0 {
			validationResponse(w, missingTarget)
			return
		}
		input["cloudflare_target"], _ = json.Marshal(target)
	}
	normalized, _ := json.Marshal(input)
	var b itemCreateBody
	if err := json.Unmarshal(normalized, &b); err != nil {
		validationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
		return
	}

	invalid := invalidInput

	if utf8.RuneCountInString(b.Name) < 1 || utf8.RuneCountInString(b.Name) > 255 {
		invalid("name")
		return
	}
	if b.ItemType != "SERVICE" && b.ItemType != "USAGE" && b.ItemType != "FIXED_FEE" {
		invalid("item_type")
		return
	}
	if b.Sku != nil && utf8.RuneCountInString(*b.Sku) > 50 {
		invalid("sku")
		return
	}
	currency := "IDR"
	if b.DefaultCurrency != nil {
		currency = *b.DefaultCurrency
	}
	if utf8.RuneCountInString(currency) != 3 {
		invalid("default_currency")
		return
	}
	price, ok := itemMoney(b.DefaultUnitPrice)
	if !ok {
		invalid("default_unit_price")
		return
	}
	purchase, ok := itemMoney(b.DefaultPurchasePrice)
	if !ok {
		invalid("default_purchase_price")
		return
	}
	if b.HostingDomain != nil && utf8.RuneCountInString(*b.HostingDomain) > 255 {
		invalid("hosting_domain")
		return
	}
	if b.HostingGraceDays != nil && (*b.HostingGraceDays < 0 || *b.HostingGraceDays > 365) {
		invalid("hosting_grace_days")
		return
	}
	hosting := boolDefault(b.IsHosting, false)

	if hosting {
		var fields []string
		if b.HostingDomain == nil || *b.HostingDomain == "" {
			fields = append(fields, "hosting_domain")
		}
		if b.HostingGraceDays == nil {
			fields = append(fields, "hosting_grace_days")
		}
		if b.HostingSuspensionEnabled == nil {
			fields = append(fields, "hosting_suspension_enabled")
		}
		if b.CloudflareTarget == nil {
			fields = append(fields, "cloudflare_target")
		}
		if len(fields) > 0 {
			detail := "hosting item requires: " + strings.Join(fields, ", ")
			validationResponse(w, []validationError{{"value_error", []any{"body"}, "Value error, " + detail, original, map[string]string{"error": detail}}})
			return
		}
	}

	if t := b.CloudflareTarget; t != nil {
		for _, v := range []struct {
			s   string
			max int
		}{{t.ZoneID, 100}, {t.RecordID, 100}, {t.RecordName, 255}, {t.RecordType, 20}, {t.LiveContent, 0}, {t.MaintenanceContent, 0}} {
			l := utf8.RuneCountInString(v.s)
			if l == 0 || (v.max > 0 && l > v.max) {
				invalid("cloudflare_target")
				return
			}
		}
	}
	if b.HostingDomain != nil && *b.HostingDomain != "" {
		v := strings.ToLower(strings.TrimSpace(*b.HostingDomain))
		b.HostingDomain = &v
	}
	id, err := itemUUID()
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	locked, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && locked.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = ensureScopedItemAccounts(r.Context(), tx, h.scope, intValue(b.RevenueAccountID), intValue(b.ExpenseAccountID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 400, "account not found")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	status := pgtype.Text{}
	if hosting {
		status = pgtype.Text{String: "ACTIVE", Valid: true}
	}
	i, err := scopeditem.InsertItem(r.Context(), tx, h.scope, dbqueryv8.InsertItemParams{ItemID: id, Sku: textValue(b.Sku), Name: b.Name, ItemType: b.ItemType, Description: textValue(b.Description), DefaultCurrency: currency, DefaultUnitPrice: price, DefaultPurchasePrice: purchase, RevenueAccountID: intValue(b.RevenueAccountID), ExpenseAccountID: intValue(b.ExpenseAccountID), IsSold: boolDefault(b.IsSold, true), IsPurchased: boolDefault(b.IsPurchased, false), Active: boolDefault(b.Active, true), IsHosting: hosting, HostingDomain: textValue(b.HostingDomain), HostingGraceDays: intValue(b.HostingGraceDays), HostingSuspensionEnabled: boolValue(b.HostingSuspensionEnabled), HostingStatus: status})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	var target *dbquery.CloudflareTarget
	if t := b.CloudflareTarget; t != nil {
		tid, e := itemUUID()
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		v, e := scopeditem.UpsertItemTarget(r.Context(), tx, h.scope, dbqueryv8.UpsertItemTargetParams{TargetID: tid, ItemID: id, ZoneID: strings.TrimSpace(t.ZoneID), RecordID: strings.TrimSpace(t.RecordID), RecordName: strings.ToLower(strings.TrimSpace(t.RecordName)), RecordType: strings.ToUpper(strings.TrimSpace(t.RecordType)), LiveContent: strings.TrimSpace(t.LiveContent), MaintenanceContent: strings.TrimSpace(t.MaintenanceContent), Proxied: boolDefault(t.Proxied, true)})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		legacy := legacyItemTarget(v)
		target = &legacy
	}
	encoded, err := json.Marshal(itemJSON(legacyItem(i), target))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}

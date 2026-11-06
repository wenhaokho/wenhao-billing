package platform

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/unicode/runenames"
)

type validationError struct {
	Type  string `json:"type"`
	Loc   []any  `json:"loc"`
	Msg   string `json:"msg"`
	Input any    `json:"input"`
	Ctx   any    `json:"ctx,omitempty"`
}
type loginInput struct {
	Email    string
	Password string
	Remember bool
}

func redactPasswordInput(value any) any {
	switch v := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(v))
		for k, item := range v {
			if strings.EqualFold(k, "password") {
				clean[k] = "[REDACTED]"
			} else {
				clean[k] = redactPasswordInput(item)
			}
		}
		return clean
	case []any:
		clean := make([]any, len(v))
		for i, item := range v {
			clean[i] = redactPasswordInput(item)
		}
		return clean
	default:
		return value
	}
}
func validationResponse(w http.ResponseWriter, errs []validationError) {
	for i := range errs {
		if len(errs[i].Loc) > 1 && errs[i].Loc[1] == "password" && errs[i].Type != "missing" {
			errs[i].Input = "[REDACTED]"
		} else {
			errs[i].Input = redactPasswordInput(errs[i].Input)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(422)
	json.NewEncoder(w).Encode(map[string]any{"detail": errs})
}
func loginValidation(w http.ResponseWriter, r *http.Request) (loginInput, bool) {
	var result loginInput
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		validationResponse(w, []validationError{{"json_invalid", []any{"body", 0}, "JSON decode error", map[string]any{}, map[string]string{"error": "Invalid JSON"}}})
		return result, false
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return result, false
	}
	if !requestJSONMediaType(r) {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return result, false
	}
	var obj any
	if err = json.Unmarshal(body, &obj); err != nil {
		pos := len(body)
		reason := "Expecting value"
		if e, ok := err.(*json.SyntaxError); ok && int(e.Offset) <= len(body) {
			pos = int(e.Offset) - 1
			if strings.Contains(e.Error(), "unexpected end") {
				pos = len(body)
			}
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed == "{" {
			reason = "Expecting property name enclosed in double quotes"
		}
		validationResponse(w, []validationError{{"json_invalid", []any{"body", pos}, "JSON decode error", map[string]any{}, map[string]string{"error": reason}}})
		return result, false
	}
	m, ok := obj.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", obj, nil}})
		return result, false
	}
	var errs []validationError
	for _, field := range []string{"email", "password"} {
		value, present := m[field]
		if !present {
			errs = append(errs, validationError{"missing", []any{"body", field}, "Field required", m, nil})
			continue
		}
		str, ok := value.(string)
		if !ok {
			errs = append(errs, validationError{"string_type", []any{"body", field}, "Input should be a valid string", value, nil})
			continue
		}
		if field == "password" {
			result.Password = str
			continue
		}
		normalized, reason := normalizeEmail(str)
		if reason != "" {
			errs = append(errs, validationError{"value_error", []any{"body", field}, "value is not a valid email address: " + reason, value, map[string]string{"reason": reason}})
		} else {
			result.Email = normalized
		}
	}
	if v, present := m["remember"]; present {
		var valid bool
		switch x := v.(type) {
		case bool:
			result.Remember = x
			valid = true
		case string:
			switch strings.ToLower(x) {
			case "1", "true", "t", "on", "yes", "y":
				result.Remember = true
				valid = true
			case "0", "false", "f", "off", "no", "n":
				valid = true
			}
		case float64:
			if x == 0 || x == 1 {
				valid = true
				result.Remember = x == 1
			}
		}
		if !valid {
			typ, msg := "bool_parsing", "Input should be a valid boolean, unable to interpret input"
			switch v.(type) {
			case nil, []any, map[string]any:
				typ, msg = "bool_type", "Input should be a valid boolean"
			}
			errs = append(errs, validationError{typ, []any{"body", "remember"}, msg, v, nil})
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return result, false
	}
	return result, true
}
func normalizeEmail(input string) (string, string) {
	if utf8.RuneCountInString(input) > 2048 {
		return "", "Length must not exceed 2048 characters"
	}
	if strings.ContainsAny(input, "\r\n") {
		return "", "Carriage return and line feed characters are not allowed"
	}
	s := strings.TrimSpace(input)
	if strings.HasPrefix(s, "\"") && strings.Contains(s, "\"@") {
		return "", "Quoting the part before the @-sign is not allowed here."
	}
	// Match Pydantic display-name syntax before validating the extracted address.
	// RFC comments are not accepted outside a quoted display name.
	if left := strings.LastIndexByte(s, '<'); left >= 0 && strings.HasSuffix(s, ">") {
		name := strings.TrimSpace(s[:left])
		validName := true
		if strings.HasPrefix(name, "\"") && strings.HasSuffix(name, "\"") && len(name) >= 2 {
			validName = len(name) > 2
		} else {
			for _, r := range name {
				if !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) || strings.ContainsRune("_.!#$%&'*+-/=?^`{|}~", r)) {
					validName = false
					break
				}
			}
		}
		if validName && left+1 < len(s)-1 {
			s = strings.TrimSpace(s[left+1 : len(s)-1])
		}
	}
	original := s
	i := strings.LastIndex(s, "@")
	if i < 0 {
		return "", "An email address must have an @-sign."
	}
	if i == 0 {
		return "", "There must be something before the @-sign."
	}
	if i == len(s)-1 {
		return "", "There must be something after the @-sign."
	}
	local, domain := norm.NFC.String(s[:i]), strings.ToLower(s[i+1:])
	if strings.HasPrefix(local, ".") {
		return "", "An email address cannot start with a period."
	}
	if strings.HasSuffix(local, ".") {
		return "", "An email address cannot have a period immediately before the @-sign."
	}
	if strings.Contains(s, "..") {
		return "", "An email address cannot have two periods in a row."
	}
	if strings.HasPrefix(domain, "-") {
		return "", "An email address cannot have a hyphen immediately after the @-sign."
	}
	if strings.HasSuffix(domain, ".") {
		return "", "An email address cannot end with a period."
	}
	if unicodeDomain, err := idna.Lookup.ToUnicode(domain); err == nil {
		domain = unicodeDomain
	}
	badSet := map[rune]bool{}
	for _, ch := range local {
		if ch < 128 && !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&'*+-/=?^_`{|}~. ", ch) {
			badSet[ch] = true
		}
	}
	if len(badSet) > 0 {
		bad := make([]string, 0, len(badSet))
		for ch := range badSet {
			if ch < 32 || ch == 127 {
				bad = append(bad, fmt.Sprintf("U+%04X", ch))
			} else if ch == '\\' {
				bad = append(bad, "\"\\\"")
			} else {
				bad = append(bad, "'"+string(ch)+"'")
			}
		}
		sort.Strings(bad)
		return "", "The email address contains invalid characters before the @-sign: " + strings.Join(bad, ", ") + "."
	}
	unsafe := map[rune]bool{}
	for i, ch := range local {
		if ch >= 128 && (unicode.Is(unicode.C, ch) || unicode.Is(unicode.Z, ch) || i == 0 && unicode.Is(unicode.M, ch)) {
			unsafe[ch] = true
		}
	}
	if len(unsafe) > 0 {
		chars := make([]rune, 0, len(unsafe))
		for ch := range unsafe {
			chars = append(chars, ch)
		}
		sort.Slice(chars, func(i, j int) bool { return chars[i] < chars[j] })
		names := make([]string, 0, len(chars))
		for _, ch := range chars {
			name := runenames.Name(ch)
			if name == "" || strings.HasPrefix(name, "<") {
				name = fmt.Sprintf("U+%04X", ch)
			}
			names = append(names, name)
		}
		return "", "The email address contains unsafe characters: " + strings.Join(names, ", ") + "."
	}
	if strings.Contains(local, " ") {
		return "", "The email address contains invalid characters before the @-sign: SPACE."
	}
	if strings.Contains(domain, "_") {
		return "", "The part after the @-sign contains invalid characters: '_'."
	}
	if ascii, err := idna.Lookup.ToASCII(domain); err != nil || len(ascii) > 253 {
		return "", "The part after the @-sign is not valid."
	} else {
		for _, label := range strings.Split(ascii, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", "The part after the @-sign is not valid."
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
					return "", "The part after the @-sign is not valid."
				}
			}
		}
	}
	if !strings.Contains(domain, ".") {
		return "", "The part after the @-sign is not valid. It should have a period."
	}
	for _, special := range []string{"arpa", "invalid", "local", "localhost", "onion", "test"} {
		if domain == special || strings.HasSuffix(domain, "."+special) {
			return "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."
		}
	}
	asciiDomain, _ := idna.Lookup.ToASCII(domain)
	last := asciiDomain[len(asciiDomain)-1]
	if last < 'a' || last > 'z' {
		return "", "The part after the @-sign is not valid. It is not within a valid top-level domain."
	}
	if strings.ContainsAny(local, "@<>\r\n\t") || strings.ContainsFunc(domain, unicode.IsSpace) {
		return "", "The email address is not valid."
	}
	for _, mailbox := range []string{"info", "marketing", "sales", "support", "abuse", "noc", "security", "postmaster", "hostmaster", "usenet", "news", "webmaster", "www", "uucp", "ftp"} {
		if strings.EqualFold(local, mailbox) {
			local = mailbox
			break
		}
	}
	normalized := local + "@" + domain
	for _, candidate := range []struct{ value, reason string }{{original, ""}, {normalized, "after normalization"}, {local + "@" + asciiDomain, "when the part after the @-sign is converted to IDNA ASCII"}} {
		diff := len(candidate.value) - 254
		if diff <= 0 {
			continue
		}
		suffix := ""
		if diff > 1 {
			suffix = "s"
		}
		reason := candidate.reason
		if reason != "" {
			reason += fmt.Sprintf(" (%d byte%s too many)", diff, suffix)
		} else if utf8.RuneCountInString(candidate.value) == len(candidate.value) {
			reason = fmt.Sprintf("(%d character%s too many)", diff, suffix)
		} else {
			max := 1
			for _, r := range candidate.value {
				if n := utf8.RuneLen(r); n > max {
					max = n
				}
			}
			min := diff / max
			if min < 1 {
				min = 1
			}
			if min == diff {
				reason = fmt.Sprintf("(%d character%s too many)", diff, suffix)
			} else {
				reason = fmt.Sprintf("(%d-%d character%s too many)", min, diff, suffix)
			}
		}
		return "", "The email address is too long " + reason + "."
	}
	return normalized, ""
}

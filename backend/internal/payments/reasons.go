package payments

import "strings"

// Reason codes: the kind of each reconciliation_log reason, stored beside the
// sentence in reason_codes (same order) so a client can tag a row without
// parsing English. Migration 00011 backfills the column with the same rules
// as a SQL CASE; TestReasonCodesBackfill keeps the two in step.
const (
	ReasonNoCandidate      = "NO_CANDIDATE"
	ReasonCurrencyMismatch = "CURRENCY_MISMATCH"
	ReasonAmountMismatch   = "AMOUNT_MISMATCH"
	ReasonLowConfidence    = "LOW_CONFIDENCE"
	ReasonAmbiguous        = "AMBIGUOUS"
	ReasonClearFailed      = "CLEAR_FAILED"
	ReasonExactMatch       = "EXACT_MATCH"
	ReasonManualEntry      = "MANUAL_ENTRY"
	ReasonManualApproval   = "MANUAL_APPROVAL"
	ReasonAliasMatch       = "ALIAS_MATCH"
	ReasonOther            = "OTHER"
)

// AllReasonCodes lists every code. The response structs publish the same list
// as an enum tag (a tag cannot use this slice); TestReasonCodesEnumInSchema
// fails when the two differ.
var AllReasonCodes = []string{
	ReasonNoCandidate, ReasonCurrencyMismatch, ReasonAmountMismatch, ReasonLowConfidence,
	ReasonAmbiguous, ReasonClearFailed, ReasonExactMatch, ReasonManualEntry,
	ReasonManualApproval, ReasonAliasMatch, ReasonOther,
}

// The held sentence clearIncoming writes when the invoice changed between
// matching and clearing: "invoice <id> changed before clearing".
const (
	clearFailedPrefix = "invoice "
	clearFailedSuffix = " changed before clearing"
	confidencePrefix  = "confidence "
)

// reasonPrefixes are the plain prefix rules, case-sensitive as the code
// writes the sentences.
var reasonPrefixes = []struct{ prefix, code string }{
	{"no open invoice matches", ReasonNoCandidate},
	{"currency mismatch:", ReasonCurrencyMismatch},
	{"amount mismatch:", ReasonAmountMismatch},
	{"ambiguous match:", ReasonAmbiguous},
	{"exact amount+currency", ReasonExactMatch},
	{"manual-entry", ReasonManualEntry},
	{"manual approval against invoice", ReasonManualApproval},
	{"alias match:", ReasonAliasMatch},
}

// ReasonCode is the code of one reason sentence on its own; a sentence no
// rule knows (a reversal's reason, a manual entry's notes) is OTHER. The code
// comes from the text alone, so user-supplied text (a reverse reason, a
// manual entry's notes) that starts with a known prefix gets that prefix's
// code: the spec derives codes from the text only, and accepts this.
// ReasonCodes adds the positional rule for clearing failures.
func ReasonCode(text string) string {
	for _, r := range reasonPrefixes {
		if strings.HasPrefix(text, r.prefix) {
			return r.code
		}
	}
	if rest, ok := strings.CutPrefix(text, confidencePrefix); ok && strings.Contains(rest, "below threshold") {
		return ReasonLowConfidence
	}
	if rest, ok := strings.CutPrefix(text, clearFailedPrefix); ok && strings.HasSuffix(rest, clearFailedSuffix) {
		return ReasonClearFailed
	}
	return ReasonOther
}

// ReasonCodes is the code of each reason, in order; never nil. Any reason
// after an EXACT_MATCH one is CLEAR_FAILED: clearIncoming holds a payment the
// engine cleared by appending why the clearing failed, and a ledger or FX
// refusal is free text (err.Error()) no prefix rule can know.
func ReasonCodes(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	afterExact := false
	for _, r := range reasons {
		code := ReasonCode(r)
		if afterExact {
			code = ReasonClearFailed
		}
		afterExact = afterExact || code == ReasonExactMatch
		out = append(out, code)
	}
	return out
}

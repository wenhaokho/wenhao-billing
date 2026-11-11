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

// ReasonCode is the code of one reason sentence; a sentence no rule knows
// (a reversal's reason, a manual entry's notes, a ledger or FX refusal held
// at clearing) is OTHER.
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

// ReasonCodes is ReasonCode of each reason, in order; never nil.
func ReasonCodes(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, ReasonCode(r))
	}
	return out
}

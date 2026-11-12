// One status → chip mapping for every screen (spec "Visual system").
// No imports: e2e/brand.spec.ts loads this file directly.

export type StatusTone = "green" | "amber" | "red" | "blue" | "neutral";

// Keys are API codes (upper snake case); statusTone/statusLabel also accept
// display spellings such as "Partially paid".
const TONES: Record<string, StatusTone> = {
  // done / good
  PAID: "green",
  CLEARED: "green",
  ACTIVE: "green",
  APPROVED: "green",
  ACCEPTED: "green",
  INVOICED: "green",
  COMPLETED: "green",
  // in flight
  SENT: "blue",
  FINALIZED: "blue",
  OPEN: "blue",
  // needs attention
  PARTIAL: "amber",
  PARTIALLY_PAID: "amber",
  PENDING_MANUAL_REVIEW: "amber",
  AWAITING_FINALIZATION: "amber",
  EXPIRED: "amber",
  PAUSED: "amber",
  ON_HOLD: "amber",
  // waiting on a person, like Manual Review (ruling R11)
  HELD: "amber",
  SUSPEND_PENDING: "amber",
  // stopped / wrong
  OVERDUE: "red",
  REJECTED: "red",
  DECLINED: "red",
  FLAGGED: "red",
  SUSPENDED: "red",
  MISCONFIGURED: "red",
  // not in play
  DRAFT: "neutral",
  VOID: "neutral",
  INACTIVE: "neutral",
  // finished, already handled (ruling R7)
  CANCELLED: "neutral",
  REVERSED: "neutral",
  ENDED: "neutral",
};

const LABELS: Record<string, string> = {
  PARTIAL: "Partially paid",
  PENDING_MANUAL_REVIEW: "Pending manual review",
  SUSPEND_PENDING: "Suspension pending",
};

function code(status: string): string {
  return status.trim().toUpperCase().replace(/[\s-]+/g, "_");
}

export function statusTone(status: string): StatusTone {
  return TONES[code(status)] ?? "neutral";
}

/** Sentence-case label; chips render it uppercase. */
export function statusLabel(status: string): string {
  const c = code(status);
  if (LABELS[c]) return LABELS[c];
  const words = c.toLowerCase().replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Reconciliation-log reason codes: the `reason_codes` enum in
 * backend/openapi.json (backend payments.AllReasonCodes). Keep the two in step.
 */
export type ReasonCode =
  | "NO_CANDIDATE"
  | "CURRENCY_MISMATCH"
  | "AMOUNT_MISMATCH"
  | "LOW_CONFIDENCE"
  | "AMBIGUOUS"
  | "CLEAR_FAILED"
  | "EXACT_MATCH"
  | "MANUAL_ENTRY"
  | "MANUAL_APPROVAL"
  | "ALIAS_MATCH"
  | "OTHER";

// Their own map (a Record over every code, so a new one fails the build):
// AMBIGUOUS or OTHER mean something else as an invoice status.
const REASON_TONES: Record<ReasonCode, StatusTone> = {
  // why it was held
  NO_CANDIDATE: "red",
  CURRENCY_MISMATCH: "red",
  AMOUNT_MISMATCH: "red",
  AMBIGUOUS: "red",
  CLEAR_FAILED: "red",
  LOW_CONFIDENCE: "amber",
  // evidence for the match
  ALIAS_MATCH: "green",
  EXACT_MATCH: "green",
  // a person's action or free text
  MANUAL_ENTRY: "neutral",
  MANUAL_APPROVAL: "neutral",
  OTHER: "neutral",
};

/** Tone of a reason code; one from a newer server is neutral. */
export function reasonTone(reasonCode: ReasonCode): StatusTone {
  return REASON_TONES[reasonCode] ?? "neutral";
}

/** Sentence-case label of a reason code ("Amount mismatch"). */
export function reasonLabel(reasonCode: ReasonCode): string {
  return statusLabel(reasonCode);
}

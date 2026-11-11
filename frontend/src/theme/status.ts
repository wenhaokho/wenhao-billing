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

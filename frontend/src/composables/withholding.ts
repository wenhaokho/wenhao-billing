export type WhtType = "NONE" | "PPH21" | "PPH23";

export const WHT_OPTIONS: { label: string; value: WhtType }[] = [
  { label: "None", value: "NONE" },
  { label: "PPh 21", value: "PPH21" },
  { label: "PPh 23", value: "PPH23" },
];

export const WHT_DEFAULT_RATES: Record<"PPH21" | "PPH23", number> = {
  PPH21: 2.5,
  PPH23: 2,
};

// Exact integer half-up division: round(n / d) for non-negative n, positive d.
function divHalfUp(n: bigint, d: bigint): bigint {
  return (2n * n + d) / (2n * d);
}

/**
 * Mirrors the backend withholding math.
 * - NONE: total = base, wht = 0
 * - without gross-up: total = base, wht = round(base * rate / 100)
 * - with gross-up: total = round(base / (1 - rate / 100)), wht = total - base
 * IDR rounds to 0 decimals, other currencies to 2.
 */
export function computeWithholding(
  base: number,
  currency: string,
  type: WhtType,
  rate: number | null,
  grossUp: boolean,
): { total: number; whtAmount: number } {
  if (type === "NONE" || rate == null || !(rate > 0 && rate < 100)) {
    return { total: base, whtAmount: 0 };
  }
  const decimals = currency === "IDR" ? 0 : 2;
  const scale = 10 ** decimals;
  // Scaled-integer arithmetic so half-up matches the backend's Decimal math.
  const baseMinor = BigInt(Math.round(base * scale));
  const rateE4 = BigInt(Math.round(rate * 10000)); // ten-thousandths of a percent
  const E6 = 1_000_000n; // 100% in ten-thousandths of a percent
  if (!grossUp) {
    const wht = divHalfUp(baseMinor * rateE4, E6);
    return { total: base, whtAmount: Number(wht) / scale };
  }
  const total = divHalfUp(baseMinor * E6, E6 - rateE4);
  return { total: Number(total) / scale, whtAmount: Number(total - baseMinor) / scale };
}

/** PPh still expected to be withheld on an invoice: max(0, wht_amount - wht_booked). */
export function whtOutstanding(inv: {
  wht_amount?: string | number | null;
  wht_booked?: string | number | null;
}): number {
  const v = Number(inv.wht_amount ?? 0) - Number(inv.wht_booked ?? 0);
  return Number.isFinite(v) && v > 0 ? Math.round(v * 10000) / 10000 : 0;
}

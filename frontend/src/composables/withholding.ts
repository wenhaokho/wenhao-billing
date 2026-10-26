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

// Half-up rounding to `decimals` places (amounts are non-negative).
function roundHalfUp(value: number, decimals: number): number {
  const factor = 10 ** decimals;
  return Math.round(value * factor) / factor;
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
  if (type === "NONE" || rate == null || !(rate > 0)) {
    return { total: base, whtAmount: 0 };
  }
  const decimals = currency === "IDR" ? 0 : 2;
  const r = (v: number) => roundHalfUp(v, decimals);
  if (!grossUp) {
    return { total: base, whtAmount: r((base * rate) / 100) };
  }
  const total = r(base / (1 - rate / 100));
  return { total, whtAmount: r(total - base) };
}

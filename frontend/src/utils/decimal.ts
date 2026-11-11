// Exact arithmetic on decimal strings (money stays a string end to end):
// fixed-point BigInt at 4 places, never Number().

const SCALE = 10_000n;

function toUnits(v: string | null | undefined): bigint {
  const s = (v ?? "").trim();
  if (!s) return 0n;
  const neg = s.startsWith("-");
  const [int, frac = ""] = (neg || s.startsWith("+") ? s.slice(1) : s).split(".");
  const units = BigInt(int || "0") * SCALE + BigInt((frac + "0000").slice(0, 4));
  return neg ? -units : units;
}

function fromUnits(u: bigint): string {
  const neg = u < 0n;
  const a = neg ? -u : u;
  const frac = (a % SCALE).toString().padStart(4, "0");
  return `${neg ? "-" : ""}${a / SCALE}.${frac}`;
}

/** Exact sum of decimal strings. */
export function decSum(values: (string | null | undefined)[]): string {
  return fromUnits(values.reduce((acc, v) => acc + toUnits(v), 0n));
}

/** Exact a − b of decimal strings. */
export function decSub(a: string | null | undefined, b: string | null | undefined): string {
  return fromUnits(toUnits(a) - toUnits(b));
}

export function decIsZero(v: string | null | undefined): boolean {
  return toUnits(v) === 0n;
}

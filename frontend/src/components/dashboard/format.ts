// Display helpers for the dashboard. Money stays a decimal string: sums go
// through exact fixed-point (BigInt at 4 places), and Number() is used only to
// scale a value for display (compact "71.8M", bar heights, percentages).

import type { KpiDelta } from "./types";

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

/** "71.8M", "1.2B", "940K", "512": one decimal, a trailing ".0" dropped. */
export function formatCompact(v: string | number | null | undefined): string {
  const n = typeof v === "number" ? v : Number(v ?? 0);
  if (!Number.isFinite(n)) return "—";
  const abs = Math.abs(n);
  const sign = n < 0 ? "-" : "";
  const one = (x: number) => {
    const r = Math.round(x * 10) / 10;
    return Number.isInteger(r) ? r.toFixed(0) : r.toFixed(1);
  };
  if (abs >= 1_000_000_000) return sign + one(abs / 1_000_000_000) + "B";
  if (abs >= 1_000_000) return sign + one(abs / 1_000_000) + "M";
  if (abs >= 1_000) return sign + one(abs / 1_000) + "K";
  return sign + one(abs);
}

/** A share in percent, rounded: "74%", "<1%" for a sliver, "—" when unknown. */
export function formatShare(p: number | null | undefined): string {
  if (p === null || p === undefined || !Number.isFinite(p)) return "—";
  if (p > 0 && p < 1) return "<1%";
  return `${Math.round(p)}%`;
}

/**
 * Change of `amount` against `previous` as "▲ 12.3%" / "▼ 4.0%". "—" when the
 * previous window was zero (no base to compare against). With `upIsGood`, up
 * reads green and down red.
 */
export function deltaOf(amount: string, previous: string, upIsGood = true): KpiDelta {
  const prev = Number(previous);
  if (!Number.isFinite(prev) || decIsZero(previous)) return { text: "—", tone: "neutral" };
  const change = ((Number(amount) - prev) / Math.abs(prev)) * 100;
  const rounded = Math.round(change * 10) / 10;
  if (rounded === 0) return { text: "0.0%", tone: "neutral" };
  const up = rounded > 0;
  const good = up === upIsGood;
  return { text: `${up ? "▲" : "▼"} ${Math.abs(rounded).toFixed(1)}%`, tone: good ? "good" : "bad" };
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "2026-10" -> "Oct". */
export function monthShort(ym: string): string {
  return MONTHS[Number(ym.split("-")[1]) - 1] ?? ym;
}

/** "2026-10" -> "Oct 2026". */
export function monthLong(ym: string): string {
  const [y] = ym.split("-");
  return `${monthShort(ym)} ${y}`;
}

/**
 * How long ago `iso` was, from `now` (ms): "just now" under a minute, then
 * "5 min ago", "3 h ago", "2 d ago". A time slightly in the future (clock
 * skew between browser and server) reads "just now".
 */
export function timeAgo(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "—";
  const secs = Math.floor((now - t) / 1000);
  if (secs < 60) return "just now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

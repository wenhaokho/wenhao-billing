/**
 * Chart.js colours from the brand tokens (--wb-* in assets/app.css). Read at
 * call time, so a chart built after a theme switch gets that theme's values;
 * callers rebuild their options when useTheme().theme changes.
 */
export interface ChartColors {
  /** Categorical series, in order: red, gold, blue, green, amber, soft red, muted. */
  series: string[];
  /** Opacity of a series' unpaid (still owed) segment, 0..1. */
  unpaidAlpha: number;
  /** Grid and axis lines. */
  grid: string;
  /** Tick and legend text. */
  text: string;
}

const SERIES_TOKENS = [
  "--wb-red",
  "--wb-gold",
  "--wb-blue-fg",
  "--wb-green-fg",
  "--wb-amber-fg",
  "--wb-red-soft",
  "--wb-muted-soft",
];

function token(style: CSSStyleDeclaration, name: string): string {
  return style.getPropertyValue(name).trim();
}

export function chartColors(): ChartColors {
  const style = getComputedStyle(document.documentElement);
  return {
    series: SERIES_TOKENS.map((t) => token(style, t)),
    unpaidAlpha: 0.3,
    grid: token(style, "--wb-line"),
    text: token(style, "--wb-ink"),
  };
}

/**
 * The series slot a currency always takes, so it keeps its colour in every
 * chart whatever its position in a dataset (variant A of the mockup: IDR red,
 * SGD gold, USD blue, MYR green).
 */
const PINNED: Record<string, number> = { IDR: 0, SGD: 1, USD: 2, MYR: 3, EUR: 4 };
/** Slots left for unpinned currencies, clear of the pinned ones. */
const FALLBACK = [5, 6];

export function currencySlot(ccy: string): number {
  const code = (ccy ?? "").toUpperCase();
  const pinned = PINNED[code];
  if (pinned !== undefined) return pinned;
  // Deterministic hash: an unpinned currency lands on the same slot in every chart.
  let h = 0;
  for (let i = 0; i < code.length; i++) h = (h * 31 + code.charCodeAt(i)) >>> 0;
  return FALLBACK[h % FALLBACK.length];
}

/** `color` (#rgb, #rrggbb or rgb()) at opacity `alpha`, as rgba(). */
export function withAlpha(color: string, alpha: number): string {
  const c = color.trim();
  let r: number, g: number, b: number;
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(c);
  if (hex) {
    const h = hex[1].length === 3 ? hex[1].replace(/./g, (x) => x + x) : hex[1];
    r = parseInt(h.slice(0, 2), 16);
    g = parseInt(h.slice(2, 4), 16);
    b = parseInt(h.slice(4, 6), 16);
  } else {
    const rgb = /^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/i.exec(c);
    if (!rgb) return c;
    [r, g, b] = [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])];
  }
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

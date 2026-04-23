// Decimal places per currency. Kept in one place so display stays consistent.
// IDR has no sub-unit in practice; SGD/USD use 2 decimals.
const DECIMALS = {
    IDR: 0,
    SGD: 2,
    USD: 2,
};
function decimalsFor(currency) {
    if (!currency)
        return 2;
    return DECIMALS[currency.toUpperCase()] ?? 2;
}
function toNumber(v) {
    if (v === null || v === undefined || v === "")
        return null;
    const n = typeof v === "number" ? v : Number(v);
    return Number.isFinite(n) ? n : null;
}
export function formatAmount(v, currency) {
    const n = toNumber(v);
    if (n === null)
        return "—";
    const digits = decimalsFor(currency);
    return n.toLocaleString(undefined, {
        minimumFractionDigits: digits,
        maximumFractionDigits: digits,
    });
}
export function formatMoney(v, currency) {
    const formatted = formatAmount(v, currency);
    if (formatted === "—")
        return formatted;
    return currency ? `${formatted} ${currency}` : formatted;
}

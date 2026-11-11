/**
 * The calendar day `d` falls on in the browser's time zone, as `YYYY-MM-DD`.
 *
 * Use this for every date the app sends to the API. A date picked in a
 * DatePicker is local midnight, and `toISOString()` converts it to UTC, which
 * east of UTC (the business runs on Jakarta time, UTC+7) is the previous day.
 */
export function toLocalIsoDate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

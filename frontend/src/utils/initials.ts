// No imports: e2e/shell.spec.ts loads this file directly.

/**
 * Up to two uppercase initials for the account chip: the first letters of the
 * first two words of a display name, or the first two letters or digits of an
 * email's local part ("j.doe@x" gives "JD", not "J.").
 */
export function initials(label: string): string {
  const text = label.trim();
  if (text.includes("@")) {
    const local = text.slice(0, text.indexOf("@")).replace(/[^\p{L}\p{N}]/gu, "");
    return (local || text).slice(0, 2).toUpperCase();
  }
  const parts = text.split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return text.slice(0, 2).toUpperCase();
}

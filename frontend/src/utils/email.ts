/** Split a comma/semicolon/whitespace-separated address list, dropping blanks. */
export function splitEmails(value: string | null | undefined): string[] {
  return (value ?? "")
    .split(/[,;\s]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Join address lists into one CC string, de-duplicated case-insensitively. */
export function joinCc(...lists: (string[] | string | null | undefined)[]): string {
  const out: string[] = [];
  for (const list of lists) {
    const items = Array.isArray(list) ? list : splitEmails(list);
    for (const addr of items) {
      if (addr && !out.some((o) => o.toLowerCase() === addr.toLowerCase())) out.push(addr);
    }
  }
  return out.join(", ");
}

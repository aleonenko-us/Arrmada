// shortDate is a library table's day ("Oct 3, 2026") from a stored timestamp, either
// "2026-10-03T10:00:00Z" or SQLite's "2026-10-03 10:00:00". Only the date part is read,
// so the day never shifts with the browser's time zone; "—" when there's none.
export function shortDate(s?: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(s ?? "");
  if (!m) return "—";
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

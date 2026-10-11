// Convert's time forecast, in words. The server estimates the seconds (from this machine's
// own conversion speed — internal/convert/throughput.go); this turns them into "~24 h ·
// ~4 nights" and "Backlog ≈ 9 months of nights at 01:00–07:00". nights() mirrors the
// server's Nights so the settings page can show the forecast at hours not yet saved.

function minutes(t: string | undefined): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec((t ?? "").trim());
  if (!m) return null;
  const h = +m[1], min = +m[2];
  return h <= 23 && min <= 59 ? h * 60 + min : null;
}

/** Seconds of the encode window per day; no (or an unreadable) window is the whole day. */
export function windowSeconds(start?: string, end?: string): number {
  const s = minutes(start), e = minutes(end);
  if (s === null || e === null || s === e) return 24 * 3600;
  return ((e - s + 1440) % 1440) * 60;
}

/** Nights of the start–end window that sec seconds of work needs (rounded up). */
export function nights(sec: number, start?: string, end?: string): number {
  if (!(sec > 0)) return 0;
  return Math.ceil(sec / windowSeconds(start, end));
}

/** "~40 min", "~24 h", "~3 days" — estimates, rounded generously. */
export function aboutTime(sec: number): string {
  if (!(sec > 0)) return "";
  const min = sec / 60;
  if (min < 55) return `~${Math.max(5, Math.round(min / 5) * 5)} min`;
  const h = sec / 3600;
  if (h < 48) return `~${Math.max(1, Math.round(h))} h`;
  return `~${Math.round(h / 24)} days`;
}

// span is n nights (or days) as the unit that reads best: "~4 nights", "~3 weeks",
// "~9 months", "~2 years", with `of` after the longer ones (" of nights").
function span(n: number, unit: "night" | "day", of: string, approx = "~"): string {
  if (!(n > 0)) return "";
  if (n < 14) return `${approx}${n} ${unit}${n === 1 ? "" : "s"}`;
  if (n < 60) return `${approx}${Math.round(n / 7)} weeks${of}`;
  if (n < 730) return `${approx}${Math.round(n / 30)} months${of}`;
  const y = Math.round(n / 365);
  return `${approx}${y} year${y === 1 ? "" : "s"}${of}`;
}

/** "~4 nights", "~3 weeks of nights", "~9 months of nights". */
export function aboutNights(n: number, ofNights = true): string {
  return span(n, "night", ofNights ? " of nights" : "");
}

/** Up next's line for one file: "~24 h · ~4 nights" (just the time when hours aren't set). */
export function fileForecast(etaSec: number | undefined, windowSet: boolean, n: number | undefined): string {
  if (!etaSec || etaSec <= 0) return "";
  const t = aboutTime(etaSec);
  return windowSet && n && n > 1 ? `${t} · ${aboutNights(n, false)}` : t;
}

/** The backlog in words: "Backlog ≈ 9 months of nights at 01:00–07:00", or "≈ 3 weeks at
 *  any time" when there are no hours. */
export function backlogLine(backlogSec: number | undefined, start?: string, end?: string): string {
  if (!backlogSec || backlogSec <= 0) return "";
  if (windowSeconds(start, end) >= 24 * 3600) {
    const days = Math.ceil(backlogSec / 86400);
    return `Backlog ≈ ${days <= 1 ? aboutTime(backlogSec).slice(1) : span(days, "day", "", "")} at any time`;
  }
  return `Backlog ≈ ${span(nights(backlogSec, start, end), "night", " of nights", "")} at ${start}–${end}`;
}

/** What a restart now would cost, once a running encode has done more than three hours. */
export function restartLoss(inFlightSec: number | undefined): string {
  if (!inFlightSec || inFlightSec <= 3 * 3600) return "";
  return `A restart or update now loses ~${Math.round(inFlightSec / 3600)} h of encoding`;
}

import type { Episode, SearchAttempt } from "../../lib/api";

// Grab missing / Grab run as background jobs, and every search leaves a stored attempt
// (search_attempts) under its scope: "S03" for a season, "S03E04" for an episode. The
// buttons read the latest one back from the server — what was found and why nothing was
// taken — rather than a "Requested" mark kept in this browser.
export const seasonScope = (n: number) => `S${String(n).padStart(2, "0")}`;

// latestFor is the newest stored attempt under scope, if any.
export function latestFor(attempts: SearchAttempt[], scope: string): SearchAttempt | undefined {
  let best: SearchAttempt | undefined;
  for (const a of attempts) {
    if (a.scope === scope && (!best || a.started_at > best.started_at || (a.started_at === best.started_at && a.id > best.id))) best = a;
  }
  return best;
}

export const today = new Date().toISOString().slice(0, 10);
export const aired = (e: Episode) => !!e.air_date && e.air_date <= today;
export const sxe = (e: Episode) => `S${String(e.season_number).padStart(2, "0")}E${String(e.episode_number).padStart(2, "0")}`;

export function fmtSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "—";
  const gb = bytes / 1024 ** 3;
  return gb >= 1 ? `${gb.toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`;
}

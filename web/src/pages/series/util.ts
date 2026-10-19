import type { Episode, SearchAttempt, Season, Series } from "../../lib/api";

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
export const aired = (e: Episode, on = today) => !!e.air_date && e.air_date <= on;
export const sxe = (e: Pick<Episode, "season_number" | "episode_number">) => `S${String(e.season_number).padStart(2, "0")}E${String(e.episode_number).padStart(2, "0")}`;
export const seasonName = (n: number) => (n === 0 ? "Specials" : `Season ${n}`);

export function fmtSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "—";
  const gb = bytes / 1024 ** 3;
  return gb >= 1 ? `${gb.toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`;
}

// sortSeasons is the page's season order: Season 1 first, Specials (season 0) last.
export function sortSeasons(seasons: Season[] | undefined): Season[] {
  return [...(seasons ?? [])].sort((a, b) => {
    if (a.season_number === 0) return 1;
    if (b.season_number === 0) return -1;
    return a.season_number - b.season_number;
  });
}

// fmtAirDay is a calendar date as people say it: "Thu 9 Oct", with the year once it isn't
// this one. The date is a plain YYYY-MM-DD, read as local so it never slips a day.
export function fmtAirDay(date: string, on = today): string {
  const d = new Date(`${date}T00:00:00`);
  if (isNaN(d.getTime())) return date;
  const opts: Intl.DateTimeFormatOptions = { weekday: "short", day: "numeric", month: "short" };
  if (date.slice(0, 4) !== on.slice(0, 4)) opts.year = "numeric";
  return d.toLocaleDateString(undefined, opts);
}

// nextEpisode is the soonest monitored episode still to air, matched to the server's
// stats.next_air_date (which counts only monitored episodes in monitored seasons) so the
// hero and the library card agree. Several on the same day: the first by number.
export function nextEpisode(s: Series): Episode | undefined {
  const date = s.stats?.next_air_date;
  if (!date) return undefined;
  let best: Episode | undefined;
  for (const sn of s.seasons ?? []) {
    if (!sn.monitored) continue;
    for (const e of sn.episodes ?? []) {
      if (e.air_date !== date || !e.monitored) continue;
      if (!best || e.season_number < best.season_number || (e.season_number === best.season_number && e.episode_number < best.episode_number)) best = e;
    }
  }
  return best;
}

// wantedInSeason: an aired episode the show is after — monitored, in a monitored season.
export const wantedIn = (sn: Season, e: Episode, on = today) => e.monitored && sn.monitored && aired(e, on);

// seasonCounts is a season's progress: files on disk out of what's counted (files plus
// aired wanted episodes), and the aired episodes nobody asked for (shown greyed instead).
export function seasonCounts(sn: Season, on = today) {
  const eps = sn.episodes ?? [];
  const have = eps.filter((e) => e.has_file).length;
  const counted = eps.filter((e) => e.has_file || wantedIn(sn, e, on)).length;
  const notMonitored = eps.filter((e) => !e.has_file && aired(e, on) && !wantedIn(sn, e, on)).length;
  const missing = eps.filter((e) => !e.has_file && e.monitored && aired(e, on)).length;
  const airedCount = eps.filter((e) => aired(e, on)).length;
  return { have, counted, notMonitored, missing, airedCount, total: eps.length };
}

// waitingOnSearch is what a show with its searching off is holding back: how many aired,
// monitored regular episodes have no file (they'd be searched for once it's on), and
// whether any regular episode is monitored at all (if none is, turning searching on
// monitors every one — see series.Repo.SetMonitored).
export function waitingOnSearch(s: Series, on = today): { wanted: number; anyMonitored: boolean } {
  let wanted = 0;
  let anyMonitored = false;
  for (const sn of s.seasons ?? []) {
    if (sn.season_number === 0) continue;
    for (const e of sn.episodes ?? []) {
      if (e.monitored) anyMonitored = true;
      if (!e.has_file && wantedIn(sn, e, on)) wanted++;
    }
  }
  return { wanted, anyMonitored };
}

// defaultSeason is where the page opens: the first season with a monitored aired episode
// missing, else the latest season that has aired anything, else the first one. Specials
// come last in both searches, so they're only the default when nothing else qualifies.
export function defaultSeason(seasons: Season[], on = today): number | undefined {
  const ordered = sortSeasons(seasons);
  const gap = ordered.find((sn) => sn.season_number > 0 && seasonCounts(sn, on).missing > 0);
  if (gap) return gap.season_number;
  const airedSeasons = ordered.filter((sn) => sn.season_number > 0 && seasonCounts(sn, on).airedCount > 0);
  if (airedSeasons.length) return airedSeasons[airedSeasons.length - 1].season_number;
  return ordered[0]?.season_number;
}

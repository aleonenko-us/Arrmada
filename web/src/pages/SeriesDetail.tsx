import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { disposalLine, useRecycleMode } from "../lib/disposal";
import { ReleaseSearchModal } from "../components/ReleaseSearchModal";
import { FileDetailsModal } from "../components/FileDetailsModal";
import { FitBadge } from "../components/FitBadge";
import { NumberingBanner } from "./series/NumberingReviewModal";
import { usePoll } from "../lib/usePoll";
import { libraryStatus } from "../lib/status";
import { useLiveConnected, useLiveEvent } from "../lib/live";
import { QUEUE_CHANGED, QUEUE_PROGRESS, progressByHash, queueChangedFor } from "../lib/queueLive";
import { downloadHashes, downloadingCount, mergeDownloads, patchSeriesProgress } from "./series/downloads";
import { jobFailed, jobToast, useJob } from "../lib/useJob";
import { attemptLine, outcomeTone, searchFinishedFor } from "../lib/searchOutcome";
import { refreshSearches, useSearchAttempts } from "../components/LastSearch";
import { Button, StatusChip, useToast } from "../ui";
import { api, type CastMember, type FitItem, type Series as SeriesT, type Season, type Episode } from "../lib/api";
import { Hero } from "./series/Hero";
import { HeroActions } from "./series/HeroActions";
import { AliasPanel, DuplicatesPanel, HistoryPanel, SceneMapPanel, SeriesBlocklistPanel } from "./series/panels";
import { aired, latestFor, seasonScope, sortSeasons, sxe } from "./series/util";

// What the page listens for besides the queue (see SeriesDetail).
const SERIES_TOPICS = ["series.imported", "series.searched", "search.finished", "release.grabbed"];
// The download bars' backstop when queue.progress and queue.changed move them.
const DOWNLOAD_FALLBACK_MS = 30_000;
// The old marks are dead weight in this browser now; drop them once.
try { localStorage.removeItem("arrmada.grabRequested"); } catch { /* storage unavailable */ }

export function SeriesDetail() {
  const { id } = useParams();
  const sid = Number(id);
  const [s, setS] = useState<SeriesT | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();

  // err shows the toast in the error colour: a search that failed says so plainly.
  const flash = useCallback((msg: string, err = false) => toast(msg, { tone: err ? "error" : "info", ms: 3500 }), [toast]);

  const load = useCallback(() => {
    return api.seriesDetail(sid).then(setS).catch((e: Error) => {
      if (e.message.toLowerCase().includes("not found")) setNotFound(true);
      else setError(e.message);
    });
  }, [sid]);

  useEffect(() => { load(); }, [load]);

  // How each episode file fits the show's profile's ideal file — report only.
  const [fits, setFits] = useState<Map<string, FitItem>>(new Map());
  useEffect(() => {
    api.libraryFitEpisodes(sid).then((r) => setFits(new Map((r.items ?? []).map((it) => [`${it.season}:${it.episode}`, it])))).catch(() => {});
  }, [sid]);

  // While any episode is downloading its bar moves from queue.progress (by the torrent's
  // hash), and a queue.changed naming one of its torrents reads the light downloads
  // endpoint — not the whole show. That endpoint is also the slow backstop (quick only
  // when an old grab has no hash to follow). The full detail reloads when the server
  // announces an import or a search for this show.
  const connected = useLiveConnected();
  const downloading = s ? downloadingCount(s) : 0;
  const { hashes: dlHashes, complete: allHashed } = useMemo(() => (s ? downloadHashes(s) : { hashes: [], complete: true }), [s]);
  const lastCount = useRef(0);
  const grabLook = useRef<number | undefined>(undefined);
  const pollDownloads = useCallback(() => {
    return api.seriesDownloads(sid).then((dls) => {
      // Without the websocket nothing announces the import, so a download leaving the
      // list (finished, failed or removed) is the cue to reload instead.
      if (!connected && dls.length < lastCount.current) load();
      lastCount.current = dls.length;
      setS((cur) => (cur ? mergeDownloads(cur, dls) : cur));
    }).catch(() => { /* the next tick tries again */ });
  }, [sid, load, connected]);
  useEffect(() => { lastCount.current = downloading; }, [downloading]);
  usePoll(pollDownloads, downloading > 0 ? (connected && allHashed ? DOWNLOAD_FALLBACK_MS : 3000) : null, { immediate: false });
  useLiveEvent([QUEUE_PROGRESS, QUEUE_CHANGED], (ev) => {
    if (queueChangedFor(ev, dlHashes)) { void pollDownloads(); return; }
    const p = progressByHash(ev);
    if (p) setS((cur) => (cur ? patchSeriesProgress(cur, p) : cur));
  }, downloading > 0);
  useLiveEvent(SERIES_TOPICS, (ev) => {
    const evID = (ev.data as { id?: number } | null)?.id;
    if ((ev.topic === "series.imported" || ev.topic === "series.searched") && evID === sid) {
      load();
      if (ev.topic === "series.searched") refreshSearches("series", sid); // the per-scope last-search lines
    } else if (searchFinishedFor(ev, "series", sid)) {
      // Any stored search of this show — the sweep's, RSS's, or a Grab pressed on another
      // device — updates the season and episode lines here, without a reload.
      refreshSearches("series", sid);
    } else if (ev.topic === "release.grabbed") {
      // The grab names no show, and the client takes a moment to list the torrent: look
      // twice, cheaply, rather than reload the whole page for every grab anywhere.
      void pollDownloads();
      window.clearTimeout(grabLook.current);
      grabLook.current = window.setTimeout(() => void pollDownloads(), 5000);
    }
  });
  useEffect(() => () => window.clearTimeout(grabLook.current), []);

  if (notFound) return <Shell><div className="py-10 text-center text-[13px] text-ink-dim">That series isn't in your library. <Link to="/series" className="underline" style={{ color: "var(--accent)" }}>Back to Series</Link></div></Shell>;
  if (!s) return <Shell><p className="text-[12.5px] text-ink-dim">{error ?? "Loading…"}</p></Shell>;

  const ex = s.extra;
  const seasons = sortSeasons(s.seasons);
  // Every regular episode, for the show-wide upgrade-hold chip.
  const allEps = seasons.filter((sn) => sn.season_number > 0).flatMap((sn) => sn.episodes ?? []);

  return (
    <>
      <PageHeader title={s.title} />

      <Hero series={s}>
        <HeroActions series={s} onChange={load} flash={flash} />
        <UpgradeHoldChip series={s} episodes={allEps} onChange={load} flash={flash} />
      </Hero>

      <div className="mx-auto w-full max-w-[1200px] px-4 pb-10 sm:px-6">
        <NumberingBanner seriesId={s.id} refreshKey={`${s.last_refreshed_at ?? ""}|${s.numbering_source ?? ""}`} onApplied={load} />
        {!s.monitored && (
          <div className="mt-2 rounded-lg px-3 py-2 text-[12px] text-ink-dim" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
            Series paused — nothing is searched. Season and episode choices below are kept for when you resume.
          </div>
        )}
        <div className="mt-2 flex flex-col gap-3">
          {seasons.map((sn) => <SeasonBlock key={sn.id} series={s} season={sn} onChange={load} flash={flash} defaultOpen={false} fits={fits} />)}
        </div>

        {/* The expert numbering panels sit under the episodes, collapsed: most shows never
            need them. */}
        <AliasPanel series={s} />
        {s.series_type === "anime" && <SceneMapPanel series={s} />}

        {/* A block isn't always a history event (a junk download is blocklisted as it
            leaves the queue), so the panel also reloads when a download goes away. */}
        <SeriesBlocklistPanel seriesId={s.id} refreshKey={`${s.last_event_id ?? 0}:${downloading}`} />

        <CastRow cast={ex?.cast} />

        <DuplicatesPanel seriesId={s.id} refreshKey={s.last_event_id} flash={flash} />

        <HistoryPanel seriesId={s.id} refreshKey={s.last_event_id} />
      </div>
    </>
  );
}

function CastRow({ cast }: { cast?: CastMember[] }) {
  if (!cast || cast.length === 0) return null;
  return (
    <div className="mt-8">
      <h2 className="m-0 mb-3 text-[14px] font-bold">Cast</h2>
      <div className="thin-scroll flex gap-3 overflow-x-auto pb-2">
        {cast.map((c, i) => (
          <div key={i} className="w-[92px] flex-none text-center">
            <div className="mb-1.5 overflow-hidden rounded-lg" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }}>
              {c.profile_url ? <img src={c.profile_url} alt={c.name} className="h-full w-full object-cover" loading="lazy" /> : null}
            </div>
            <div className="truncate text-[11px] font-semibold" title={c.name}>{c.name}</div>
            {c.character && <div className="truncate text-[10px] text-ink-faint" title={c.character}>{c.character}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}

// UpgradeHoldChip shows when episodes of this show were kept as they are when its profile
// changed ("keep existing files"), with Resume for all of them; each season has its own
// Resume too. A hold nobody can see is a file that silently never upgrades.
function UpgradeHoldChip({ series, episodes, onChange, flash }: { series: SeriesT; episodes: Episode[]; onChange: () => void; flash: (m: string, err?: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const held = episodes.filter((e) => e.has_file && e.upgrade_hold).length;
  if (held === 0) return null;
  const resume = async () => {
    setBusy(true);
    try {
      await api.resumeSeriesUpgrades(series.id);
      flash("Upgrades resumed — the next sweep can replace these episodes again.");
      onChange();
    } catch (e) {
      flash((e as Error).message, true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 text-[11.5px] text-ink-dim">
      <StatusChip tone="accent">Upgrades paused</StatusChip>
      <span>{held} episode{held === 1 ? "" : "s"} kept when the profile changed</span>
      <Button size="sm" variant="secondary" onClick={resume} busy={busy} busyLabel="Resuming…">Resume</Button>
    </div>
  );
}


function SeasonBlock({ series, season, onChange, flash, defaultOpen, fits }: { series: SeriesT; season: Season; onChange: () => void; flash: (m: string) => void; defaultOpen: boolean; fits: Map<string, FitItem> }) {
  const [open, setOpen] = useState(defaultOpen);
  const [searching, setSearching] = useState(false);
  const [busy, setBusy] = useState(false);
  // The season's Grab missing runs as a job; its stored attempt says how it went.
  const [grabJob, setGrabJob] = useState<number | null>(null);
  const grabbing = useJob(grabJob, {
    onDone: (j) => { setGrabJob(null); if (jobFailed(j)) flash(jobToast(j)); refreshSearches("series", series.id); onChange(); },
  });
  const last = latestFor(useSearchAttempts("series", series.id), seasonScope(season.season_number));
  const eps = season.episodes ?? [];
  const have = eps.filter((e) => e.has_file).length;
  const total = eps.length;
  const airedCount = eps.filter((e) => aired(e)).length;
  // The progress denominator counts what's wanted — aired episodes monitored in a monitored
  // season — plus what's already here, so an in-progress season isn't behind on episodes
  // that haven't come out yet, nor on ones nobody asked for (shown greyed instead).
  const wanted = (e: Episode) => e.monitored && season.monitored && aired(e);
  const counted = eps.filter((e) => e.has_file || wanted(e)).length;
  const notMonitored = eps.filter((e) => !e.has_file && aired(e) && !wanted(e)).length;
  const nextAir = eps.map((e) => e.air_date).filter(Boolean).sort()[0];
  // The on-disk folder for this season = the directory of any episode file we have.
  const seasonDir = (eps.find((e) => e.file_path)?.file_path ?? "").replace(/[\\/][^\\/]*$/, "");
  const state: "unreleased" | "upcoming" | "normal" = total === 0 ? "unreleased" : airedCount === 0 ? "upcoming" : "normal";
  const name = season.season_number === 0 ? "Specials" : `Season ${season.season_number}`;
  const pct = counted ? Math.round((have / counted) * 100) : 0;
  // Grab missing only fills gaps, so it's offered only while there is one: an aired,
  // monitored episode with no file. On a full season it could only fetch a pack that the
  // import gate then throws away. Never on Specials: they have no packs, so they're grabbed
  // one special at a time from their own rows.
  const anyMissing = season.season_number > 0 && eps.some((e) => !e.has_file && e.monitored && aired(e));
  const held = eps.filter((e) => e.has_file && e.upgrade_hold).length;
  const resumeSeason = async () => {
    setBusy(true);
    try {
      await api.resumeSeriesUpgrades(series.id, season.season_number);
      flash(`Upgrades resumed for ${name}.`);
      onChange();
    }
    catch (e) { flash((e as Error).message); }
    finally { setBusy(false); }
  };

  const grabSeason = async () => {
    setBusy(true);
    try {
      const r = await api.autoGrabSeries(series.id, season.season_number, 0);
      if (r.job_id) setGrabJob(r.job_id);
      flash(`Searching for ${name}'s missing episodes…`);
    }
    catch (e) { flash((e as Error).message); }
    finally { setBusy(false); }
  };
  const searchingNow = busy || grabbing.running;

  return (
    <div className="overflow-hidden rounded-xl" style={{ background: "var(--panel)", border: "1px solid var(--line)", opacity: state === "unreleased" ? 0.7 : 1 }}>
      <div className="flex items-center gap-3 px-4 py-3">
        <button onClick={() => state !== "unreleased" && setOpen((o) => !o)} className="flex min-w-0 flex-1 items-center gap-2 text-left" style={{ cursor: state === "unreleased" ? "default" : "pointer" }}>
          <span className="font-mono text-[13px] text-ink-faint">{state === "unreleased" ? " " : open ? "▾" : "▸"}</span>
          <span className="text-[13.5px] font-semibold">{name}</span>
          {state === "unreleased" ? (
            <span className="rounded px-1.5 py-0.5 font-mono text-[9.5px] uppercase" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }}>Not yet released</span>
          ) : state === "upcoming" ? (
            <span className="rounded px-1.5 py-0.5 font-mono text-[9.5px] uppercase" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }} title={nextAir ? `First airs ${nextAir}` : undefined}>{nextAir ? `Airs ${nextAir}` : "Upcoming"}</span>
          ) : (
            <span className="flex-none font-mono text-[10.5px] text-ink-faint">
              {have}/{counted}
              {notMonitored > 0 && <span style={{ opacity: 0.7 }} title="Aired episodes without a file that aren't monitored — not counted as missing"> · {notMonitored} not monitored</span>}
            </span>
          )}
          {seasonDir && <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-ink-faint" title={seasonDir}>{seasonDir}</span>}
        </button>
        {state !== "unreleased" && (
          <div className="h-1.5 w-24 overflow-hidden rounded-full" style={{ background: "var(--line)" }}>
            <div className="h-full rounded-full" style={{ width: `${pct}%`, background: have >= counted && counted > 0 ? "var(--good)" : "var(--accent)" }} />
          </div>
        )}
        {held > 0 && (
          <>
            <StatusChip tone="accent" title={`${held} episode${held === 1 ? "" : "s"} kept as they were when the profile changed`}>Upgrades paused</StatusChip>
            <Button size="sm" variant="secondary" onClick={resumeSeason} disabled={busy} title={`Let upgrades replace ${name}'s kept episodes again`}>Resume</Button>
          </>
        )}
        {state !== "unreleased" && (
          <>
            {anyMissing && <button
              onClick={grabSeason}
              disabled={searchingNow}
              title={last ? `${attemptLine(last)}. Click to search again.` : `Grabs ${name}'s missing episodes — a season pack when most of the season is missing or no single episodes exist`}
              className="rounded-lg px-2.5 py-1 text-[11px] font-semibold"
              style={{ border: "1px solid var(--accent-line)", color: "var(--accent)" }}
            >
              {searchingNow ? "Searching…" : last ? "Grab again" : "Grab missing"}
            </button>}
            <button onClick={() => setSearching(true)} title={`Search indexers for ${name}`} className="rounded-lg px-2.5 py-1 text-[11px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Search</button>
          </>
        )}
        <button onClick={async () => { await api.setSeasonMonitored(series.id, season.season_number, !season.monitored); onChange(); }} title={series.monitored ? "Monitor this whole season" : "Series paused — nothing is searched. This choice applies when you resume."} className="rounded-lg px-2.5 py-1 text-[11px] font-semibold" style={{ border: `1px solid ${season.monitored ? "var(--accent-line)" : "var(--line)"}`, color: season.monitored ? "var(--accent)" : "var(--ink-faint)", opacity: series.monitored ? 1 : 0.5 }}>
          {season.monitored ? "Monitored" : "Monitor"}
        </button>
      </div>
      {anyMissing && last && !searchingNow && (
        <div className="px-4 pb-2 text-[11px]" style={{ color: outcomeTone(last.outcome) }}>{attemptLine(last)}</div>
      )}
      {open && total > 0 && (
        <div style={{ borderTop: "1px solid var(--line)" }}>
          {eps.map((e) => <EpisodeRow key={e.id} series={series} ep={e} onChange={onChange} flash={flash} fit={fits.get(`${e.season_number}:${e.episode_number}`)} />)}
        </div>
      )}
      {searching && (
        <ReleaseSearchModal
          title={`${series.title} — ${name}`}
          subtitle={season.season_number === 0 ? "Releases tagged S00 for this show." : "Season packs and episodes for this season."}
          fetchReleases={() => api.seriesReleases(series.id, season.season_number)}
          onGrab={async (rel) => { await api.grabSeries(series.id, { token: rel.token ?? "" }); onChange(); }}
          onClose={() => setSearching(false)}
        />
      )}
    </div>
  );
}

function EpisodeRow({ series, ep, onChange, flash, fit }: { series: SeriesT; ep: Episode; onChange: () => void; flash: (m: string) => void; fit?: FitItem }) {
  const [searching, setSearching] = useState(false);
  const [showFile, setShowFile] = useState(false);
  const [busy, setBusy] = useState(false);
  const [grabJob, setGrabJob] = useState<number | null>(null);
  const grabbing = useJob(grabJob, {
    onDone: (j) => { setGrabJob(null); if (jobFailed(j)) flash(jobToast(j)); refreshSearches("series", series.id); onChange(); },
  });
  const last = latestFor(useSearchAttempts("series", series.id), sxe(ep));
  const dl = !ep.has_file && ep.download ? ep.download : null;
  const dlPct = dl ? Math.round(dl.progress * 100) : 0;
  // A paused series searches nothing, so its episodes aren't Wanted either.
  const status: { label: string; color: string } = ep.has_file || (!dl && aired(ep))
    ? libraryStatus({ hasFile: ep.has_file, monitored: ep.monitored && series.monitored })
    : dl
      ? { label: `↓ ${dlPct}%`, color: "var(--accent-text)" }
      : { label: "Unaired", color: "var(--ink-faint)" };

  const grabEp = async () => {
    setBusy(true);
    try {
      const r = await api.autoGrabSeries(series.id, ep.season_number, ep.episode_number);
      if (r.job_id) setGrabJob(r.job_id);
      flash(`Searching for ${sxe(ep)}…`);
    }
    catch (e) { flash((e as Error).message); }
    finally { setBusy(false); }
  };
  const replaceEp = async () => {
    // Replace blocklists a release and downloads another one in the background, so it asks
    // first — and says how far it reaches: a pack it lands only overrides this episode.
    if (!window.confirm(`Blocklist the current release of ${sxe(ep)} and download a different one? Only this episode's file is replaced.`)) return;
    setBusy(true);
    try { await api.regrabEpisode(series.id, ep.season_number, ep.episode_number); flash(`Replacing ${sxe(ep)} — blocklisted the current release, searching…`); }
    catch (e) { flash((e as Error).message); }
    finally { setBusy(false); }
  };
  // Asks first, and says truthfully where the file goes. A bin refusal shows in the dialog.
  const [confirmDel, setConfirmDel] = useState(false);
  const [delErr, setDelErr] = useState<string | null>(null);
  const binMode = useRecycleMode();
  const deleteEpFile = async () => {
    setBusy(true); setDelErr(null);
    try { await api.deleteEpisodeFile(series.id, ep.season_number, ep.episode_number); setConfirmDel(false); flash(`Deleted ${sxe(ep)} file`); onChange(); }
    catch (e) { setDelErr((e as Error).message); }
    finally { setBusy(false); }
  };

  return (
    <div className="relative flex items-center gap-3 px-4 py-2.5" style={{ borderBottom: "1px solid var(--line-soft)", opacity: ep.monitored ? 1 : 0.55 }}>
      <button onClick={async () => { await api.setEpisodeMonitored(ep.id, !ep.monitored); onChange(); }} title={!series.monitored ? "Series paused — nothing is searched. This choice applies when you resume." : ep.monitored ? "Monitored — click to stop" : "Not monitored — click to monitor"} className="flex-none text-[13px]" style={{ color: ep.monitored ? "var(--accent)" : "var(--ink-faint)", opacity: series.monitored ? 1 : 0.5 }}>
        {ep.monitored ? "◉" : "○"}
      </button>
      <span className="w-[64px] flex-none font-mono text-[11px] text-ink-faint">{sxe(ep)}</span>
      {series.series_type === "anime" && ep.absolute_number ? (
        <span className="flex-none rounded px-1 font-mono text-[10px]" style={{ background: "var(--accent-soft)", color: "var(--accent)" }} title="Absolute episode number">#{ep.absolute_number}</span>
      ) : null}
      {ep.has_file && ep.file_path ? (
        <button
          onClick={() => setShowFile(true)}
          title="Show this file's details — media info and the release it came from"
          className="min-w-0 flex-1 truncate text-left text-[12.5px] hover:underline"
          style={{ color: "var(--ink)" }}
        >
          {ep.title || "TBA"}
        </button>
      ) : (
        <span className="min-w-0 flex-1 truncate text-[12.5px]">{ep.title || "TBA"}</span>
      )}
      <span className="hidden w-[92px] flex-none font-mono text-[10.5px] text-ink-faint sm:block">{ep.air_date || "—"}</span>
      <span className="w-[80px] flex-none text-right font-mono text-[10px] uppercase" style={{ color: status.color }}>{status.label}</span>
      {ep.has_file && fit?.fit && (
        <span className="hidden w-[76px] flex-none text-right sm:block">
          <FitBadge item={fit} />
          <span className="ml-1 font-mono text-[10px] text-ink-faint">{fit.facts.bitrate_mbps ? fit.facts.bitrate_mbps.toFixed(1) : ""}</span>
        </span>
      )}
      <div className="flex flex-none items-center gap-1">
        {ep.has_file ? (<>
          <button onClick={replaceEp} disabled={busy} title="Blocklist this release and grab a different one" className="rounded-md px-2 py-1 text-[10.5px] font-semibold" style={{ border: "1px solid var(--accent-line)", color: "var(--accent)" }}>{busy ? "…" : "Replace"}</button>
          <button onClick={() => setConfirmDel(true)} disabled={busy} title="Delete this episode's file" className="rounded-md px-2 py-1 text-[10.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--reject)" }}>Delete</button>
        </>) : !dl && aired(ep) && (
          // The last search of this episode (stored server-side) says what it found, so a
          // grab that turned up nothing isn't forgotten. Still clickable: asking again after a
          // release shows up is the normal next move. Unaired episodes get none: there's
          // nothing to grab yet, and Search still reaches them.
          <button
            onClick={grabEp}
            disabled={busy || grabbing.running}
            title={last ? `${attemptLine(last)}. Click to search again.` : "Auto-grab the best release for this episode"}
            className="rounded-md px-2 py-1 text-[10.5px] font-semibold"
            style={last
              ? { border: "1px solid var(--line)", background: "var(--panel-2)", color: outcomeTone(last.outcome) }
              : { border: "1px solid var(--accent-line)", color: "var(--accent)" }}
          >
            {busy || grabbing.running ? "…" : last ? "Grab again" : "Grab"}
          </button>
        )}
        <button onClick={() => setSearching(true)} title="Search indexers for this episode" className="rounded-md px-2 py-1 text-[10.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Search</button>
      </div>
      {confirmDel && (
        <ConfirmDialog
          title={<>Delete the file for {sxe(ep)}?</>}
          body={<>{disposalLine(ep.size_bytes ?? 0, binMode)}, with its subtitles. The episode becomes wanted again.</>}
          confirmLabel="Delete file"
          busyLabel="Deleting…"
          busy={busy}
          error={delErr}
          onConfirm={deleteEpFile}
          onCancel={() => { setConfirmDel(false); setDelErr(null); }}
        />
      )}
      {dl && (
        <div className="absolute inset-x-0 bottom-0 h-[2px]" style={{ background: "var(--panel-2)" }}>
          <div className="h-full" style={{ width: `${dlPct}%`, background: "var(--accent)", transition: "width 1s linear" }} />
        </div>
      )}
      {searching && (
        <ReleaseSearchModal
          title={`${series.title} — ${sxe(ep)}`}
          subtitle={ep.title || undefined}
          fetchReleases={() => api.seriesReleases(series.id, ep.season_number, ep.episode_number)}
          onGrab={async (rel) => { await api.grabSeries(series.id, { token: rel.token ?? "" }); onChange(); }}
          onClose={() => setSearching(false)}
        />
      )}
      {showFile && ep.file_path && (
        <FileDetailsModal
          path={ep.file_path}
          title={`${series.title} — ${sxe(ep)}`}
          subtitle={ep.title || undefined}
          onCleanTracks={async () => { await api.convertRequest(`episode:${series.id}:${ep.season_number}:${ep.episode_number}`); }}
          onClose={() => setShowFile(false)}
        />
      )}
    </div>
  );
}


function Shell({ children }: { children: React.ReactNode }) {
  return <><PageHeader title="Series" /><div className="mx-auto w-full max-w-[1200px] px-4 py-6 sm:px-6">{children}</div></>;
}


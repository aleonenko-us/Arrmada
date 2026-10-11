import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { NumberingBanner } from "./series/NumberingReviewModal";
import { usePoll } from "../lib/usePoll";
import { useLiveConnected, useLiveEvent } from "../lib/live";
import { QUEUE_CHANGED, QUEUE_PROGRESS, progressByHash, queueChangedFor } from "../lib/queueLive";
import { downloadHashes, downloadingCount, mergeDownloads, patchSeriesProgress } from "./series/downloads";
import { searchFinishedFor } from "../lib/searchOutcome";
import { refreshSearches } from "../components/LastSearch";
import { Button, StatusChip, useToast } from "../ui";
import { api, type CastMember, type FitItem, type Series as SeriesT, type Episode } from "../lib/api";
import { Hero } from "./series/Hero";
import { HeroActions } from "./series/HeroActions";
import { AliasPanel, DuplicatesPanel, HistoryPanel, SceneMapPanel, SeriesBlocklistPanel } from "./series/panels";
import { defaultSeason, sortSeasons } from "./series/util";
import { SeasonPanel, SeasonRail } from "./series/SeasonRail";

// What the page listens for besides the queue (see SeriesDetail).
const SERIES_TOPICS = ["series.imported", "series.searched", "search.finished", "release.grabbed"];
// The download bars' backstop when queue.progress and queue.changed move them.
const DOWNLOAD_FALLBACK_MS = 30_000;
// The old marks are dead weight in this browser now; drop them once.
try { localStorage.removeItem("arrmada.grabRequested"); } catch { /* storage unavailable */ }

export function SeriesDetail() {
  const { id } = useParams();
  const sid = Number(id);
  const [params, setParams] = useSearchParams();
  // The season lives in the address (?season=), replaced rather than pushed: Back leaves
  // the show instead of stepping through every season looked at.
  const pickSeason = useCallback((n: number) => {
    setParams((p) => { const q = new URLSearchParams(p); q.set("season", String(n)); return q; }, { replace: true });
  }, [setParams]);
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
  // The season shown: the one in the address (so a reload stays put), else where there's
  // something to do (see defaultSeason).
  const asked = Number(params.get("season"));
  const current = seasons.find((sn) => params.has("season") && sn.season_number === asked) ?? seasons.find((sn) => sn.season_number === defaultSeason(seasons));

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
        <div className="mt-4">
          <SeasonRail seasons={seasons} selected={current?.season_number} onSelect={pickSeason} />
          {current && <SeasonPanel key={current.season_number} series={s} season={current} fits={fits} onChange={load} flash={flash} />}
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


function Shell({ children }: { children: React.ReactNode }) {
  return <><PageHeader title="Series" /><div className="mx-auto w-full max-w-[1200px] px-4 py-6 sm:px-6">{children}</div></>;
}


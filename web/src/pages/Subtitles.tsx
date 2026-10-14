import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { PageHeader } from "../components/PageHeader";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { useTabParam } from "../lib/useTabParam";
import { useSearchParamState } from "../lib/useSearchState";
import { usePoll } from "../lib/usePoll";
import { TabPanel, Tabs, type TabItem } from "../ui/Tabs";
import { LINKS } from "../lib/links";
import { RescanButton, scanTitle } from "../components/RescanButton";
import { api, type SubtitleSettings, type SubFileEntry, type SubSeriesGroup, type SubtitleJob, type SubtitleCoverage, type SubLangStatus, type SubtitleProviderState } from "../lib/api";
import { HealthCell } from "./subtitles/health";
import { RedoChip } from "./subtitles/redo";
import { AttentionCard } from "./subtitles/attention";
import { KeptTracksNote, RowPoster, card, cardStyle } from "./subtitles/shared";
import { langName, useLanguages } from "../lib/languages";
import { ProfileSelect } from "./subtitles/ProfileSelect";
import { bulkSummary, orphanApi } from "./subtitles/orphanApi";
import { useConfirm, useToast } from "../ui";
import { ACTIVE, ActiveRow, Queue, backlogText, sortActive } from "./subtitles/queue";
import { queueApi, type SubtitleQueue } from "./subtitles/queueApi";
import { LazySubtitleDrawer } from "../components/subtitles/LazySubtitleDrawer";
import type { SubMediaRef } from "../lib/subtitleFileApi";
import { Page } from "../ui/Page";

// The settings live in Settings → Subtitles; this page is the working view.
type Tab = "overview" | "queue" | "library" | "logs";
const TAB_KEYS: readonly Tab[] = ["overview", "queue", "library", "logs"];

const SOURCE_LABEL: Record<string, string> = { extract: "extract", ocr: "OCR", download: "download", ai: "AI" };
// Why a missing language is backing off, and what brings it back early.
const REASON_LABEL: Record<string, string> = {
  auth_failed: "OpenSubtitles login rejected", model_missing: "no whisper model for it", quota: "OpenSubtitles quota spent",
  rate_limited: "OpenSubtitles rate limit", provider_error: "OpenSubtitles error", ai_failed: "AI failed",
  out_of_sync: "only out-of-sync matches", below_score: "only poor matches", no_match: "no OpenSubtitles match",
  impossible: "whisper can't make this language from the audio", no_audio: "no audio", ocr_needed: "needs OCR",
};
const RETRY_ON_LABEL: Record<string, string> = { creds: "login", model: "a model", langs: "a language change" };
// OpenSubtitles' state in a word or two, for the header pill.
const PROVIDER_STATE_LABEL: Record<SubtitleProviderState, string> = {
  not_configured: "not set up", needs_account: "search only", unverified: "not checked yet", ok: "logged in",
  auth_failed: "login rejected", quota_spent: "quota used", rate_limited: "rate-limited",
};

const lbl = "font-mono text-2xs font-bold uppercase tracking-[0.11em] text-ink-faint";

export function Subtitles() {
  // The old Settings tab's address (bookmarks, earlier copy) opens the settings in the hub.
  const [params] = useSearchParams();
  if (params.get("tab") === "settings") return <Navigate to={LINKS.subtitlesSettings} replace />;
  return <SubtitlesPage />;
}

function SubtitlesPage() {
  const navigate = useNavigate();
  const [tab, setTab] = useTabParam(TAB_KEYS, "overview");
  const [settings, setSettings] = useState<SubtitleSettings | null>(null);
  const [jobs, setJobs] = useState<SubtitleJob[]>([]);
  const [backlog, setBacklog] = useState<SubtitleQueue | null>(null);
  const toast = useToast();
  const say = useCallback((m: string) => toast(m, { ms: 3500 }), [toast]);

  const loadSettings = useCallback(() => api.subtitleSettings().then(setSettings).catch(() => {}), []);
  useEffect(() => { loadSettings(); }, [loadSettings]);

  // The live list carries the AI backlog (files, audio, ETA) along with the jobs.
  const loadJobs = useCallback(() => queueApi.live().then((q) => { setJobs(q.jobs); setBacklog(q); }).catch(() => {}), []);
  const anyActive = jobs.some((j) => ACTIVE.has(j.state));
  usePoll(loadJobs, anyActive ? 1500 : 4000);

  const ensureAll = async () => {
    try {
      const [m, tv] = await Promise.all([api.subtitleSweep("movies"), api.subtitleSweep("tv")]);
      const n = m.queued + tv.queued;
      say(n ? `Queued ${n} file${n === 1 ? "" : "s"} missing subtitles…` : "Everything already has its subtitles. 🎉");
      setTab("queue");
      void loadJobs();
    } catch (e) { toast((e as Error).message, { tone: "error", ms: 3500 }); }
  };

  const activeCount = jobs.filter((j) => ACTIVE.has(j.state)).length;
  // The pill lists the sources that work right now, read from real state: embedded tracks
  // always do; OpenSubtitles by what it last said (green only after a login worked), AI
  // once a model is installed. A rejected login turns it red.
  const ps = settings?.provider_state;
  const sources = ["embedded"];
  if (ps && ps !== "not_configured") sources.push(ps === "ok" ? "OpenSubtitles" : `OpenSubtitles (${PROVIDER_STATE_LABEL[ps]})`);
  if (settings?.ai_ready) sources.push("AI");
  const provider = `Sources: ${sources.join(" · ")}`;
  const pillTone = ps === "auth_failed" ? "reject"
    : ps === "quota_spent" || ps === "rate_limited" || ps === "unverified" ? "avoid"
    : ps === "ok" || settings?.ai_ready ? "good" : null;
  // The Queue tab counts the jobs working right now; nothing shows while it's idle.
  const TABS: TabItem<Tab>[] = [
    { key: "overview", label: "Overview" },
    { key: "queue", label: "Queue", count: activeCount || undefined },
    { key: "library", label: "Library" },
    { key: "logs", label: "Logs" },
  ];

  return (
    <>
      <PageHeader title="Subtitles" />
      <Page>
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <p className="max-w-[64ch] text-base text-ink-dim">One external <code>.srt</code> per language next to every video, made from the best source available: an embedded text track, an OpenSubtitles download, or local AI transcription when a model is installed. Pick languages in <Link to={LINKS.subtitlesSettings} style={{ color: "var(--accent-text)" }}>Settings</Link>.</p>
          <div className="flex items-center gap-2">
            <span className="inline-flex items-center gap-2 rounded-full px-3 py-1.5 text-sm font-semibold" title={settings?.provider_detail || undefined} style={{ border: `1px solid ${pillTone ? `var(--${pillTone})` : "var(--line)"}`, background: pillTone ? `var(--${pillTone}-soft)` : "var(--panel-2)" }}>
              <span className="h-2 w-2 rounded-full" style={{ background: pillTone ? `var(--${pillTone})` : "var(--ink-faint)" }} />
              {provider}
            </span>
            <Link to={LINKS.subtitlesSettings} className="rounded-lg px-3 py-2 text-base font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>Configure →</Link>
            <button onClick={ensureAll} className="rounded-lg px-3.5 py-2 text-base font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>Ensure all</button>
          </div>
        </div>

        <Tabs tabs={TABS} value={tab} onChange={setTab} idPrefix="subtitles" label="Subtitles sections" />

        <TabPanel idPrefix="subtitles" value={tab}>
          {tab === "overview" && <Overview jobs={jobs} backlog={backlog} settings={settings} flash={say} onSettings={() => navigate(LINKS.subtitlesSettings)}
            onLibrary={() => { setTab("library"); say("Pick the Orphaned filter to adopt or replace them."); }}
            onQueued={() => { void loadJobs(); }} />}
          {tab === "queue" && <Queue jobs={jobs} backlog={backlog} onChange={loadJobs} flash={say} />}
          {tab === "library" && <Library flash={say} onQueued={loadJobs} />}
          {tab === "logs" && <LogsConsole />}
        </TabPanel>
      </Page>
    </>
  );
}

/* ============================= OVERVIEW ============================= */
function Overview({ jobs, backlog, settings, flash, onSettings, onLibrary, onQueued }: {
  jobs: SubtitleJob[]; backlog: SubtitleQueue | null; settings: SubtitleSettings | null; flash: (m: string) => void; onSettings: () => void;
  onLibrary: () => void; onQueued: () => void;
}) {
  // The totals come from the server's last library pass. This used to fetch the flat
  // list of every file — a directory listing per file, 25,000 of them — every time the
  // tab opened.
  const [cov, setCov] = useState<SubtitleCoverage | null>(null);
  const [busy, setBusy] = useState(false);
  const languages = useLanguages();
  const load = useCallback(() => api.subtitleCoverage().then(setCov).catch(() => {}), []);
  useEffect(() => { load(); }, [load]);
  // While a pass is running (or a recount for new languages is due), keep asking until it lands.
  usePoll(load, cov?.scanning || cov?.stale ? 3000 : null, { immediate: false });
  const rescan = async () => {
    setBusy(true);
    try { const r = await api.subtitleRescan(); flash(r.started ? "Rescanning the library…" : "A scan is already running."); await load(); }
    catch (e) { flash(e instanceof Error ? e.message : "Couldn't start a scan"); }
    finally { setBusy(false); }
  };
  const active = sortActive(jobs);
  const ai = backlogText(backlog);
  const scanned = !!cov && cov.scanned_at > 0;
  // Figures counted for the old kept languages would be wrong, so they aren't shown.
  const recounting = !!cov?.stale;
  const pct = scanned && cov.files ? Math.round((cov.covered / cov.files) * 100) : 0;

  return (
    <div className="flex flex-col gap-3.5">
      <div className="grid gap-3.5" style={{ gridTemplateColumns: "1.1fr 1fr 1fr" }}>
        <div className={card} style={cardStyle}>
          <div className="flex items-center justify-between">
            <div className={lbl}>Coverage</div>
            <RescanButton busy={busy || !!cov?.scanning} onClick={rescan} title={scanTitle(cov)} />
          </div>
          <div className="mt-2 text-display font-extrabold tracking-tight">{scanned && !recounting ? `${pct}%` : "…"}</div>
          <div className="mt-3 border-t pt-3 text-sm text-ink-dim" style={{ borderColor: "var(--line-soft)" }} role="status">
            {recounting
              ? "Recounting for your new languages…"
              : scanned
              ? <><b style={{ color: "var(--good-text)" }}>{cov.covered.toLocaleString()}</b> fully subtitled · <b style={{ color: cov.missing ? "var(--avoid-text)" : "var(--ink)" }}>{cov.missing.toLocaleString()}</b> missing a language · {cov.files.toLocaleString()} files</>
              : "Scanning your library — the first pass after startup takes a few minutes."}
          </div>
        </div>
        <div className={card} style={cardStyle}>
          <div className={lbl}>Kept languages</div>
          <div className="mt-2.5 flex flex-wrap gap-1.5">
            {(settings?.languages ?? []).map((c) => <span key={c} className="rounded-full px-2.5 py-1 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>{langName(c, languages)}</span>)}
          </div>
          <div className="mt-3 text-sm text-ink-faint">Each of these gets an external <code>.srt</code>. <KeptTracksNote /></div>
        </div>
        <div className={card} style={cardStyle}>
          <div className={lbl}>Sources &amp; automation</div>
          <div className="mt-2 flex flex-col gap-1.5 text-sm text-ink-dim">
            <Row2 label="Embedded extract" state="on" />
            <Row2 label="OpenSubtitles download" state={providerRow(settings?.provider_state)} onClick={onSettings} />
            <Row2 label={`AI transcription${settings?.ai_ready && settings.ai_backend ? ` (${settings.ai_backend === "cpu" ? "CPU" : "GPU"})` : ""}`} state={settings?.ai_ready ? "on" : "model"} onClick={onSettings} />
            <Row2 label="Image-sub OCR" state="unsupported" />
          </div>
          <div className="mt-2.5 border-t pt-2.5 text-sm text-ink-faint" style={{ borderColor: "var(--line-soft)" }}>
            Auto: movies {settings?.movies_auto ? "on" : "off"} · series {settings?.series_auto ? "on" : "off"}
            {/* The daily upgrade swaps AI and loose downloads for subtitles made for the exact file. */}
            {settings?.can_download && (
              <div className="mt-1" title="Each day, recent AI-made subtitles and downloads not made for the exact file are searched again, and replaced when one made for this file appears.">
                {upgradedLine(settings.upgraded_week ?? 0)}
              </div>
            )}
          </div>
        </div>
      </div>

      <AttentionCard flash={flash} onLibrary={onLibrary} onQueued={onQueued} />

      {active.length > 0 && (
        <div className={card} style={cardStyle}>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <div className="text-md font-bold">Working now</div>
            {/* Whisper works through its own lane; this says how far behind it is. */}
            {ai && <div className="font-mono text-xs text-ink-faint">{ai}</div>}
          </div>
          <div className="mt-2 flex flex-col gap-1.5">{active.map((j) => <ActiveRow key={j.id} j={j} />)}</div>
        </div>
      )}
    </div>
  );
}
// Row2 is one source's real state. Something that needs setting up is a button to its
// settings; image-sub OCR isn't something Arrmada does, so it says so rather than "soon".
const ROW_LABEL = { on: "on", setup: "set up", model: "needs a model", unsupported: "not supported", rejected: "login rejected", paused: "paused", unchecked: "not checked" } as const;
// providerRow is OpenSubtitles' row: on only once a login has worked.
function providerRow(ps?: SubtitleProviderState): keyof typeof ROW_LABEL {
  switch (ps) {
    case "ok": return "on";
    case "auth_failed": return "rejected";
    case "quota_spent": case "rate_limited": return "paused";
    case "unverified": return "unchecked";
    default: return "setup";
  }
}
function Row2({ label, state, onClick }: { label: string; state: keyof typeof ROW_LABEL; onClick?: () => void }) {
  const on = state === "on";
  const chip = "rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase";
  const style = { background: on ? "var(--good-soft, rgba(127,176,105,.16))" : "var(--panel-2)", color: on ? "var(--good-text)" : "var(--ink-faint)" };
  return (
    <div className="flex items-center justify-between">
      <span>{label}</span>
      {!on && onClick
        ? <button onClick={onClick} className={chip} style={{ ...style, color: "var(--accent-text)", border: "1px solid var(--accent-line)" }}>{ROW_LABEL[state]}</button>
        : <span className={chip} style={style}>{ROW_LABEL[state]}</span>}
    </div>
  );
}

/* QUEUE: the Queue tab (and the rows the Overview shares) live in subtitles/queue.tsx. */
// upgradedLine is the week's count from the daily upgrade.
function upgradedLine(n: number): string {
  if (n === 0) return "No subtitles upgraded this week";
  return `${n.toLocaleString()} subtitle${n === 1 ? "" : "s"} upgraded this week`;
}

// retryIn is how far off a backoff's next try is: "in 6 days", "in 3 h", "in 10 min", or
// "soon" once it's due (the next sweep picks it up).
function retryIn(at: number): string {
  const sec = at - Date.now() / 1000;
  if (sec < 60) return "soon";
  if (sec < 90 * 60) return `in ${Math.round(sec / 60)} min`;
  if (sec < 36 * 3600) return `in ${Math.round(sec / 3600)} h`;
  const d = Math.round(sec / 86400);
  return `in ${d} day${d === 1 ? "" : "s"}`;
}

/* ============================= LIBRARY ============================= */
type SortKey = "title" | "missing" | "embedded";
function rowKey(f: SubFileEntry): string {
  return f.kind === "episode" ? `e:${f.series_id}:${f.season}:${f.episode}` : `m:${f.movie_id}`;
}
// refOf names a row's file for the per-file drawer.
function refOf(f: SubFileEntry): SubMediaRef {
  return f.kind === "episode"
    ? { kind: "episode", series_id: f.series_id!, season: f.season!, episode: f.episode! }
    : { kind: "movie", movie_id: f.movie_id! };
}
function embeddedKinds(f: SubFileEntry): { txt: boolean; pgs: boolean; vob: boolean } {
  let txt = false, pgs = false, vob = false;
  for (const t of f.embedded ?? []) {
    if (t.text) txt = true;
    else if (t.codec === "hdmv_pgs_subtitle") pgs = true;
    else if (t.codec === "dvd_subtitle") vob = true;
  }
  return { txt, pgs, vob };
}
type Filter = "missing" | "pgs" | "text" | "external" | "orphaned";
const orphanCount = (f: SubFileEntry) => (f.orphans ?? []).length;
const PAGE_ROWS = 100;
// The library list is for movies or shows (?media=tv).
const MEDIA_KINDS = ["movies", "tv"] as const;

function Library({ flash, onQueued }: { flash: (m: string) => void; onQueued: () => void }) {
  const [media, setMedia] = useSearchParamState<"movies" | "tv">("media", "movies", { allowed: MEDIA_KINDS });
  const [items, setItems] = useState<SubFileEntry[] | null>(null);
  const [scanning, setScanning] = useState(false); // no pass has completed yet (just after startup)
  const [stale, setStale] = useState(false); // counted for the old kept languages; a recount is under way
  const [refreshKey, setRefreshKey] = useState(0);
  const [rescanBusy, setRescanBusy] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [queued, setQueued] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<{ key: SortKey; dir: "asc" | "desc" }>({ key: "missing", dir: "desc" });
  const [filters, setFilters] = useState<Set<Filter>>(new Set());
  // The same search box as the TV list: title or year. The box updates at once; the
  // address follows after a pause.
  const [query, setQuery] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  // Rows are rendered a page at a time: a library of thousands stays quick to type into.
  const [shown, setShown] = useState(PAGE_ROWS);
  const [drawer, setDrawer] = useState<SubFileEntry | null>(null); // the file whose drawer is open

  const confirm = useConfirm();
  const confirmRedo = useConfirmRedo();
  useEffect(() => { setItems(null); setQueued(new Set()); setFilters(new Set()); }, [media]);
  // A reload (after a rescan or an adopt) keeps the filters, and the rows on screen until
  // the new list lands.
  useEffect(() => {
    // TV is rendered per show by TVLibrary, which fetches its own rolled-up list.
    if (media === "tv") return;
    api.subtitleLibrary().then((r) => { setItems(r.items); setScanning(!!r.scanning); setStale(!!r.stale); }).catch(() => setItems([]));
  }, [media, refreshKey]);
  // A finished sync check patched its movie on the server: reload in place.
  const reloadMovies = useCallback(() => setRefreshKey((k) => k + 1), []);
  // While the list is from the old languages, wait for the recount and then reload.
  usePoll(async () => {
    const c = await api.subtitleCoverage().catch(() => null);
    if (c && !c.stale && !c.scanning) { setStale(false); setRefreshKey((k) => k + 1); }
  }, stale && media === "movies" ? 3000 : null, { immediate: false });

  // Start a fresh pass and hold the spinner until it lands, then reload. Bounded so a
  // stalled pass can't pin the button.
  const rescan = async () => {
    setRescanBusy(true);
    try {
      const r = await api.subtitleRescan();
      flash(r.started ? "Rescanning the library…" : "A scan is already running — waiting for it.");
      for (let i = 0; i < 300; i++) {
        await new Promise((res) => setTimeout(res, 2000));
        const c = await api.subtitleCoverage().catch(() => null);
        if (c && !c.scanning) break;
      }
      setRefreshKey((k) => k + 1);
      flash("Library rescanned.");
    } catch (e) { flash((e as Error).message); } finally { setRescanBusy(false); }
  };

  const ensure = async (f: SubFileEntry, redo = false) => {
    if (redo && !(await confirmRedo(f.title))) return;
    const key = rowKey(f);
    setBusy(key);
    try {
      if (f.kind === "episode") await api.subtitleQueueEpisode(f.series_id!, f.season!, f.episode!, redo);
      else await api.subtitleQueueMovie(f.movie_id!, redo);
      setQueued((q) => new Set(q).add(key));
      flash(redo ? `Redoing “${f.title}”` : `Queued “${f.title}”`);
      onQueued();
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };

  // An orphaned subtitle (named for no video in the folder): adopt renames it to pair with
  // the movie; replace makes a fresh one and only then moves the orphan to the recycle bin.
  const orphanAction = async (f: SubFileEntry, name: string, action: "adopt" | "replace") => {
    if (action === "replace" && !(await confirm({
      title: `Replace ${name}?`,
      body: `Arrmada makes a fresh, properly named subtitle for ${f.title}. Once it's written, ${name} moves to the recycle bin; if nothing can be made, it stays where it is.`,
      confirmLabel: "Replace",
    }))) return;
    const key = rowKey(f);
    setBusy(key);
    try {
      if (action === "adopt") {
        const r = await orphanApi.adopt(f.movie_id!, name);
        flash(`Adopted — now ${r.name}`);
        setRefreshKey((k) => k + 1);
      } else {
        await orphanApi.replace(f.movie_id!, name);
        setQueued((q) => new Set(q).add(key));
        flash(`Queued a replacement for ${name}`);
        onQueued();
      }
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };
  // Adopt all / Replace all act on the orphaned movies in view.
  const bulkOrphans = async (action: "adopt" | "replace") => {
    const rows = view.filter((f) => orphanCount(f) > 0);
    const n = rows.reduce((sum, f) => sum + orphanCount(f), 0);
    const movies = `${rows.length} movie${rows.length === 1 ? "" : "s"}`;
    const ok = await confirm(action === "adopt" ? {
      title: `Adopt ${n} orphaned subtitle${n === 1 ? "" : "s"}?`,
      body: `Each is renamed to pair with its movie (${movies}), so Plex shows it. Any whose language isn't clear from its name, that shares a language with another orphan, or whose new name is taken is left for you to decide.`,
      confirmLabel: "Adopt all",
    } : {
      title: `Replace ${n} orphaned subtitle${n === 1 ? "" : "s"}?`,
      body: `Queues a fresh, properly named subtitle for ${movies}. Each orphan moves to the recycle bin once its replacement is written; if nothing can be made it stays. Orphans in languages you don't keep, and forced ones, are left alone.`,
      confirmLabel: "Replace all",
    });
    if (!ok) return;
    setBusy("bulk");
    try {
      const r = await orphanApi.bulk(action, rows.map((f) => f.movie_id!));
      flash(bulkSummary(action, r));
      setRefreshKey((k) => k + 1);
      if (action === "replace") onQueued();
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };

  const toggleF = (f: Filter) => setFilters((s) => { const n = new Set(s); n.has(f) ? n.delete(f) : n.add(f); return n; });
  const setSortKey = (key: SortKey) => setSort((s) => s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: key === "title" ? "asc" : "desc" });

  const counts = useMemo(() => {
    const c = { missing: 0, pgs: 0, text: 0, external: 0, orphaned: 0 };
    for (const f of items ?? []) {
      if (f.missing > 0) c.missing++;
      const k = embeddedKinds(f);
      if (k.pgs || k.vob) c.pgs++;
      if (k.txt) c.text++;
      if ((f.external ?? []).length > 0) c.external++;
      if (orphanCount(f) > 0) c.orphaned++;
    }
    return c;
  }, [items]);

  const view = useMemo(() => {
    const q = query.trim().toLowerCase();
    let list = q ? (items ?? []).filter((f) => `${f.title} ${f.year ?? ""}`.toLowerCase().includes(q)) : (items ?? []).slice();
    if (filters.has("missing")) list = list.filter((f) => f.missing > 0);
    if (filters.has("pgs")) list = list.filter((f) => { const k = embeddedKinds(f); return k.pgs || k.vob; });
    if (filters.has("text")) list = list.filter((f) => embeddedKinds(f).txt);
    if (filters.has("external")) list = list.filter((f) => (f.external ?? []).length > 0);
    if (filters.has("orphaned")) list = list.filter((f) => orphanCount(f) > 0);
    const dir = sort.dir === "asc" ? 1 : -1;
    const val = (f: SubFileEntry): number | string => {
      switch (sort.key) {
        case "title": return f.title.toLowerCase();
        case "missing": return f.missing;
        case "embedded": return (f.embedded ?? []).length;
      }
    };
    return list.sort((a, b) => {
      const va = val(a), vb = val(b);
      return typeof va === "string" || typeof vb === "string" ? String(va).localeCompare(String(vb)) * dir : (va - vb) * dir;
    });
  }, [items, filters, sort, query]);
  // A new search, filter or sort starts again from the first page.
  useEffect(() => { setShown(PAGE_ROWS); }, [filters, sort, query, media]);

  const noun = media === "tv" ? "episodes" : "movies";
  const filtering = filters.size > 0 || query.trim() !== "";
  const HEADERS: { label: string; key?: SortKey }[] = [
    { label: "Title", key: "title" }, { label: "Audio" }, { label: "Embedded", key: "embedded" },
    { label: "Coverage" }, { label: "Health" }, { label: "" },
  ];

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <div className="inline-flex w-fit rounded-lg p-0.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
          {(["movies", "tv"] as const).map((m) => (
            <button key={m} onClick={() => setMedia(m)} className="rounded-md px-3.5 py-1.5 text-sm font-semibold" style={{ background: media === m ? "var(--accent)" : "transparent", color: media === m ? "var(--accent-ink)" : "var(--ink-faint)" }}>
              {m === "movies" ? "Movies" : "TV Shows"}{items && media === m && m === "movies" ? <span className="ml-1.5 font-mono text-xs opacity-70">{items.length.toLocaleString()}</span> : null}
            </button>
          ))}
        </div>
        {/* The lists come from the server's last pass; this asks for a new one. */}
        <RescanButton busy={rescanBusy} onClick={rescan} title="Rescan the library for new files and subtitles" />
      </div>

      {stale && media === "movies" && (
        <div className="rounded-lg px-3 py-2 text-sm text-ink-dim" role="status" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
          Recounting for your new languages… this list updates when it finishes.
        </div>
      )}
      {media === "tv" ? (
        <TVLibrary key={refreshKey} flash={flash} onQueued={onQueued} />
      ) : items === null || (scanning && items.length === 0) ? (
        <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Scanning your {noun}…{items !== null && " The first pass after startup takes a few minutes."}</div>
      ) : items.length === 0 ? (
        <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>No downloaded {noun} yet.</div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1.5">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search movies…"
              aria-label="Search movies"
              className="mr-1 w-full rounded-lg px-3 py-1.5 text-base sm:w-[240px]"
              style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            />
            <Pill active={filters.has("missing")} disabled={!counts.missing} onClick={() => toggleF("missing")}>Missing <span className="opacity-60">{counts.missing}</span></Pill>
            <Pill active={filters.has("pgs")} disabled={!counts.pgs} onClick={() => toggleF("pgs")}>PGS/VobSub <span className="opacity-60">{counts.pgs}</span></Pill>
            <Pill active={filters.has("text")} disabled={!counts.text} onClick={() => toggleF("text")}>Embedded text <span className="opacity-60">{counts.text}</span></Pill>
            <Pill active={filters.has("external")} disabled={!counts.external} onClick={() => toggleF("external")}>Has external <span className="opacity-60">{counts.external}</span></Pill>
            <Pill active={filters.has("orphaned")} disabled={!counts.orphaned} onClick={() => toggleF("orphaned")}>Orphaned <span className="opacity-60">{counts.orphaned}</span></Pill>
            {filtering && <button onClick={() => { setFilters(new Set()); setQuery(""); }} className="ml-1 text-xs text-ink-faint underline hover:text-[var(--ink)]">clear</button>}
            {filters.has("orphaned") && view.some((f) => orphanCount(f) > 0) && (
              <span className="ml-auto flex items-center gap-1.5">
                <button type="button" onClick={() => bulkOrphans("adopt")} disabled={busy !== null} className="rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>Adopt all</button>
                <button type="button" onClick={() => bulkOrphans("replace")} disabled={busy !== null} className="rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Replace all</button>
              </span>
            )}
          </div>
          <p className="text-xs text-ink-faint">
            {filtering ? <><b style={{ color: "var(--ink)" }}>{view.length.toLocaleString()}</b> of {items.length.toLocaleString()} · </> : null}
            <b>Ensure subs</b> makes any missing kept-language <code>.srt</code> from the best available source.
          </p>
          <div className="overflow-x-auto rounded-xl" style={{ border: "1px solid var(--line)" }}>
            <table className="w-full border-collapse text-base" style={{ minWidth: 900 }}>
              <thead><tr style={{ background: "var(--panel-2)" }}>{HEADERS.map((h) => (
                <th key={h.label} onClick={h.key ? () => setSortKey(h.key!) : undefined}
                  className={`px-3 py-2 text-left font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint ${h.key ? "cursor-pointer select-none hover:text-[var(--ink)]" : ""}`}>
                  {h.label}{h.key && sort.key === h.key ? (sort.dir === "asc" ? " ▲" : " ▼") : ""}
                </th>
              ))}</tr></thead>
              <tbody>
                {view.slice(0, shown).map((f, i) => (
                  <SubRow
                    key={rowKey(f)}
                    f={f}
                    first={i === 0}
                    busy={busy}
                    queued={queued.has(rowKey(f))}
                    onEnsure={() => ensure(f)}
                    onRedo={() => ensure(f, true)}
                    flash={flash}
                    onChanged={reloadMovies}
                    onQueued={onQueued}
                    onOrphan={(name, action) => orphanAction(f, name, action)}
                    onProfileChanged={() => setRefreshKey((k) => k + 1)}
                    onOpen={() => setDrawer(f)}
                  />
                ))}
              </tbody>
            </table>
            {view.length === 0 && <div className="p-8 text-center text-base text-ink-dim">No movies match.</div>}
          </div>
          {view.length > shown && (
            <button type="button" onClick={() => setShown((n) => n + PAGE_ROWS)} className="self-center rounded-lg px-3.5 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>
              Show more <span className="font-mono text-xs text-ink-faint">{Math.min(PAGE_ROWS, view.length - shown)} of {(view.length - shown).toLocaleString()} left</span>
            </button>
          )}
        </>
      )}
      {drawer && <LazySubtitleDrawer media={refOf(drawer)} title={drawer.title} onClose={() => setDrawer(null)} onChanged={() => setRefreshKey((k) => k + 1)} />}
    </div>
  );
}
// TVLibrary lists SHOWS, not episodes.
//
// The flat list was one row per episode — 23,000 of them, each probed before the page
// could render. You cannot find a show in that, and it is slow for the same reason it
// is unusable. One row per show, a search box, and episodes probed only for the show
// you actually open.
function TVLibrary({ flash, onQueued }: { flash: (m: string) => void; onQueued: () => void }) {
  const [groups, setGroups] = useState<SubSeriesGroup[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [stale, setStale] = useState(false);
  const [reload, setReload] = useState(0);
  const [query, setQuery] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  const [onlyGaps, setOnlyGaps] = useState(false);
  const [open, setOpen] = useState<number | null>(null);

  useEffect(() => {
    api.subtitleSeriesGroups().then((r) => { setGroups(r.groups); setScanning(!!r.scanning); setStale(!!r.stale); }).catch(() => setGroups([]));
  }, [reload]);
  usePoll(async () => {
    const c = await api.subtitleCoverage().catch(() => null);
    if (c && !c.stale && !c.scanning) { setStale(false); setReload((k) => k + 1); }
  }, stale ? 3000 : null, { immediate: false });

  const view = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (groups ?? []).filter(
      (g) => (!q || g.title.toLowerCase().includes(q)) && (!onlyGaps || g.missing > 0),
    );
  }, [groups, query, onlyGaps]);

  const totals = useMemo(() => {
    let eps = 0, missing = 0, shows = 0;
    for (const g of groups ?? []) {
      eps += g.episodes;
      missing += g.missing;
      if (g.missing > 0) shows++;
    }
    return { eps, missing, shows };
  }, [groups]);

  if (groups === null || (scanning && groups.length === 0)) {
    return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Scanning your shows…{groups !== null && " The first pass after startup takes a few minutes."}</div>;
  }
  if (groups.length === 0) {
    return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>No downloaded episodes yet.</div>;
  }

  return (
    <div className="flex flex-col gap-2">
      {stale && (
        <div className="rounded-lg px-3 py-2 text-sm text-ink-dim" role="status" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
          Recounting for your new languages… this list updates when it finishes.
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search shows…"
          aria-label="Search shows"
          className="w-[240px] rounded-lg px-3 py-1.5 text-base"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
        />
        <Pill active={onlyGaps} onClick={() => setOnlyGaps((v) => !v)}>
          Needs subtitles <span className="opacity-60">{totals.shows}</span>
        </Pill>
        <span className="text-xs text-ink-faint">
          {groups.length.toLocaleString()} shows · {totals.eps.toLocaleString()} episodes ·{" "}
          <b style={{ color: totals.missing ? "var(--avoid-text)" : "var(--good-text)" }}>{totals.missing.toLocaleString()}</b> missing a kept language
        </span>
      </div>

      <div className="overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)" }}>
        {view.length === 0 ? (
          <div className="p-8 text-center text-base text-ink-dim">No shows match that search.</div>
        ) : (
          view.map((g, i) => (
            <SeriesGroupRow
              key={g.series_id}
              g={g}
              first={i === 0}
              open={open === g.series_id}
              onToggle={() => setOpen((cur) => (cur === g.series_id ? null : g.series_id))}
              flash={flash}
              onQueued={onQueued}
              onProfileChanged={() => setReload((k) => k + 1)}
            />
          ))
        )}
      </div>
    </div>
  );
}

// SeriesGroupRow is one show, expanding to its episodes. The episodes are fetched on
// first open and kept, so collapsing and reopening doesn't re-probe the files.
function SeriesGroupRow({ g, first, open, onToggle, flash, onQueued, onProfileChanged }: {
  g: SubSeriesGroup; first: boolean; open: boolean; onToggle: () => void;
  flash: (m: string) => void; onQueued: () => void; onProfileChanged: () => void;
}) {
  const [eps, setEps] = useState<SubFileEntry[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [queued, setQueued] = useState<Set<string>>(new Set());
  const [drawer, setDrawer] = useState<SubFileEntry | null>(null);
  const confirmRedo = useConfirmRedo();

  useEffect(() => {
    if (!open || eps !== null) return;
    api.subtitleSeriesEpisodes(g.series_id).then(setEps).catch(() => setEps([]));
  }, [open, eps, g.series_id]);

  const ensure = async (f: SubFileEntry, redo = false) => {
    if (redo && !(await confirmRedo(`${g.title} ${f.title}`))) return;
    const key = rowKey(f);
    setBusy(key);
    try {
      await api.subtitleQueueEpisode(f.series_id!, f.season!, f.episode!, redo);
      setQueued((q) => new Set(q).add(key));
      flash(redo ? `Redoing ${f.title}` : `Queued ${f.title}`);
      onQueued();
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };

  const pct = g.episodes ? Math.round((g.covered / g.episodes) * 100) : 0;

  // Whole show at once — every episode still missing a kept language. The Library
  // offered one episode or the entire library and nothing between.
  const [showBusy, setShowBusy] = useState(false);
  const [showQueued, setShowQueued] = useState(false);
  const ensureShow = async () => {
    setShowBusy(true);
    try {
      const r = await api.subtitleQueueSeries(g.series_id);
      setShowQueued(true);
      flash(r.queued ? `Queued ${r.queued} episode${r.queued === 1 ? "" : "s"} of ${g.title}` : `${g.title}: nothing to do — every episode has its subtitles`);
      onQueued();
    } catch (e) { flash((e as Error).message); } finally { setShowBusy(false); }
  };

  return (
    <div style={{ borderTop: first ? "none" : "1px solid var(--line-soft)" }}>
      <div className="flex items-center gap-2 pr-3 hover:bg-[var(--panel-2)]">
      <button onClick={onToggle} className="flex min-w-0 flex-1 items-center gap-3 px-3 py-2.5 text-left">
        <span className="w-3 flex-none text-xs text-ink-faint">{open ? "▾" : "▸"}</span>
        <RowPoster url={g.poster_url} />
        <span className="min-w-0 flex-1 truncate text-base font-semibold">
          {g.title} <span className="font-normal text-ink-faint">{g.year || ""}</span>
        </span>
        <span className="hidden font-mono text-xs text-ink-faint sm:inline">
          {g.seasons} season{g.seasons === 1 ? "" : "s"} · {g.episodes} ep
        </span>
        <span className="w-[120px] flex-none">
          <span className="block h-1.5 w-full overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
            <span className="block h-full rounded-full" style={{ width: `${pct}%`, background: g.missing ? "var(--avoid)" : "var(--good)" }} />
          </span>
        </span>
        <span className="w-[92px] flex-none text-right font-mono text-xs" style={{ color: g.missing ? "var(--avoid-text)" : "var(--good-text)" }}>
          {g.missing ? `${g.missing} missing` : "complete"}
        </span>
      </button>
      <ProfileSelect kind="series" id={g.series_id} value={g.profile_id} title={g.title} onChanged={() => { setEps(null); onProfileChanged(); }} />
      <button
        type="button"
        onClick={ensureShow}
        disabled={showBusy || showQueued || !g.missing}
        title={g.missing ? `Queue the ${g.missing} episode${g.missing === 1 ? "" : "s"} still missing a kept language` : "Every episode already has its subtitles"}
        className="flex-none rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-40"
        style={{ border: "1px solid var(--line)", background: "var(--panel)", color: "var(--ink)" }}
      >
        {showQueued ? "Queued" : "Ensure show"}
      </button>
      </div>

      {open && (
        <div className="px-3 pb-3">
          {eps === null ? (
            <div className="py-6 text-center text-sm text-ink-dim">Scanning {g.title}…</div>
          ) : eps.length === 0 ? (
            <div className="py-6 text-center text-sm text-ink-dim">No episodes with files.</div>
          ) : (
            <div className="overflow-x-auto rounded-lg" style={{ border: "1px solid var(--line)" }}>
              <table className="w-full border-collapse text-base" style={{ minWidth: 900 }}>
                <thead><tr style={{ background: "var(--panel-2)" }}>
                  {["Episode", "Audio", "Embedded", "Coverage", "Health", ""].map((h, i) => (
                    <th key={i} className="px-3 py-2 text-left font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{h}</th>
                  ))}
                </tr></thead>
                <tbody>
                  {eps.map((f, i) => (
                    <SubRow key={rowKey(f)} f={f} first={i === 0} busy={busy} queued={queued.has(rowKey(f))} onEnsure={() => ensure(f)} onRedo={() => ensure(f, true)} onOpen={() => setDrawer(f)}
                      flash={flash} onQueued={onQueued} onChanged={() => { api.subtitleSeriesEpisodes(g.series_id).then(setEps).catch(() => {}); }} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
      {/* A change in the drawer re-reads the show's episodes (the effect above refetches on null). */}
      {drawer && <LazySubtitleDrawer media={refOf(drawer)} title={drawer.title} onClose={() => setDrawer(null)} onChanged={() => setEps(null)} />}
    </div>
  );
}

// SubRow is one file's coverage line. Shared by the flat movie table and the per-show
// episode table, so the two can't drift apart.
// A redo overwrites the sidecars that are there, so it asks first.
function useConfirmRedo() {
  const confirm = useConfirm();
  return (title: string) => confirm({
    title: `Redo every language for ${title}?`,
    body: "Each kept language is made again from the next-best source: never the same download, else another track or AI. The old files go to the recycle bin once their replacements are written. To redo one language, click its chip instead.",
    confirmLabel: "Redo",
  });
}

function SubRow({ f, first, busy, queued, onEnsure, onRedo, flash, onChanged, onQueued, onOrphan, onOpen, onProfileChanged }: {
  f: SubFileEntry; first: boolean; busy: string | null; queued: boolean; onEnsure: () => void; onRedo: () => void;
  flash: (m: string) => void; onChanged: () => void; onQueued: () => void; onOrphan?: (name: string, action: "adopt" | "replace") => void; onOpen: () => void;
  onProfileChanged?: () => void;
}) {
  const key = rowKey(f);
  const k = embeddedKinds(f);
  // The title opens the file's drawer: its subtitle files, previews, timing and delete.
  const name = <button type="button" onClick={onOpen} className="text-left hover:underline" title="Open this file's subtitles">{f.title} <span className="font-normal text-ink-faint">{f.year || ""}</span></button>;
  return (
    <tr style={{ borderTop: first ? "none" : "1px solid var(--line-soft)" }}>
      <td className="px-3 py-2 font-semibold">
        {f.kind === "movie" ? (
          <span className="flex items-center gap-2.5"><RowPoster url={f.poster_url} />{name}
            {onProfileChanged && !!f.movie_id && <ProfileSelect kind="movie" id={f.movie_id} value={f.profile_id} title={f.title} onChanged={onProfileChanged} />}</span>
        ) : name}
      </td>
      <td className="px-3 py-2 font-mono text-xs text-ink-dim">{(f.audio_langs ?? []).map((l) => l.toUpperCase()).join(" ") || "—"}</td>
      <td className="px-3 py-2">
        {(f.embedded ?? []).length === 0 ? <span className="text-ink-faint">—</span> : (
          <div className="flex items-center gap-1">
            {k.txt && <EmbBadge kind="txt" />}
            {k.pgs && <EmbBadge kind="pgs" />}
            {k.vob && <EmbBadge kind="vob" />}
          </div>
        )}
      </td>
      <td className="px-3 py-2">
        <div className="flex flex-wrap items-center gap-1">{(f.languages ?? []).map((l) => l.have && l.variant !== "forced" ? <RedoChip key={l.lang} f={f} l={l} flash={flash} onQueued={onQueued} /> : <CoverChip key={l.lang + (l.variant ?? "")} l={l} />)}</div>
        {onOrphan && (f.orphans ?? []).length > 0 && (
          <div className="mt-1 flex flex-col gap-0.5">
            {f.orphans!.map((o) => (
              <div key={o.name} className="flex min-w-0 items-center gap-1.5 text-xs text-ink-faint">
                <span className="truncate font-mono" title="Named for no video in this folder, so Plex won't show it with this file">{o.name}</span>
                <button type="button" onClick={() => onOrphan(o.name, "adopt")} disabled={busy !== null} title="Rename it to pair with this movie, so Plex shows it" className="flex-none rounded px-1.5 py-0.5 font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>Adopt</button>
                {o.variant !== "forced" && <button type="button" onClick={() => onOrphan(o.name, "replace")} disabled={busy !== null} title="Make a fresh subtitle; this one goes to the recycle bin once it's written" className="flex-none rounded px-1.5 py-0.5 font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Replace</button>}
              </div>
            ))}
          </div>
        )}
      </td>
      <td className="px-3 py-2"><HealthCell f={f} flash={flash} onChanged={onChanged} /></td>
      <td className="px-3 py-2">
        <div className="flex items-center justify-end gap-2">
          {queued ? (
            <span className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--good)", color: "var(--good-text)" }}>Queued ✓</span>
          ) : f.missing === 0 ? (
            <>
              <span className="font-mono text-xs" style={{ color: "var(--good-text)" }}>complete</span>
              <button onClick={onRedo} disabled={busy !== null} title="Make every language again; to redo one, click its chip" className="rounded-md px-2 py-1 text-xs font-semibold text-ink-dim disabled:opacity-50" style={{ border: "1px solid var(--line)" }}>{busy === key ? "…" : "Redo all languages…"}</button>
            </>
          ) : (
            <button onClick={onEnsure} disabled={busy !== null} className="rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>{busy === key ? "Queuing…" : "Ensure subs"}</button>
          )}
        </div>
      </td>
    </tr>
  );
}

function EmbBadge({ kind }: { kind: "txt" | "pgs" | "vob" }) {
  const style = kind === "txt" ? { background: "var(--panel-2)", color: "var(--ink-dim)" } : { background: "var(--avoid-soft)", color: "var(--avoid-text)" };
  const label = kind === "txt" ? "TXT" : kind === "pgs" ? "PGS" : "VOB";
  const title = kind === "txt" ? "Embedded text — extractable to SRT" : "Image subtitle — needs OCR";
  return <span className="rounded px-1 py-0.5 text-xs font-bold uppercase" style={style} title={title}>{label}</span>;
}
function CoverChip({ l }: { l: SubLangStatus }) {
  // A profile's forced want: only a ".forced" subtitle (the foreign-dialogue lines) meets it.
  const name = l.variant === "forced" ? `${l.lang} forced` : l.lang;
  if (l.have) return <span className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good-text)" }} title="External SRT present">✓ {name}</span>;
  // A subtitle under another name covers it, but Plex won't pair it with this file.
  if (l.orphan) return <span className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel-2)", color: "var(--avoid-text)", border: "1px dashed var(--avoid)" }} title="A subtitle in the folder is named for no video, so Plex won't show it with this file. The sweep leaves it alone; Ensure subs makes a properly named one.">{name} · orphaned</span>;
  // Backing off: the last run couldn't make it, and the sweep leaves it alone until then.
  if (l.reason) {
    const why = REASON_LABEL[l.reason] ?? l.reason.replace(/_/g, " ");
    const when = l.next_retry_at ? `next try ${retryIn(l.next_retry_at)}`
      : l.retry_on ? `waiting for ${l.retry_on.split(",").map((e) => RETRY_ON_LABEL[e] ?? e).join(" or ")}`
      : "retry by hand";
    return <span className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel-2)", color: "var(--avoid-text)", border: "1px solid var(--avoid-soft)" }} title={`Missing — last try: ${why}. The sweep leaves it alone until then; Ensure subs tries now.`}>{name} · {when}</span>;
  }
  // The ladder tries the first source, then falls back to AI when that comes up empty.
  // No source means nothing can make it yet: no text track, no download set up, and no AI model
  // that can (whisper never makes a forced subtitle).
  const first = !l.source ? "no source" : SOURCE_LABEL[l.source] ?? l.source;
  const plan = l.fallback ? `${first} → ${SOURCE_LABEL[l.fallback] ?? l.fallback}` : first;
  return <span className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }} title={`Missing — will try ${plan}`}>{name} · {plan}</span>;
}

/* ============================= LOGS ============================= */
function LogsConsole() {
  const [lines, setLines] = useState<{ at: number; level: string; msg: string }[]>([]);
  const [follow, setFollow] = useState(true);
  const boxRef = useRef<HTMLDivElement | null>(null);
  usePoll(() => api.subtitleLogs().then((l) => {
    setLines((prev) => {
      const a = prev[prev.length - 1], b = l[l.length - 1];
      if (prev.length === l.length && a?.at === b?.at && a?.msg === b?.msg) return prev;
      return l;
    });
  }).catch(() => {}), 1500);
  useEffect(() => { if (follow && boxRef.current) boxRef.current.scrollTop = boxRef.current.scrollHeight; }, [lines, follow]);
  const tone = (lvl: string) => (lvl === "error" ? "var(--reject)" : lvl === "warn" ? "var(--avoid)" : "var(--ink-dim)");
  const clock = (at: number) => new Date(at * 1000).toLocaleTimeString();
  return (
    <div className={card} style={cardStyle}>
      <div className="mb-2 flex items-center justify-between">
        <div className="text-md font-bold">Activity log</div>
        <label className="flex items-center gap-1.5 text-xs text-ink-faint"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Auto-scroll</label>
      </div>
      <div ref={boxRef} className="thin-scroll max-h-[62vh] overflow-y-auto rounded-lg p-3 font-mono text-sm leading-relaxed" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
        {lines.length === 0 ? (
          <div className="text-ink-faint">No activity yet. Queue a file and it'll stream here.</div>
        ) : lines.map((l, i) => (
          <div key={i} className="flex gap-2"><span className="flex-none text-ink-faint">{clock(l.at)}</span><span style={{ color: tone(l.level) }}>{l.msg}</span></div>
        ))}
      </div>
    </div>
  );
}

function Pill({ active, disabled, onClick, children }: { active: boolean; disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" disabled={disabled} onClick={onClick}
      className="rounded-full px-2.5 py-1 text-xs font-semibold transition-colors disabled:opacity-40 disabled:cursor-default"
      style={{ border: `1px solid ${active ? "var(--accent)" : "var(--line)"}`, background: active ? "var(--accent-soft, rgba(198,93,59,.12))" : "transparent", color: active ? "var(--accent-text)" : "var(--ink-dim)" }}>
      {children}
    </button>
  );
}

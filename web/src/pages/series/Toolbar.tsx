import { useState } from "react";
import { DeleteSeriesDialog } from "../../components/DeleteSeriesDialog";
import { ReleaseSearchModal } from "../../components/ReleaseSearchModal";
import { UploadTorrentModal } from "../../components/UploadTorrentModal";
import { LastSearches, refreshSearches } from "../../components/LastSearch";
import { api, type Series as SeriesT } from "../../lib/api";
import { jobFailed, jobToast, useJob } from "../../lib/useJob";
import { searchJobLine } from "../../lib/searchOutcome";
import { RenameModal } from "./RenameModal";
import { MONITOR_PRESETS } from "./presets";
import { ManualImportModal } from "./ManualImportModal";
import { AliasPanel, SceneMapPanel } from "./panels";

export function Toolbar({ series, onChange, flash }: { series: SeriesT; onChange: () => void; flash: (m: string, err?: boolean) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  // The search and a whole-folder import run as jobs; when one ends the toast says what
  // actually happened.
  const [searchJob, setSearchJob] = useState<number | null>(null);
  const [importJob, setImportJob] = useState<number | null>(null);
  const [searchResult, setSearchResult] = useState<{ text: string; failed: boolean } | null>(null);
  const search = useJob(searchJob, {
    onDone: (j) => {
      setSearchJob(null);
      const text = searchJobLine(j);
      setSearchResult({ text, failed: jobFailed(j) });
      flash(text, jobFailed(j));
      refreshSearches("series", series.id);
      onChange();
    },
  });
  useJob(importJob, {
    onDone: (j) => { setImportJob(null); flash(jobToast(j, "Import finished."), jobFailed(j)); onChange(); },
  });
  const [showImport, setShowImport] = useState(false);
  const [showSearch, setShowSearch] = useState(false);
  const [showPaste, setShowPaste] = useState(false);

  const run = async (key: string, fn: () => Promise<void>) => {
    setBusy(key);
    try { await fn(); } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };

  const btn = "rounded-lg px-3 py-2 text-[12.5px] font-semibold disabled:opacity-50";
  const ghost = { border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" } as const;

  const [showRename, setShowRename] = useState(false);

  return (
    <>
      <div className="mt-4 flex flex-wrap items-center gap-3">
        <button
          role="switch"
          aria-checked={series.monitored}
          disabled={busy !== null}
          onClick={() => run("monitor", async () => { await api.setSeriesMonitored(series.id, !series.monitored); onChange(); flash(series.monitored ? "Paused — nothing is searched until you resume." : "Resumed."); })}
          className="inline-flex items-center gap-2 text-[12.5px] font-semibold disabled:opacity-50"
          title={series.monitored ? "Monitored — click to pause. Season and episode choices are kept." : "Paused — nothing is searched. Click to resume."}
        >
          <span className="relative inline-block h-[22px] w-[38px] rounded-full transition-colors" style={{ background: series.monitored ? "var(--accent)" : "var(--line)" }}>
            <span className="absolute top-[3px] h-[16px] w-[16px] rounded-full bg-white transition-all" style={{ left: series.monitored ? "19px" : "3px" }} />
          </span>
          <span style={{ color: series.monitored ? "var(--ink)" : "var(--ink-dim)" }}>{series.monitored ? "Monitored" : "Monitor"}</span>
        </button>
        <label className="inline-flex items-center gap-1.5 text-[12px] text-ink-dim" title="When a refresh finds a season the show didn't have, monitor it.">
          <input
            type="checkbox"
            checked={!!series.monitor_new_seasons}
            disabled={busy !== null}
            onChange={(e) => { const on = e.target.checked; void run("monitor-new", async () => { await api.setSeriesMonitorNewSeasons(series.id, on); onChange(); }); }}
          />
          Monitor new seasons
        </label>
        <select
          value=""
          disabled={busy !== null}
          aria-label="Apply a monitoring preset"
          title="Sets which episodes are monitored, in one go. The pause switch is left as it is."
          onChange={(e) => {
            const preset = e.target.value;
            e.target.value = "";
            if (!preset) return;
            const label = MONITOR_PRESETS.find((p) => p.value === preset)?.label ?? preset;
            void run("preset", async () => { await api.applySeriesMonitorPreset(series.id, preset); onChange(); flash(`Monitoring: ${label}.`); });
          }}
          className="rounded-lg px-2 py-1.5 text-[12px] disabled:opacity-50"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
        >
          <option value="">Apply monitoring…</option>
          {MONITOR_PRESETS.map((p) => <option key={p.value} value={p.value} title={p.help}>{p.label}</option>)}
        </select>
        <button className={btn} style={ghost} disabled={busy !== null} onClick={() => run("refresh", async () => { await api.refreshSeries(series.id); onChange(); flash("Refreshed metadata and rescanned disk."); })}>
          {busy === "refresh" ? "Refreshing…" : "Refresh & rescan"}
        </button>
        <button
          className={btn}
          style={series.series_type === "anime" ? { background: "var(--accent-soft)", color: "var(--accent)", border: "1px solid var(--accent-line)" } : ghost}
          disabled={busy !== null}
          title="Anime numbers episodes 1..N across the whole run. Turn on to match releases by absolute episode number (and per-cour files). Auto-detected for Japanese animation."
          onClick={() => run("type", async () => {
            const next = series.series_type === "anime" ? "standard" : "anime";
            await api.setSeriesType(series.id, next);
            onChange();
            flash(next === "anime" ? "Anime numbering on — rescan to match absolute-numbered files." : "Standard numbering.");
          })}
        >
          {busy === "type" ? "Saving…" : series.series_type === "anime" ? "Anime ✓" : "Anime"}
        </button>
        <button className={btn} style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }} disabled={busy !== null || search.running} onClick={() => run("search", async () => { setSearchResult(null); const r = await api.searchSeries(series.id); if (r.job_id) setSearchJob(r.job_id); flash("Searching — follow packs and episodes in Activity → Queue once grabbed."); })}>
          {busy === "search" || search.running ? "Searching…" : "Auto-grab missing"}
        </button>
        <button className={btn} style={ghost} disabled={busy !== null} onClick={() => setShowSearch(true)}>Search indexers</button>
        <button className={btn} style={ghost} disabled={busy !== null} onClick={() => setShowPaste(true)}>Upload torrent</button>
        <button className={btn} style={ghost} disabled={busy !== null} onClick={() => setShowImport(true)}>Manual import</button>
        <button className={btn} style={ghost} disabled={busy !== null} onClick={() => setShowRename(true)}>Rename</button>
        <DeleteButton series={series} />
      </div>
      {(search.running || searchResult) && (
        <div className="mt-2 text-[12px]" role="status" style={{ color: search.running ? "var(--ink-dim)" : searchResult?.failed ? "var(--reject)" : "var(--ink)" }}>
          {search.running ? "Searching…" : searchResult?.text}
        </div>
      )}
      <LastSearches kind="series" id={series.id} />
      <AliasPanel series={series} />
      {series.series_type === "anime" && <SceneMapPanel series={series} />}
      {showPaste && (
        <UploadTorrentModal
          what={series.title}
          onPreview={(torrent) => api.previewTorrent(torrent)}
          onGrab={async (torrent, filename, title) => { await api.grabSeriesTorrent(series.id, torrent, filename, title); onChange(); }}
          onClose={() => setShowPaste(false)}
        />
      )}
      {showRename && <RenameModal seriesId={series.id} title={series.title} onClose={() => setShowRename(false)} onRenamed={onChange} />}
      {showImport && (
        <ManualImportModal
          series={series}
          onClose={() => setShowImport(false)}
          onImported={() => { onChange(); flash("Imported."); }}
          onBackground={(jobId) => { onChange(); if (jobId) setImportJob(jobId); }}
        />
      )}
      {showSearch && (
        <ReleaseSearchModal
          title={`Search indexers — ${series.title}`}
          subtitle="Whole-series search. Pick any pack or episode to grab."
          fetchReleases={() => api.seriesReleases(series.id)}
          onGrab={async (rel) => { await api.grabSeries(series.id, { token: rel.token ?? "" }); onChange(); }}
          onClose={() => setShowSearch(false)}
        />
      )}
    </>
  );
}

function DeleteButton({ series }: { series: SeriesT }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)} className="rounded-lg px-3 py-2 text-[12.5px] font-semibold" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>Delete</button>
      {open && <DeleteSeriesDialog series={series} onClose={() => setOpen(false)} onDeleted={() => { window.location.href = "/series"; }} />}
    </>
  );
}

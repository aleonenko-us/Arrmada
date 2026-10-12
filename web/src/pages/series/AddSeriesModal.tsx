import { useEffect, useId, useRef, useState } from "react";
import { api, type SeriesLookup } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { Button, Modal } from "../../ui";
import { DEFAULT_MONITOR_PRESET, MONITOR_PRESETS, isMonitorPreset, type MonitorPreset } from "./presets";

// AddSeriesModal is the library's "+ Add series": search TMDB, pick a show, and add it on a
// profile with a monitoring preset, searching for it at once or not.
export function AddSeriesModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<SeriesLookup[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profile, setProfile] = useState("");
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [searchOnAdd, setSearchOnAdd] = useState(true);
  const [monitor, setMonitor] = useState<MonitorPreset>(DEFAULT_MONITOR_PRESET);
  const [addingId, setAddingId] = useState<number | null>(null);
  const titleId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    qualityApi.qualityProfiles("series").then((r) => {
      setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
      const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
      if (def) setProfile(def.key);
    }).catch(() => {});
    // The settings are this dialog's starting point; changing them here is for this add
    // only (it used to rewrite the global "Search on add").
    api.settings().then((s) => {
      setSearchOnAdd(s.search_on_add);
      if (isMonitorPreset(s.series_monitor_default)) setMonitor(s.series_monitor_default);
    }).catch(() => {});
  }, []);

  const toggleSearchOnAdd = () => setSearchOnAdd((v) => !v);

  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!q.trim()) return;
    setLoading(true); setError(null);
    try { setResults(await api.lookupSeries(q.trim())); } catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  };

  const add = async (r: SeriesLookup) => {
    setAddingId(r.tmdb_id); setError(null);
    try { await api.addSeries({ tmdb_id: r.tmdb_id, quality_profile: profile, monitored: true, search_on_add: searchOnAdd, monitor }); onAdded(); }
    catch (e) { setError((e as Error).message); setAddingId(null); }
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="lg" initialFocus={inputRef}>
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 id={titleId} className="m-0 text-[15px] font-bold">Add a series</h2>
          <div className="flex flex-wrap items-center gap-4">
            <button type="button" onClick={toggleSearchOnAdd} title="On: search right away. Off: add it paused — nothing is searched until you resume it. For this add only." className="flex items-center gap-2">
              <span className="relative inline-block h-[20px] w-[34px] rounded-full transition-colors" style={{ background: searchOnAdd ? "var(--accent)" : "var(--line)" }}>
                <span className="absolute top-[3px] h-[14px] w-[14px] rounded-full bg-white transition-all" style={{ left: searchOnAdd ? "17px" : "3px" }} />
              </span>
              <span className="text-[11.5px] font-medium" style={{ color: searchOnAdd ? "var(--ink)" : "var(--ink-dim)" }}>{searchOnAdd ? "Search on add" : "Add paused"}</span>
            </button>
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] uppercase text-ink-faint">Monitor</span>
              <select
                value={monitor}
                onChange={(e) => setMonitor(e.target.value as MonitorPreset)}
                title={MONITOR_PRESETS.find((p) => p.value === monitor)?.help}
                aria-label="Which episodes to monitor"
                className="rounded-lg px-2 py-1.5 text-[12px]"
                style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
              >
                {MONITOR_PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
              </select>
            </div>
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] uppercase text-ink-faint">Quality</span>
              <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2 py-1.5 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
                {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
              </select>
            </div>
          </div>
        </div>
        <form onSubmit={search} className="mb-3 flex gap-2">
          <input ref={inputRef} value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search TV series — e.g. Severance" aria-label="Search TMDB for a series" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-[13px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          <Button type="submit" variant="primary" busy={loading} busyLabel="…">Search</Button>
        </form>
        {error && <div className="mb-3 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
        <div className="max-h-[52vh] overflow-y-auto">
          {results.map((r) => (
            <button key={r.tmdb_id} onClick={() => add(r)} disabled={addingId !== null} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
              <div className="h-[68px] w-[46px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
                {r.poster_url && <img src={r.poster_url} alt="" className="h-full w-full object-cover" loading="lazy" />}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-semibold">{r.title} <span className="font-normal text-ink-faint">{r.year ? `(${r.year})` : ""}</span></div>
                <div className="mt-0.5 line-clamp-2 text-[11.5px] text-ink-dim">{r.overview || "No overview."}</div>
              </div>
              <span className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>
                {addingId === r.tmdb_id ? "Adding…" : "Add"}
              </span>
            </button>
          ))}
        </div>
    </Modal>
  );
}

import { useState } from "react";
import { type Series } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";
import { useToast } from "../../ui";
import { bulkFailure, runBulk, type BulkResult } from "../../features/library/bulk";
import type { LibrarySelection } from "../../features/library/types";
import { MONITOR_PRESETS } from "./presets";

const SELECT = "rounded-lg px-2.5 py-1.5 text-sm font-medium";
const SELECT_STYLE = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

// SeriesBulkActions are the Series controls in the library's bulk bar: monitor (the pause
// gate), quality profile and monitoring preset for every selected show.
export function SeriesBulkActions({ sel, profiles, onDone }: {
  sel: LibrarySelection<Series>;
  profiles: { key: string; name: string }[];
  onDone: () => void;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const none = sel.ids.size === 0 || busy;
  const run = async (fn: (id: number) => Promise<unknown>, done: (n: number) => string) => {
    setBusy(true);
    try {
      const r: BulkResult<unknown> = await runBulk(sel, fn);
      toast(r.failed.length ? bulkFailure(r) : done(r.ok.length), { tone: r.failed.length ? "error" : "info" });
      onDone();
    } finally { setBusy(false); }
  };

  return (
    <>
      {/* The switch is a pause gate: season and episode choices are kept either way. */}
      <button onClick={() => run((id) => seriesApi.setSeriesMonitored(id, true), (n) => `${n} resumed.`)} disabled={none} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }} title="Resume searching. Season and episode choices are kept; a show with nothing monitored gets every regular season.">Monitor</button>
      <button onClick={() => run((id) => seriesApi.setSeriesMonitored(id, false), (n) => `${n} paused.`)} disabled={none} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }} title="Pause: nothing is searched. Season and episode choices are kept for when you resume.">Unmonitor</button>
      <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
      <select value="" disabled={none} aria-label="Set the quality profile of the selected series" className={SELECT} style={SELECT_STYLE}
        onChange={(e) => { const v = e.target.value; if (v) void run((id) => seriesApi.setSeriesProfile(id, v), (n) => `Quality profile set on ${n} series.`); }}>
        <option value="">Set quality profile…</option>
        {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
      </select>
      <select value="" disabled={none} aria-label="Apply a monitoring preset to the selected series" className={SELECT} style={SELECT_STYLE}
        onChange={(e) => {
          const v = e.target.value;
          const label = MONITOR_PRESETS.find((p) => p.value === v)?.label ?? v;
          if (v) void run((id) => seriesApi.applySeriesMonitorPreset(id, v), (n) => `Monitoring set to "${label}" on ${n} series.`);
        }}>
        <option value="">Monitoring preset…</option>
        {MONITOR_PRESETS.map((p) => <option key={p.value} value={p.value} title={p.help}>{p.label}</option>)}
      </select>
    </>
  );
}

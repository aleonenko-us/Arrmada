import { useState } from "react";
import { type Series } from "../../lib/api";
import { seriesApi, type MonitorReply } from "../../lib/seriesApi";
import { useToast } from "../../ui";
import { bulkFailure, runBulk, type BulkResult } from "../../features/library/bulk";
import type { LibrarySelection } from "../../features/library/types";
import { MONITOR_PRESETS } from "./presets";

const SELECT = "rounded-lg px-2.5 py-1.5 text-sm font-medium";
const SELECT_STYLE = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

// SeriesBulkActions are the Series controls in the library's bulk bar: searching on/off
// (the show's switch), quality profile and monitoring preset for every selected show.
export function SeriesBulkActions({ sel, profiles, onDone }: {
  sel: LibrarySelection<Series>;
  profiles: { key: string; name: string }[];
  onDone: () => void;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const none = sel.ids.size === 0 || busy;
  const run = async (fn: (id: number) => Promise<unknown>, done: (n: number, ok: unknown[]) => string) => {
    setBusy(true);
    try {
      const r: BulkResult<unknown> = await runBulk(sel, fn);
      toast(r.failed.length ? bulkFailure(r) : done(r.ok.length, r.ok), { tone: r.failed.length ? "error" : "info" });
      onDone();
    } finally { setBusy(false); }
  };

  return (
    <>
      {/* The shows' searching switch: season and episode choices are kept either way. */}
      <button onClick={() => run((id) => seriesApi.setSeriesMonitored(id, true), (n) => `Searching on for ${n} series.`)} disabled={none} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }} title="Turn searching on. Season and episode choices are kept; a show with nothing monitored gets every regular season.">Searching on</button>
      <button onClick={() => run((id) => seriesApi.setSeriesMonitored(id, false), (n) => `Searching off for ${n} series.`)} disabled={none} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }} title="Turn searching off: nothing is searched. Season and episode choices are kept for when it's back on.">Searching off</button>
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
          // A preset turns searching on too (as on a show's page), so the toast counts the
          // shows it turned on for.
          if (v) void run((id) => seriesApi.applySeriesMonitorPreset(id, v), (n, ok) => {
            const on = ok.filter((r) => (r as MonitorReply | undefined)?.resumed).length;
            return `Monitoring set to "${label}" on ${n} series${on > 0 ? ` — searching turned on for ${on}` : ""}.`;
          });
        }}>
        <option value="">Monitoring preset…</option>
        {MONITOR_PRESETS.map((p) => <option key={p.value} value={p.value} title={p.help}>{p.label}</option>)}
      </select>
    </>
  );
}

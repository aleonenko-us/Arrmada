import { createContext, type ReactNode } from "react";
import type { GeoLocation, PlexConfig } from "../../lib/api";

// Pieces every Insights view shares: the stream-decision and buffer-cause vocabularies,
// number and date formats, and the small filter chips.

export const DECISION: Record<string, { label: string; color: string }> = {
  direct_play: { label: "Direct Play", color: "var(--good)" },
  direct_stream: { label: "Direct Stream", color: "var(--accent)" },
  transcode: { label: "Transcode", color: "var(--avoid)" },
};

// Diagnosed buffer-cause styling (keys match BufferEvent.cause / CauseCount.cause).
export const CAUSE: Record<string, { label: string; color: string }> = {
  transcode: { label: "Transcode overloaded", color: "var(--reject)" },
  transcode_cpu: { label: "CPU transcode (no HW)", color: "var(--reject)" },
  transcode_fallback: { label: "Plex fell back to CPU", color: "var(--reject)" },
  bandwidth: { label: "Bandwidth / network", color: "var(--avoid)" },
  unknown: { label: "Inconclusive", color: "var(--ink-faint)" },
};

// InsightsCfg hands the latest Plex settings to deep components (History's HW badge)
// without threading a prop through every view.
export const InsightsCfg = createContext<PlexConfig | null>(null);

export function geoLabel(g: GeoLocation): string {
  if (g.local) return "Local";
  if (g.city && g.country_code) return `${g.city}, ${g.country_code}`;
  if (g.country) return g.country;
  return g.ip || "—";
}

export function fmtMbps(kbps: number): string {
  if (!kbps) return "0";
  const mb = kbps / 1000;
  return mb >= 10 ? mb.toFixed(0) : mb.toFixed(1);
}

export function fmtClock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), ss = s % 60;
  const mm = String(m).padStart(2, "0"), sss = String(ss).padStart(2, "0");
  return h > 0 ? `${h}:${mm}:${sss}` : `${m}:${sss}`;
}

// fmtHours renders seconds compactly for ranked lists ("12h", "1.5h", "40m").
export function fmtHours(secs: number): string {
  if (!secs) return "0";
  const h = secs / 3600;
  return h >= 10 ? `${h.toFixed(0)}h` : h >= 1 ? `${h.toFixed(1)}h` : `${Math.round(secs / 60)}m`;
}

// fmtHoursValue renders an hours figure (a chart value) the same way.
export function fmtHoursValue(h: number): string {
  return fmtHours(h * 3600);
}

export function fmtDate(epoch: number): string {
  if (!epoch) return "—";
  const d = new Date(epoch * 1000);
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) + " " + d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function fmtDay(epoch: number): string {
  if (!epoch) return "—";
  return new Date(epoch * 1000).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

export function fmtDur(secs: number): string {
  if (!secs) return "—";
  const h = Math.floor(secs / 3600), m = Math.round((secs % 3600) / 60);
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

// dayLabel turns "2026-10-09" into "Oct 9" (local; no time zone shift).
export function dayLabel(d: string, long = false): string {
  const [y, m, day] = d.split("-").map(Number);
  return new Date(y, m - 1, day).toLocaleDateString(undefined, long ? { weekday: "short", month: "short", day: "numeric" } : { month: "short", day: "numeric" });
}

// bandwidthLabels puts the date on the first bucket of each day and leaves the rest
// blank; bucket times are "YYYY-MM-DD HH:00" (local).
export function bandwidthLabels(ts: string[]): string[] {
  let last = "";
  return ts.map((t) => {
    const d = t.slice(0, 10);
    if (d === last) return "";
    last = d;
    return dayLabel(d);
  });
}

// fmtStall renders observed stall milliseconds compactly ("47s", "3m 12s").
export function fmtStall(ms: number): string {
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s`;
  return `${Math.floor(secs / 60)}m ${secs % 60}s`;
}

export function FilterGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="font-mono text-2xs font-bold uppercase tracking-[0.09em] text-ink-faint">{label}</span>
      <div className="flex flex-wrap gap-1">{children}</div>
    </div>
  );
}

export function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" aria-pressed={active} onClick={onClick} className="rounded-full px-2.5 py-1 text-xs font-semibold transition-colors" style={{ border: `1px solid ${active ? "var(--accent)" : "var(--line)"}`, background: active ? "var(--accent-soft)" : "var(--panel-2)", color: active ? "var(--accent)" : "var(--ink-dim)" }}>
      {children}
    </button>
  );
}

export function StatTile({ label, value, sub, color }: { label: string; value: string; sub: string; color?: string }) {
  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{label}</div>
      <div className="mt-1 text-3xl font-extrabold tracking-tight" style={color ? { color } : undefined}>{value}</div>
      <div className="text-xs text-ink-faint">{sub}</div>
    </div>
  );
}

// METRIC is how the Graphs (and the People page) count: plays, or hours watched.
export type Metric = "plays" | "duration";

import { useEffect, useMemo, useState } from "react";
import { api, type MovieEvent, type SearchAttempt } from "../../lib/api";
import { useQuery } from "../../lib/query";
import { attemptBody, outcomeTone, reasonLabel, scopeLabel } from "../../lib/searchOutcome";

// The type filters, and which history events each covers. Anything else (added,
// refreshed, failed, deleted…) shows under All only.
const TYPES = [
  { key: "all", label: "All" },
  { key: "grabbed", label: "Grabbed" },
  { key: "imported", label: "Imported" },
  { key: "searched", label: "Searched" },
  { key: "renamed", label: "Renamed" },
  { key: "numbering", label: "Numbering" },
] as const;
type TypeKey = (typeof TYPES)[number]["key"];

const CATEGORY: Record<string, TypeKey> = {
  grabbed: "grabbed",
  imported: "imported",
  upgraded: "imported",
  searched: "searched",
  renamed: "renamed",
  numbering: "numbering",
  renumbered: "numbering",
  "scene-map": "numbering",
};

const TONES: Record<string, string> = {
  grabbed: "var(--accent-text)",
  imported: "var(--good-text)",
  upgraded: "var(--good-text)",
  failed: "var(--avoid-text)",
  blocklisted: "var(--avoid-text)",
  deleted: "var(--reject-text)",
};

export interface ActivityRow {
  key: string;
  at: number; // ms
  event: string;
  type: TypeKey | "other";
  detail: string;
  attempt?: SearchAttempt;
}

// eventTime reads a history event's created_at: SQLite's UTC "YYYY-MM-DD HH:MM:SS", or RFC 3339.
export function eventTime(s: string): number {
  const t = Date.parse(s.includes("T") ? s : s.replace(" ", "T") + "Z");
  return isNaN(t) ? 0 : t;
}

// mergeActivity is the show's one timeline, newest first: its history events and its
// stored search attempts. A click's search writes both an event and an attempt, so a
// 'searched' event is kept only from before the oldest attempt loaded — the attempt says
// more (counts and reasons), and older history still shows what was searched.
export function mergeActivity(events: MovieEvent[], attempts: SearchAttempt[]): ActivityRow[] {
  const oldest = attempts.reduce((m, a) => Math.min(m, a.started_at), Infinity);
  const rows: ActivityRow[] = [];
  events.forEach((e, i) => {
    const at = eventTime(e.created_at);
    if (e.event === "searched" && at >= oldest - 5_000) return;
    rows.push({ key: `e${i}`, at, event: e.event, type: CATEGORY[e.event] ?? "other", detail: e.detail ?? "" });
  });
  for (const a of attempts) {
    rows.push({ key: `a${a.id}`, at: a.started_at, event: "searched", type: "searched", detail: attemptBody(a), attempt: a });
  }
  return rows.sort((x, y) => y.at - x.at);
}

function fmtWhen(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

const TRIGGER: Record<string, string> = { manual: "by hand", sweep: "the sweep", rss: "RSS", add: "on add", request: "for a request", replace: "Replace", upgrade: "an upgrade sweep", stall: "after a stalled download" };

// ActivityTab is what happened to the show, newest first: grabs, imports, renames,
// numbering changes and every search — the ones that found nothing included, with why.
export function ActivityTab({ seriesId, refreshKey }: { seriesId: number; refreshKey: unknown }) {
  const [events, setEvents] = useState<MovieEvent[] | null>(null);
  useEffect(() => {
    let alive = true;
    api.seriesHistory(seriesId).then((e) => { if (alive) setEvents(e); }).catch(() => { if (alive) setEvents([]); });
    return () => { alive = false; };
  }, [seriesId, refreshKey]);
  // Shares the searches:series:<id>: prefix, so a finished search refreshes it too.
  const { data: attempts } = useQuery(`searches:series:${seriesId}:activity`, () => api.searches("series", seriesId, { limit: 100 }), { staleMs: 15_000 });
  const [type, setType] = useState<TypeKey>("all");
  const rows = useMemo(() => mergeActivity(events ?? [], attempts ?? []), [events, attempts]);
  const shown = type === "all" ? rows : rows.filter((r) => r.type === type);

  return (
    <section aria-label="Activity">
      <div role="group" aria-label="Show" className="thin-scroll mb-3 flex gap-1.5 overflow-x-auto pb-1">
        {TYPES.map((t) => (
          <button
            key={t.key}
            type="button"
            aria-pressed={type === t.key}
            onClick={() => setType(t.key)}
            className="flex-none rounded-full px-3 py-1 text-sm font-semibold"
            style={{ background: type === t.key ? "var(--accent-soft)" : "var(--panel-2)", color: type === t.key ? "var(--accent-text)" : "var(--ink-dim)", border: `1px solid ${type === t.key ? "var(--accent-line)" : "var(--line)"}` }}
          >
            {t.label}
          </button>
        ))}
      </div>
      {events === null ? (
        <div className="text-base text-ink-dim">Loading…</div>
      ) : shown.length === 0 ? (
        <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>{type === "all" ? "No activity yet." : "Nothing of that kind yet."}</div>
      ) : (
        <ol className="m-0 flex list-none flex-col p-0" data-testid="activity-list">
          {shown.map((r) => <Row key={r.key} row={r} />)}
        </ol>
      )}
    </section>
  );
}

function Row({ row }: { row: ActivityRow }) {
  const [open, setOpen] = useState(false);
  const a = row.attempt;
  const reasons = Object.entries(a?.reasons ?? {}).filter(([, n]) => n > 0).sort((x, y) => y[1] - x[1]);
  const errors = Object.entries(a?.indexer_errors ?? {});
  const expandable = !!a && (reasons.length > 0 || errors.length > 0 || (a.grabbed_titles ?? []).length > 0 || !!a.example);
  const tone = a ? outcomeTone(a.outcome) : (TONES[row.event] ?? "var(--ink-dim)");
  return (
    <li className="border-b py-2 text-sm" style={{ borderColor: "var(--line-soft)" }}>
      <div className="flex items-start gap-3">
        <span className="w-[78px] flex-none pt-px font-mono text-2xs font-bold uppercase" style={{ color: tone }}>{row.event}</span>
        <div className="min-w-0 flex-1">
          {a && scopeLabel(a.scope) && <span className="mr-1.5 font-mono text-xs font-bold text-ink-faint">{scopeLabel(a.scope)}</span>}
          {expandable ? (
            <button type="button" onClick={() => setOpen((v) => !v)} aria-expanded={open} className="text-left" style={{ color: tone }}>
              {row.detail} <span aria-hidden className="text-ink-faint">{open ? "▾" : "▸"}</span>
            </button>
          ) : (
            <span className={a ? "" : "text-ink-dim"} style={a ? { color: tone } : undefined}>{row.detail || "—"}</span>
          )}
          {a && TRIGGER[a.trigger] && <span className="text-ink-faint"> · {TRIGGER[a.trigger]}</span>}
          {open && a && (
            <ul className="m-0 mt-1.5 flex list-none flex-col gap-0.5 p-0 pl-2 text-sm text-ink-dim">
              <li>{a.returned} found · {a.eligible} fit · {a.grabbed} grabbed</li>
              {reasons.map(([code, n]) => <li key={code}>{n} {reasonLabel(code)}</li>)}
              {a.example && <li className="truncate font-mono text-xs text-ink-faint" title={a.example}>e.g. {a.example}</li>}
              {(a.grabbed_titles ?? []).map((t) => <li key={t} className="truncate font-mono text-xs" title={t}>↓ {t}</li>)}
              {errors.map(([ix, msg]) => <li key={ix} style={{ color: "var(--avoid-text)" }}>{ix}: {msg}</li>)}
            </ul>
          )}
        </div>
        <span className="flex-none font-mono text-xs text-ink-faint">{fmtWhen(row.at)}</span>
      </div>
    </li>
  );
}

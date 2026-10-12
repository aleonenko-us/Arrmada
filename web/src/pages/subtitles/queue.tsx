import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { SubtitleJob, SubtitleOutcome } from "../../lib/api";
import { api } from "../../lib/api";
import { useConfirm } from "../../ui";
import { card, cardStyle } from "./shared";
import { queueApi, type SubtitleJobState, type SubtitleQueue } from "./queueApi";
import { redoLabel } from "./redo";

// The Subtitles Queue tab and the pieces the Overview shares with it: what's running and
// waiting in each lane (extract/download, and the AI lane with its backlog estimate), and
// the saved history with filters, per-language outcomes and a Retry per row.

export const ACTIVE = new Set(["queued", "running"]);

// sortActive lists the running jobs first, then the queue in the order the workers will
// take it: imports, then button presses, then the sweep, oldest first within each. The
// API's newest-first order put the running job at the bottom.
export function sortActive(jobs: SubtitleJob[]): SubtitleJob[] {
  return jobs
    .filter((j) => ACTIVE.has(j.state))
    .sort((a, b) => (a.state === b.state ? (a.priority ?? 1) - (b.priority ?? 1) || a.at - b.at || a.id - b.id : a.state === "running" ? -1 : 1));
}

// backlogText is the AI lane in a line: "AI backlog: 37 files · ~14h at 4.2× realtime".
// No ETA until whisper has been timed once.
export function backlogText(q: Pick<SubtitleQueue, "gpu_backlog" | "gpu_eta_sec" | "rtf"> | null): string | null {
  if (!q || !q.gpu_backlog) return null;
  const files = `${q.gpu_backlog} file${q.gpu_backlog === 1 ? "" : "s"}`;
  if (!q.gpu_eta_sec || !q.rtf) return `AI backlog: ${files}`;
  return `AI backlog: ${files} · ~${fmtElapsed(q.gpu_eta_sec)} at ${q.rtf.toFixed(1)}× realtime`;
}

const PRIO_LABEL = ["import", "manual", "sweep"];
const PRIO_TITLE = ["Just imported — runs before anything else", "Queued from a button — runs before the sweep", "Queued by the periodic sweep — runs after imports and button presses"];
const chipCls = "ml-1.5 rounded px-1 py-0.5 font-mono text-[9px] font-bold uppercase";

export function ActiveRow({ j, onCancel, busy }: { j: SubtitleJob; onCancel?: () => void; busy?: boolean }) {
  const running = j.state === "running";
  const stopping = running && j.note === "stopping…";
  const pct = running && j.progress ? Math.min(100, Math.max(0, j.progress)) : 0;
  // Elapsed since the worker picked it up, and a straight-line ETA once there's enough
  // progress to extrapolate from. The list is polled every 1.5s, so this stays live.
  const elapsed = running && j.started_at ? Date.now() / 1000 - j.started_at : 0;
  const eta = elapsed > 0 && pct >= 5 ? (elapsed * (100 - pct)) / pct : 0;
  // A job waiting for the AI lane names the languages it carries there.
  const waitingAI = !running && j.lane === "gpu" && (j.langs ?? []).length > 0;
  return (
    <div className="flex flex-col gap-1 text-[12px]">
      <div className="flex items-center gap-2.5">
        <StateBadge state={j.state} />
        <span className="min-w-0 flex-1 truncate font-semibold">
          <TitleLink j={j} />
          {j.redo && <span className={`${chipCls} text-ink-faint`} style={{ border: "1px solid var(--line)" }} title="Replacing the subtitles already there">redo</span>}
          {redoLabel(j) && <span className={`${chipCls} text-ink-faint`} style={{ border: "1px solid var(--line)" }} title="Making one language again">{redoLabel(j)}</span>}
          {!running && PRIO_LABEL[j.priority] && <span className={chipCls} style={{ border: "1px solid var(--line)", color: j.priority === 0 ? "var(--accent)" : "var(--ink-faint)" }} title={PRIO_TITLE[j.priority]}>{PRIO_LABEL[j.priority]}</span>}
          {waitingAI && <span className={`${chipCls} text-ink-faint`} style={{ border: "1px solid var(--line)" }} title="Languages left for the AI">{(j.langs ?? []).join(" · ")}</span>}
        </span>
        {stopping ? <span className="font-mono text-[10.5px] text-ink-faint">stopping…</span>
          : running && j.stage && <span className="hidden truncate font-mono text-[10.5px] text-ink-faint sm:inline">{j.stage}</span>}
        {/* On a phone the title needs the room; the bar and % still show progress. */}
        {running && elapsed > 0 && <span className="hidden flex-none font-mono text-[10.5px] text-ink-faint sm:inline">{fmtElapsed(elapsed)}{eta > 0 ? ` · ~${fmtElapsed(eta)} left` : ""}</span>}
        {running && pct > 0 && <span className="w-[38px] flex-none text-right font-mono text-[10.5px] text-ink-dim">{pct}%</span>}
        {running && pct === 0 && <span className="h-3 w-3 flex-none animate-spin rounded-full" style={{ border: "2px solid var(--line)", borderTopColor: "var(--accent)" }} />}
        {/* Stop kills the running ffmpeg/whisper; Remove just drops a queued job. */}
        {onCancel && (
          <button type="button" onClick={onCancel} disabled={busy || stopping} title={running ? "Stop this job" : "Remove from the queue"}
            className="flex-none rounded-md border px-1.5 py-0.5 text-[10.5px] font-semibold hover:bg-[var(--panel-2)] disabled:opacity-50"
            style={{ borderColor: "var(--line)", color: running ? "var(--reject)" : "var(--ink-dim)" }}>
            {running ? "Stop" : "Remove"}
          </button>
        )}
      </div>
      {/* whisper reports progress in 5% steps; anything else finishes before a bar would help. */}
      {running && pct > 0 && (
        <div className="h-1 w-full overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
          <div className="h-full rounded-full" style={{ width: `${pct}%`, background: "var(--accent)", transition: "width 600ms linear" }} />
        </div>
      )}
    </div>
  );
}

// TitleLink opens the job's movie or show.
function TitleLink({ j }: { j: SubtitleJob }) {
  const to = j.kind === "episode" ? (j.series_id ? `/series/${j.series_id}` : null) : j.movie_id ? `/movies/${j.movie_id}` : null;
  if (!to) return <>{j.title}</>;
  return <Link to={to} className="hover:underline" style={{ color: "inherit" }}>{j.title}</Link>;
}

export function fmtElapsed(sec: number): string {
  sec = Math.max(0, Math.round(sec));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}h ${String(m).padStart(2, "0")}m` : m ? `${m}m ${String(s).padStart(2, "0")}s` : `${s}s`;
}

export function StateBadge({ state }: { state: string }) {
  const s = state === "done" ? { bg: "var(--good-soft, rgba(127,176,105,.16))", fg: "var(--good)" }
    : state === "failed" ? { bg: "var(--reject-soft)", fg: "var(--reject)" }
    : state === "running" ? { bg: "var(--accent-soft)", fg: "var(--accent)" }
    : state === "cancelled" ? { bg: "var(--panel-2)", fg: "var(--ink-dim)" }
    : { bg: "var(--panel-2)", fg: "var(--ink-faint)" };
  return <span className="flex-none rounded px-1.5 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: s.bg, color: s.fg }}>{state}</span>;
}

const smallBtn = "rounded-md border px-2 py-1 text-[11px] font-semibold hover:bg-[var(--panel-2)] disabled:opacity-50";

export function Queue({ jobs, backlog, onChange, flash }: { jobs: SubtitleJob[]; backlog: SubtitleQueue | null; onChange: () => void; flash: (m: string) => void }) {
  // Shown the way the workers see it: what's running on top, then what's next, in order —
  // per lane, since the AI lane runs beside extraction and downloads, not after them.
  const active = sortActive(jobs);
  const fast = active.filter((j) => j.lane !== "gpu");
  const gpu = active.filter((j) => j.lane === "gpu");
  const running = active.filter((j) => j.state === "running").length;
  const queuedFast = fast.filter((j) => j.state === "queued").length;
  const queuedGPU = gpu.filter((j) => j.state === "queued").length;
  const [busy, setBusy] = useState(false);
  const confirm = useConfirm();
  const cancel = async (j: SubtitleJob) => {
    setBusy(true);
    try { await api.subtitleCancelJob(j.id); onChange(); }
    catch (e) { flash(e instanceof Error ? e.message : "Couldn't stop that job"); }
    finally { setBusy(false); }
  };
  const clear = async (lane: "fast" | "gpu") => {
    if (lane === "gpu" && !(await confirm({
      title: "Clear the AI backlog?",
      body: `${queuedGPU} file${queuedGPU === 1 ? "" : "s"} waiting for AI transcription leave the queue. What's running now carries on, and the next sweep queues anything still missing again.`,
      confirmLabel: "Clear AI backlog",
      tone: "danger",
    }))) return;
    setBusy(true);
    try {
      const r = await queueApi.clear(lane);
      flash(`Removed ${r.cleared} queued job${r.cleared === 1 ? "" : "s"}.`);
      onChange();
    } catch (e) { flash(e instanceof Error ? e.message : "Couldn't clear the queue"); }
    finally { setBusy(false); }
  };
  const ai = backlogText(backlog);
  return (
    <div className="flex flex-col gap-3.5">
      <div className={card} style={cardStyle}>
        <div className="text-[14px] font-bold">Active <span className="font-mono text-[11px] text-ink-faint">{running} running · {queuedFast + queuedGPU} queued</span></div>
        {active.length === 0 && <div className="mt-2 text-[12px] text-ink-dim">Nothing in the queue right now.</div>}
        {active.length > 0 && (
          <div className="mt-2 flex flex-col gap-3">
            <LaneGroup title="Extract & download" sub="embedded tracks and OpenSubtitles — seconds per file"
              action={queuedFast > 0 && <button type="button" onClick={() => clear("fast")} disabled={busy} className={smallBtn} style={{ borderColor: "var(--line)", color: "var(--ink-dim)" }}>Clear queue</button>}>
              {fast.length === 0 ? <div className="text-[12px] text-ink-faint">Idle.</div> : fast.map((j) => <ActiveRow key={j.id} j={j} onCancel={() => cancel(j)} busy={busy} />)}
            </LaneGroup>
            <LaneGroup title="AI" sub={ai ?? "local whisper — runs beside the lane above"}
              action={queuedGPU > 0 && <button type="button" onClick={() => clear("gpu")} disabled={busy} className={smallBtn} style={{ borderColor: "var(--line)", color: "var(--ink-dim)" }}>Clear AI backlog</button>}>
              {gpu.length === 0 ? <div className="text-[12px] text-ink-faint">Idle.</div> : gpu.map((j) => <ActiveRow key={j.id} j={j} onCancel={() => cancel(j)} busy={busy} />)}
            </LaneGroup>
          </div>
        )}
      </div>
      <History jobs={jobs} onChange={onChange} flash={flash} />
    </div>
  );
}

function LaneGroup({ title, sub, action, children }: { title: string; sub: string; action?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.11em] text-ink-faint">{title}</span>
        <span className="min-w-0 flex-1 text-[11px] text-ink-faint">{sub}</span>
        {action}
      </div>
      {children}
    </div>
  );
}

/* ----------------------------- history ----------------------------- */

const FILTERS: { key: SubtitleJobState | "all"; label: string }[] = [
  { key: "all", label: "All" }, { key: "failed", label: "Failed" }, { key: "skipped", label: "Skipped" },
  { key: "done", label: "Done" }, { key: "cancelled", label: "Cancelled" },
];
const PAGE = 30;

// History is the saved job history (it survives restarts), newest first, a page at a time.
// It reloads when a job finishes in the live list.
function History({ jobs, onChange, flash }: { jobs: SubtitleJob[]; onChange: () => void; flash: (m: string) => void }) {
  const [filter, setFilter] = useState<SubtitleJobState | "all">("all");
  const [rows, setRows] = useState<SubtitleJob[] | null>(null);
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const [retrying, setRetrying] = useState<number | null>(null);
  // The newest finished job the live list knows: when it changes, the first page is stale.
  const newest = jobs.reduce((m, j) => (ACTIVE.has(j.state) ? m : Math.max(m, j.id)), 0);
  const state = filter === "all" ? undefined : filter;

  const load = useCallback(async (before?: number) => {
    setLoading(true);
    try {
      const p = await queueApi.history({ state, before, limit: PAGE });
      // "All" is history: what's still queued or running is the Active card's.
      const done = p.jobs.filter((j) => !ACTIVE.has(j.state));
      setRows((r) => (before ? [...(r ?? []), ...done] : done));
      setNext(p.next_before);
    } catch (e) {
      flash(e instanceof Error ? e.message : "Couldn't load the history");
      setRows((r) => r ?? []);
    } finally { setLoading(false); }
  }, [state, flash]);
  useEffect(() => { void load(); }, [load, newest]);

  const retry = async (j: SubtitleJob) => {
    setRetrying(j.id);
    try { await queueApi.retry(j); flash(`Queued “${j.title}” again`); onChange(); }
    catch (e) { flash(e instanceof Error ? e.message : "Couldn't queue it again"); }
    finally { setRetrying(null); }
  };
  const toggle = (id: number) => setOpen((o) => { const n = new Set(o); if (n.has(id)) n.delete(id); else n.add(id); return n; });

  return (
    <div className={card} style={cardStyle}>
      <div className="flex flex-wrap items-center gap-2">
        <div className="text-[14px] font-bold">History</div>
        <div className="flex-1" />
        <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter the history">
          {FILTERS.map((f) => (
            <button key={f.key} type="button" onClick={() => setFilter(f.key)} aria-pressed={filter === f.key}
              className="rounded-full px-2.5 py-1 text-[10.5px] font-semibold transition-colors"
              style={{ border: `1px solid ${filter === f.key ? "var(--accent)" : "var(--line)"}`, background: filter === f.key ? "var(--accent-soft, rgba(198,93,59,.12))" : "transparent", color: filter === f.key ? "var(--accent)" : "var(--ink-dim)" }}>
              {f.label}
            </button>
          ))}
        </div>
      </div>
      {rows === null ? <div className="mt-2 text-[12px] text-ink-dim">Loading…</div>
        : rows.length === 0 ? <div className="mt-2 text-[12px] text-ink-dim">{filter === "all" ? "Nothing has run yet." : `No ${filter} jobs in the history.`}</div>
        : (
          <div className="mt-2 flex flex-col gap-1.5">
            {rows.map((j) => <HistoryRow key={j.id} j={j} open={open.has(j.id)} onToggle={() => toggle(j.id)} onRetry={() => retry(j)} retrying={retrying === j.id} />)}
          </div>
        )}
      {next > 0 && (
        <button type="button" onClick={() => load(next)} disabled={loading} className={`mt-2.5 ${smallBtn}`} style={{ borderColor: "var(--line)", color: "var(--ink-dim)" }}>
          {loading ? "Loading…" : "Older history"}
        </button>
      )}
    </div>
  );
}

const RETRYABLE = new Set(["failed", "skipped", "cancelled"]);

function HistoryRow({ j, open, onToggle, onRetry, retrying }: { j: SubtitleJob; open: boolean; onToggle: () => void; onRetry: () => void; retrying: boolean }) {
  const outcomes = j.outcomes ?? [];
  const when = j.finished_at || j.at;
  return (
    <div className="flex flex-col gap-1 text-[12px]">
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <StateBadge state={j.state} />
        <span className="min-w-0 flex-1 truncate font-semibold">
          <TitleLink j={j} />
          {j.redo && <span className={`${chipCls} text-ink-faint`} style={{ border: "1px solid var(--line)" }} title="Replaced the subtitles already there">redo</span>}
          {redoLabel(j) && <span className={`${chipCls} text-ink-faint`} style={{ border: "1px solid var(--line)" }} title="Made one language again">{redoLabel(j)}</span>}
        </span>
        {when > 0 && <span className="flex-none font-mono text-[10.5px] text-ink-faint" title={new Date(when * 1000).toLocaleString()}>{ago(when)}</span>}
        {outcomes.length > 0 && (
          <button type="button" onClick={onToggle} aria-expanded={open} className={smallBtn} style={{ borderColor: "var(--line)", color: "var(--ink-dim)" }}>
            {open ? "Hide" : "Why"}
          </button>
        )}
        {RETRYABLE.has(j.state) && (j.movie_id || j.series_id) && (
          <button type="button" onClick={onRetry} disabled={retrying} title="Queue this file again: every kept language still missing gets another go"
            className={smallBtn} style={{ borderColor: "var(--accent-line, var(--line))", color: "var(--accent)" }}>
            {retrying ? "Queuing…" : "Retry"}
          </button>
        )}
        {j.note && <span className="basis-full truncate text-[11.5px] text-ink-faint sm:basis-auto sm:max-w-[45%]" title={j.note}>{j.note}</span>}
      </div>
      {open && (
        <ul className="ml-1 flex flex-col gap-0.5 border-l pl-3" style={{ borderColor: "var(--line-soft)" }}>
          {outcomes.map((o, i) => <OutcomeLine key={i} o={o} />)}
        </ul>
      )}
    </div>
  );
}

const RUNG_LABEL: Record<string, string> = { extract: "Embedded", opensubtitles: "OpenSubtitles", ai: "AI", upgrade: "Upgrade", repair: "Repair" };
const ERROR_RESULTS = new Set(["auth_failed", "provider_error", "ai_failed", "model_missing", "extract_failed"]);

// OutcomeLine is one rung's result for one language: "en · OpenSubtitles — no match (score 41)".
function OutcomeLine({ o }: { o: SubtitleOutcome }) {
  const color = o.result === "ok" ? "var(--good)" : ERROR_RESULTS.has(o.result) ? "var(--reject)" : "var(--ink-dim)";
  const variant = o.variant && o.variant !== "full" ? ` ${o.variant}` : "";
  return (
    <li className="text-[11.5px]" title={o.reasons?.length ? o.reasons.join(", ") : undefined}>
      <span className="font-mono text-[10.5px] font-bold uppercase text-ink-faint">{o.lang}{variant}</span>
      <span className="text-ink-faint"> · {RUNG_LABEL[o.rung] ?? o.rung} — </span>
      <span style={{ color }}>{o.detail || o.result.replace(/_/g, " ")}</span>
      {o.score ? <span className="text-ink-faint"> (score {o.score})</span> : null}
    </li>
  );
}

function ago(at: number): string {
  const sec = Date.now() / 1000 - at;
  if (sec < 60) return "just now";
  if (sec < 3600) return `${Math.round(sec / 60)} min ago`;
  if (sec < 48 * 3600) return `${Math.round(sec / 3600)} h ago`;
  return `${Math.round(sec / 86400)} days ago`;
}
